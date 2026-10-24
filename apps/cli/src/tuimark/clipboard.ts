import { accessSync, constants, realpathSync, statSync } from "node:fs";
import { delimiter, isAbsolute, join, parse, resolve, sep } from "node:path";

// The system clipboard, for `/copy` and ctrl+y, and the native-selection hint
// that goes with it.
//
// Tuimark owns the TTY, so nothing here writes an OSC 52 sequence: the text
// goes to a clipboard tool's stdin. The tools are tried in order and the first
// that runs and exits 0 wins; `which` is not trusted (wl-copy exists on a
// machine with no Wayland session, and xclip/xsel need a display).

/** A clipboard tool and the arguments that make it read stdin into the clipboard. */
export interface ClipboardTool {
  /** What the status names: the program, without its arguments. */
  name: string;
  argv: readonly string[];
}

/** The tools worth trying on `platform`, in order. */
export function clipboardTools(
  platform: NodeJS.Platform = process.platform,
): ClipboardTool[] {
  if (platform === "darwin") return [{ name: "pbcopy", argv: ["pbcopy"] }];
  if (platform === "linux")
    return [
      { name: "wl-copy", argv: ["wl-copy"] },
      { name: "xclip", argv: ["xclip", "-selection", "clipboard"] },
      { name: "xsel", argv: ["xsel", "--clipboard", "--input"] },
    ];
  return [];
}

/** The tools the "no clipboard tool" status lists, whatever the platform. */
export const CLIPBOARD_TOOL_NAMES = "pbcopy, wl-copy, xclip, xsel";

/**
 * What one attempt came to: the program is not there, it ran and exited
 * with this code (or was killed after the timeout), or it took the text.
 */
export type RunResult =
  | { kind: "missing" }
  | { kind: "exited"; code: number }
  | { kind: "timeout" };

/** Runs `argv` (an absolute program path first) with `text` on its stdin. Injected by tests. */
export type RunTool = (
  argv: readonly string[],
  text: string,
  env: Record<string, string>,
) => Promise<RunResult>;

/** How long a clipboard tool may take before it counts as hung. */
export const COPY_TIMEOUT_MS = 3000;

/**
 * The real runner: a spawned process with exactly `env`, its output ignored
 * (xclip and xsel fork and keep it). The timer is armed before the first
 * byte is written and covers the feed as well as the exit: a tool that
 * never reads its stdin blocks the write of a large text for good, and is
 * killed when the time is up.
 */
export async function spawnTool(
  argv: readonly string[],
  text: string,
  env: Record<string, string>,
  timeoutMs = COPY_TIMEOUT_MS,
): Promise<RunResult> {
  let proc: ReturnType<typeof Bun.spawn<"pipe", "ignore", "ignore">>;
  try {
    proc = Bun.spawn([...argv], {
      stdin: "pipe",
      stdout: "ignore",
      stderr: "ignore",
      env,
    });
  } catch {
    // ENOENT: no such program.
    return { kind: "missing" };
  }
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<"timeout">((resolve) => {
    timer = setTimeout(() => resolve("timeout"), timeoutMs);
  });
  const run = (async (): Promise<number> => {
    try {
      proc.stdin.write(text);
      await proc.stdin.end();
    } catch {
      // The tool closed its end early; its exit code says why.
    }
    return proc.exited;
  })();
  try {
    const outcome = await Promise.race([run, timeout]);
    if (outcome === "timeout") {
      // A hung tool may ignore SIGTERM, and would outlive the client.
      proc.kill("SIGKILL");
      // The feed may still be blocked on the pipe; the kill ends it.
      run.catch(() => {});
      return { kind: "timeout" };
    }
    return { kind: "exited", code: outcome };
  } finally {
    clearTimeout(timer);
  }
}

/** What a copy came to. */
export type CopyResult =
  | { ok: true; tool: string }
  /** None of the tools exists, or none could take the text. */
  | { ok: false; reason: "none" }
  /** A tool ran and failed (the last one tried). */
  | { ok: false; reason: "failed"; tool: string; detail: string }
  /**
   * A tool did not finish in time. The chain stops there: it may already
   * have set the clipboard, and the next tool would write it twice.
   */
  | { ok: false; reason: "timeout"; tool: string };

// ------------------------------------------------------------- the environment

const UTF8_LOCALE = "en_US.UTF-8";

/** What a clipboard tool needs of the environment; nothing else is passed on. */
const ENV_KEEP = [
  "PATH",
  "HOME",
  "LANG",
  "LC_ALL",
  "LC_CTYPE",
  "DISPLAY",
  "WAYLAND_DISPLAY",
  "XDG_RUNTIME_DIR",
  "XAUTHORITY",
  "DBUS_SESSION_BUS_ADDRESS",
];

/**
 * The environment a clipboard tool runs with: the few variables it needs
 * (the session's display, its locale), never the rest of the process's, which
 * holds provider keys. pbcopy decodes its input by the locale, and the text
 * is UTF-8: with no locale it gets a UTF-8 one, and with one that is not
 * UTF-8 (LANG=C, LC_ALL=POSIX) its character type becomes UTF-8.
 */
export function clipboardEnv(
  source: Record<string, string | undefined>,
  platform: NodeJS.Platform = process.platform,
): Record<string, string> {
  const env: Record<string, string> = {};
  const keep =
    platform === "darwin" ? [...ENV_KEEP, "__CF_USER_TEXT_ENCODING"] : ENV_KEEP;
  for (const name of keep) {
    const value = source[name];
    if (value !== undefined && value !== "") env[name] = value;
  }
  const ctype = env.LC_ALL || env.LC_CTYPE || env.LANG;
  if (!ctype) env.LANG = UTF8_LOCALE;
  else if (!/utf-?8/i.test(ctype)) {
    // LC_ALL wins over LC_CTYPE.
    delete env.LC_ALL;
    env.LC_CTYPE = UTF8_LOCALE;
  }
  return env;
}

// ------------------------------------------------------------------ the lookup

function realOrResolved(path: string): string {
  try {
    return realpathSync(path);
  } catch {
    return resolve(path);
  }
}

/** `dir` is `root` or lies inside it. */
function inside(dir: string, root: string): boolean {
  return dir === root || dir.startsWith(root.endsWith(sep) ? root : root + sep);
}

function isExecutableFile(path: string): boolean {
  try {
    if (!statSync(path).isFile()) return false;
    accessSync(path, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

/**
 * The absolute path of `name`, found only through absolute PATH entries and
 * never in a directory inside `exclude` (the workspace, the working
 * directory): a "." or empty entry, or a PATH that runs through the
 * project, must not run a `pbcopy` somebody planted there. Undefined when
 * there is none.
 */
export function resolveTool(
  name: string,
  pathEnv: string | undefined,
  exclude: readonly string[],
  isExecutable: (path: string) => boolean = isExecutableFile,
): string | undefined {
  // The filesystem root excludes nothing worth excluding: it would exclude all.
  const roots = exclude
    .map(realOrResolved)
    .filter((root) => root !== parse(root).root);
  for (const entry of (pathEnv ?? "").split(delimiter)) {
    if (!isAbsolute(entry)) continue;
    const dir = realOrResolved(entry);
    if (roots.some((root) => inside(dir, root))) continue;
    const candidate = join(dir, name);
    if (isExecutable(candidate)) return candidate;
  }
  return undefined;
}

/**
 * Copies `text` to the system clipboard with the first tool that works.
 * The options are for tests; the defaults are the platform's tools, the
 * process's PATH without the working directory, and a spawned process.
 */
export async function copyToClipboard(
  text: string,
  options: {
    tools?: readonly ClipboardTool[];
    run?: RunTool;
    /** Directories no tool is taken from (the workspace root). The cwd is always one. */
    exclude?: readonly string[];
    /** The environment to take PATH and the display variables from. */
    env?: Record<string, string | undefined>;
    platform?: NodeJS.Platform;
    /** Replaces the PATH lookup of a program name. */
    resolve?: (name: string) => string | undefined;
  } = {},
): Promise<CopyResult> {
  const platform = options.platform ?? process.platform;
  const tools = options.tools ?? clipboardTools(platform);
  const run = options.run ?? spawnTool;
  const source = options.env ?? process.env;
  const exclude = [process.cwd(), ...(options.exclude ?? [])];
  const find =
    options.resolve ??
    ((name: string) => resolveTool(name, source.PATH, exclude));
  const env = clipboardEnv(source, platform);
  let failure: { tool: string; detail: string } | undefined;
  for (const tool of tools) {
    const [program, ...args] = tool.argv;
    const path = program === undefined ? undefined : find(program);
    if (path === undefined) continue;
    const result = await run([path, ...args], text, env);
    if (result.kind === "missing") continue;
    if (result.kind === "exited" && result.code === 0)
      return { ok: true, tool: tool.name };
    if (result.kind === "timeout")
      return { ok: false, reason: "timeout", tool: tool.name };
    failure = {
      tool: tool.name,
      detail: `exited with code ${result.kind === "exited" ? result.code : "?"}`,
    };
  }
  // A tool that ran and failed is reported over the ones that are not there:
  // wl-copy without a Wayland session fails, and xclip after it may then be
  // absent, and "no clipboard tool" would hide the real reason.
  return failure
    ? { ok: false, reason: "failed", ...failure }
    : { ok: false, reason: "none" };
}

/** `1234` as `1,234`, whatever the machine's locale. */
export function charCount(n: number): string {
  return n.toLocaleString("en-US");
}

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

/** How many characters a reader sees in `text`: grapheme clusters, not code units or code points. */
export function characters(text: string): number {
  let n = 0;
  for (const _ of graphemes.segment(text)) n++;
  return n;
}

/**
 * What goes on the clipboard of `text`: the model's own words, without the
 * control bytes the view strips too (ESC sequences, C0 other than tab and
 * newline, DEL, C1). A tab stays a tab.
 */
export function clipboardText(text: string): string {
  return Bun.stripANSI(text.replace(/\r\n/g, "\n")).replace(
    // biome-ignore lint/suspicious/noControlCharactersInRegex: strip terminal control characters from untrusted text
    /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/g,
    "",
  );
}

// ------------------------------------------------------------ native selection

/**
 * While mouse reporting is on, a terminal selects text natively only with a
 * modifier held while dragging: Shift in Ghostty, kitty, WezTerm and most
 * others, Option in iTerm2, Fn in Terminal.app. `termProgram` is
 * `$TERM_PROGRAM`.
 */
export function selectionKey(termProgram: string | undefined): string {
  if (termProgram === "iTerm.app") return "option";
  if (termProgram === "Apple_Terminal") return "fn";
  return "shift";
}

/**
 * How to select text natively right now: the modifier-drag while mouse
 * reporting is on, a plain drag when it is off or there is no mouse
 * (inline, where the terminal's own selection works as usual).
 */
export function selectionHint(
  mouse: boolean,
  termProgram: string | undefined,
): string {
  return mouse ? `${selectionKey(termProgram)}-drag` : "drag";
}

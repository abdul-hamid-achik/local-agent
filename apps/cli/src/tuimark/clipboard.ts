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

/** Runs `argv` with `text` on its stdin. Injected by tests. */
export type RunTool = (
  argv: readonly string[],
  text: string,
) => Promise<RunResult>;

/** How long a clipboard tool may take before it counts as hung. */
export const COPY_TIMEOUT_MS = 3000;

/** The real runner: a spawned process, its output ignored (xclip and xsel fork and keep it). */
export const spawnTool: RunTool = async (argv, text) => {
  let proc: ReturnType<typeof Bun.spawn<"pipe", "ignore", "ignore">>;
  try {
    proc = Bun.spawn([...argv], {
      stdin: "pipe",
      stdout: "ignore",
      stderr: "ignore",
    });
  } catch {
    // ENOENT: no such program.
    return { kind: "missing" };
  }
  try {
    proc.stdin.write(text);
    await proc.stdin.end();
  } catch {
    // The tool closed its end early; its exit code says why.
  }
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<"timeout">((resolve) => {
    timer = setTimeout(() => resolve("timeout"), COPY_TIMEOUT_MS);
  });
  const code = await Promise.race([proc.exited, timeout]);
  clearTimeout(timer);
  if (code === "timeout") {
    proc.kill();
    return { kind: "timeout" };
  }
  return { kind: "exited", code };
};

/** What a copy came to. */
export type CopyResult =
  | { ok: true; tool: string }
  /** None of the tools exists, or none could take the text. */
  | { ok: false; reason: "none" }
  /** A tool ran and failed (the last one tried). */
  | { ok: false; reason: "failed"; tool: string; detail: string };

/**
 * Copies `text` to the system clipboard with the first tool that works.
 * `tools` and `run` are for tests; the defaults are the platform's tools
 * and a spawned process.
 */
export async function copyToClipboard(
  text: string,
  options: {
    tools?: readonly ClipboardTool[];
    run?: RunTool;
  } = {},
): Promise<CopyResult> {
  const tools = options.tools ?? clipboardTools();
  const run = options.run ?? spawnTool;
  let failure: { tool: string; detail: string } | undefined;
  for (const tool of tools) {
    const result = await run(tool.argv, text);
    if (result.kind === "missing") continue;
    if (result.kind === "exited" && result.code === 0)
      return { ok: true, tool: tool.name };
    failure = {
      tool: tool.name,
      detail:
        result.kind === "timeout"
          ? "did not finish in time"
          : `exited with code ${result.kind === "exited" ? result.code : "?"}`,
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

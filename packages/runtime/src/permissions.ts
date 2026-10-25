// Durable permission rules: the `.local-agent/permissions.json` file a user
// (or the client's "always" key) writes so repeated approvals become policy.
// The file is loaded by the client on demand (mtime-checked) and evaluated
// before any approval dialog or headless flag; nothing here talks to the
// model, and every matcher is pure string work over the request's target.
//
// The file sits inside the workspace, so a cloned repository can ship one.
// Deny and ask rules only ever restrict and always apply; an allow rule
// applies only once this user trusted it on this machine. Trust lives
// outside every workspace, in `trusted-rules.json` under the user's config
// directory, keyed by the workspace's real path and directory identity: the
// "always" key and `/permissions trust` write it, nothing a repository
// contains can.

import { randomUUID } from "node:crypto";
import {
  link,
  lstat,
  mkdir,
  open,
  readFile,
  realpath,
  rename,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { dirname, join } from "node:path";
import type { ApprovalRequest } from "#runtime/agent";
import { userConfigDir } from "#runtime/paths";

/** What a rule does to the requests it matches. "ask" is the dialog. */
export type PermissionEffect = "allow" | "ask" | "deny";

/** The tool families rules know about. "edit" covers edits and writes. */
export type PermissionTool =
  | "edit"
  | "run_command"
  | "fetch"
  | "web_search"
  | "mcp";

export interface PermissionRule {
  effect: PermissionEffect;
  tool: PermissionTool;
  /**
   * Tool-specific: a gitignore-style path glob (edit), an argv-prefix with
   * trailing `*` (run_command), `domain:host` or `*` (fetch, web_search),
   * or `server__tool` with `*` wildcards (mcp).
   */
  pattern: string;
}

export interface PermissionFile {
  version: 1;
  rules: PermissionRule[];
}

/** What is being checked: the tool family and its string target. */
export interface PermissionRequest {
  tool: PermissionTool;
  /**
   * edit: workspace-relative path · run_command: the space-joined argv ·
   * fetch: the URL hostname · web_search: the query · mcp: `SERVER__TOOL`
   * (through a gateway, `GATEWAY__SERVER__TOOL`: the tool that runs).
   * Commands may also carry their argv so `proposeRule` never re-splits.
   */
  target: string;
  /** The raw argv, when the request came from a command approval. */
  argv?: readonly string[];
  /**
   * mcp: other names for the same call, which deny and ask rules match
   * too (never allow): the gateway's own `GATEWAY__call_tool` and the other
   * names it may run the tool by.
   */
  also?: readonly string[];
}

/** The empty file: what a missing or unreadable one reads as. */
export const NO_PERMISSIONS: PermissionFile = { version: 1, rules: [] };

/** The verdict of `evaluate`: the effect that applies, and the rule that set it. */
export type PermissionVerdict = {
  effect: PermissionEffect;
  rule?: PermissionRule;
};

const RULES_MAX = 100;

function isTool(value: unknown): value is PermissionTool {
  return (
    value === "edit" ||
    value === "run_command" ||
    value === "fetch" ||
    value === "web_search" ||
    value === "mcp"
  );
}

function parseRules(value: unknown): PermissionRule[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const rules: PermissionRule[] = [];
  for (const raw of value) {
    if (typeof raw !== "object" || raw === null) return undefined;
    const { effect, tool, pattern } = raw as Record<string, unknown>;
    if (
      (effect !== "allow" && effect !== "ask" && effect !== "deny") ||
      !isTool(tool) ||
      typeof pattern !== "string"
    )
      return undefined;
    rules.push({ effect, tool, pattern });
  }
  return rules;
}

/** Where the file lives under a workspace root. */
export function permissionsPath(workspaceRoot: string): string {
  return join(workspaceRoot, ".local-agent", "permissions.json");
}

/** The per-user record of trusted allow rules, outside every workspace. */
export function trustStorePath(
  env: Record<string, string | undefined> = process.env,
): string {
  return join(userConfigDir(env), "trusted-rules.json");
}

type TrustStore = { version: 1; workspaces: Record<string, string[]> };

const ruleKey = (rule: PermissionRule): string =>
  JSON.stringify([rule.tool, rule.pattern]);

/**
 * The store as stored. Missing is the empty store; a damaged one (bad JSON
 * or shape) trusts nothing, and is never rewritten, so a repair keeps the
 * other workspaces' trust.
 */
async function readTrust(): Promise<{ store: TrustStore; damaged: boolean }> {
  let text: string;
  try {
    text = await readFile(trustStorePath(), "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT")
      return { store: { version: 1, workspaces: {} }, damaged: false };
    return { store: { version: 1, workspaces: {} }, damaged: true };
  }
  try {
    const parsed = JSON.parse(text);
    if (
      parsed?.version === 1 &&
      typeof parsed.workspaces === "object" &&
      parsed.workspaces !== null &&
      !Array.isArray(parsed.workspaces)
    ) {
      const workspaces: Record<string, string[]> = {};
      for (const [root, keys] of Object.entries(parsed.workspaces))
        if (Array.isArray(keys))
          workspaces[root] = keys.filter(
            (key): key is string => typeof key === "string",
          );
      return { store: { version: 1, workspaces }, damaged: false };
    }
  } catch {
    // Not JSON: damaged.
  }
  return { store: { version: 1, workspaces: {} }, damaged: true };
}

const LOCK_WAIT_MS = 2_000;
const LOCK_STALE_MS = 10_000;

/**
 * One writer at a time across processes, for every change to the trust
 * store or to a workspace's permission file: an exclusive lock file beside
 * the store. A lock a crashed holder left stale is moved aside (one taker
 * wins the rename) and removed only if it is the stale one; a holder
 * releases only its own lock.
 */
async function withPolicyLock<T>(work: () => Promise<T>): Promise<T> {
  const lock = `${trustStorePath()}.lock`;
  await mkdir(dirname(lock), { recursive: true, mode: 0o700 });
  const deadline = Date.now() + LOCK_WAIT_MS;
  let mine: bigint | undefined;
  while (mine === undefined) {
    try {
      const handle = await open(lock, "wx", 0o600);
      try {
        mine = (await handle.stat({ bigint: true })).ino;
      } finally {
        await handle.close();
      }
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
      const held = await stat(lock, { bigint: true }).catch(() => undefined);
      if (held && Date.now() - Number(held.mtimeMs) > LOCK_STALE_MS) {
        const aside = `${lock}.${randomUUID()}.stale`;
        await rename(lock, aside).catch(() => undefined);
        const moved = await stat(aside, { bigint: true }).catch(
          () => undefined,
        );
        // A live lock taken in between goes back, unless the slot is taken.
        if (moved && moved.ino !== held.ino)
          await link(aside, lock).catch(() => undefined);
        await rm(aside, { force: true });
      } else if (Date.now() > deadline)
        throw new Error(`trust store is locked (${lock})`);
      else await Bun.sleep(20);
    }
  }
  try {
    return await work();
  } finally {
    const held = await stat(lock, { bigint: true }).catch(() => undefined);
    if (held?.ino === mine) await rm(lock, { force: true });
  }
}

/** Refuses a symlink (or the wrong kind of entry) where the host writes its
 * policy, so a planted link never redirects a write outside the workspace. */
async function assertPlain(
  path: string,
  kind: "directory" | "file",
): Promise<void> {
  const info = await lstat(path).catch((error) => {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return undefined;
    throw error;
  });
  if (
    info &&
    (info.isSymbolicLink() ||
      (kind === "directory" ? !info.isDirectory() : !info.isFile()))
  )
    throw new Error(`refused: ${path} is not a plain ${kind}`);
}

/** The permission file, written atomically through a plain `.local-agent`. */
async function writePermissionFile(
  workspaceRoot: string,
  file: PermissionFile,
): Promise<string> {
  const dir = join(workspaceRoot, ".local-agent");
  await assertPlain(dir, "directory");
  await mkdir(dir, { recursive: true });
  await assertPlain(dir, "directory");
  const path = permissionsPath(workspaceRoot);
  await assertPlain(path, "file");
  const temp = `${path}.${randomUUID()}.tmp`;
  try {
    await writeFile(temp, `${JSON.stringify(file, null, 2)}\n`, {
      flag: "wx",
    });
    await rename(temp, path);
  } catch (error) {
    await rm(temp, { force: true });
    throw error;
  }
  return path;
}

/** Atomic and private: a temp file in the same directory, then a rename. */
async function writeTrust(store: TrustStore): Promise<void> {
  const file = trustStorePath();
  await mkdir(dirname(file), { recursive: true, mode: 0o700 });
  const temp = `${file}.${randomUUID()}.tmp`;
  try {
    await writeFile(temp, `${JSON.stringify(store, null, 2)}\n`, {
      mode: 0o600,
    });
    await rename(temp, file);
  } catch (error) {
    await rm(temp, { force: true });
    throw error;
  }
}

/**
 * The store's key for a workspace: its real path plus the directory's
 * identity (device and inode), so a clone at another path starts
 * untrusted, and so does a new directory at the same path, unless the
 * filesystem reused the old one's inode.
 */
async function workspaceKey(
  workspaceRoot: string,
): Promise<string | undefined> {
  try {
    const real = await realpath(workspaceRoot);
    const info = await stat(real, { bigint: true });
    return `${real}#${info.dev}:${info.ino}`;
  } catch {
    return undefined;
  }
}

/** Changes one workspace's trusted keys; the caller holds the lock. */
async function changeTrust(
  workspaceRoot: string,
  change: (keys: Set<string>) => void,
): Promise<void> {
  const { store, damaged } = await readTrust();
  if (damaged)
    throw new Error(
      `the trust store ${trustStorePath()} is damaged; repair or remove it`,
    );
  const root = await workspaceKey(workspaceRoot);
  if (root === undefined)
    throw new Error(`cannot identify the workspace ${workspaceRoot}`);
  const keys = new Set(store.workspaces[root] ?? []);
  change(keys);
  if (keys.size) store.workspaces[root] = [...keys];
  else delete store.workspaces[root];
  await writeTrust(store);
}

/**
 * Every rule the file holds, trusted or not. Missing → the empty file;
 * unreadable or schema-invalid → the empty file plus a warning. Callers
 * that enforce rules use `loadPermissionFile`; this one is for rewriting
 * the file and for listing it.
 */
export async function readPermissionFile(
  workspaceRoot: string,
): Promise<PermissionFile & { warning?: string }> {
  let text: string;
  try {
    text = await readFile(permissionsPath(workspaceRoot), "utf8");
  } catch {
    return { ...NO_PERMISSIONS };
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (error) {
    return {
      ...NO_PERMISSIONS,
      warning: `permissions.json is not valid JSON (${(error as Error).message}); ignored`,
    };
  }
  if (typeof parsed !== "object" || parsed === null) {
    return {
      ...NO_PERMISSIONS,
      warning: "permissions.json: not an object; ignored",
    };
  }
  const { version, rules } = parsed as Record<string, unknown>;
  if (version !== 1) {
    return {
      ...NO_PERMISSIONS,
      warning: `permissions.json: unsupported version ${String(version)}; ignored`,
    };
  }
  const parsedRules = parseRules(rules);
  if (!parsedRules) {
    return {
      ...NO_PERMISSIONS,
      warning: "permissions.json: invalid rules array; ignored",
    };
  }
  return { version: 1, rules: parsedRules };
}

/**
 * Reads `<root>/.local-agent/permissions.json` for enforcement. Missing →
 * the empty file; unreadable or schema-invalid → the empty file plus a
 * warning string. Allow rules this user never trusted are left out (listed
 * in `untrusted`, with a warning); deny and ask rules always apply. Never
 * throws: a bad file degrades to "ask", never to a crash.
 */
export async function loadPermissionFile(
  workspaceRoot: string,
): Promise<PermissionFile & { warning?: string; untrusted: PermissionRule[] }> {
  const file = await readPermissionFile(workspaceRoot);
  if (file.warning || !file.rules.some((rule) => rule.effect === "allow"))
    return { ...file, untrusted: [] };
  const { store, damaged } = await readTrust();
  const key = await workspaceKey(workspaceRoot);
  const trusted = new Set((key !== undefined && store.workspaces[key]) || []);
  const untrusted = file.rules.filter(
    (rule) => rule.effect === "allow" && !trusted.has(ruleKey(rule)),
  );
  if (untrusted.length === 0) return { ...file, untrusted };
  const count = untrusted.length;
  const named = untrusted
    .slice(0, 3)
    .map((rule) => `${rule.tool} ${rule.pattern}`)
    .join(", ");
  return {
    version: 1,
    rules: file.rules.filter((rule) => !untrusted.includes(rule)),
    untrusted,
    warning: damaged
      ? `the trust store ${trustStorePath()} is damaged, so no allow rule in permissions.json applies; repair or remove it`
      : `permissions.json: ${count} allow rule${count === 1 ? "" : "s"} not trusted on this machine ${count === 1 ? "is" : "are"} ignored (${named}${count > 3 ? ", …" : ""}); review them, then run /permissions trust (or local-agent --trust-rules)`,
  };
}

// ------------------------------------------------------------------- matchers

/** Escapes a regex metacharacter sequence for literal matching. */
const reEsc = (text: string): string =>
  text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/**
 * A gitignore-style path glob: `*` spans one segment, `**` spans any, a
 * leading `/` anchors to the workspace root, and a `\` escapes the next
 * character (so a literal `*` in a filename can be ruled on). Unanchored
 * patterns also match a path suffix that starts on a segment boundary.
 */
function pathGlobMatches(pattern: string, path: string): boolean {
  if (pattern === "" || pattern === "*" || pattern === "/") return true;
  const anchored = pattern.startsWith("/");
  const body = anchored ? pattern.slice(1) : pattern;
  let re = "";
  for (let i = 0; i < body.length; i++) {
    const c = body.charAt(i);
    if (c === "\\" && i + 1 < body.length) {
      re += reEsc(body.charAt(++i));
    } else if (c === "*") {
      if (body[i + 1] === "*") {
        // `**` spans any segments: `a/**/b` matches `a/b` and `a/x/b`; a
        // trailing `**` (as in `src/**`) matches everything below it.
        while (body[i + 1] === "*") i++;
        if (body[i + 1] === "/") {
          i++;
          re += i + 1 >= body.length ? ".*" : "(?:.*/)?";
        } else {
          re += ".*";
        }
      } else {
        re += "[^/]*";
      }
    } else if (c === "?") {
      re += "[^/]";
    } else {
      re += reEsc(c);
    }
  }
  const prefix = anchored ? "^" : "^(?:.*/)?";
  return new RegExp(`${prefix}${re}$`).test(path);
}

const splitWords = (text: string): string[] =>
  text.trim().split(/\s+/).filter(Boolean);

/**
 * A command pattern's words: whitespace separates them, `"…"` quotes one
 * (with `\"` and `\\` escapes inside), and a final bare `*` makes the
 * pattern a prefix. A quoted `"*"` is a literal argument.
 */
function commandPattern(
  pattern: string,
): { words: string[]; star: boolean; attached?: boolean } | undefined {
  const words: string[] = [];
  let star = false;
  let attached = false;
  let i = 0;
  while (i < pattern.length) {
    const c = pattern.charAt(i);
    if (/\s/.test(c)) {
      i++;
      continue;
    }
    if (c === '"') {
      let word = "";
      i++;
      for (;;) {
        if (i >= pattern.length) return undefined;
        const d = pattern.charAt(i++);
        if (d === '"') break;
        word += d === "\\" && i < pattern.length ? pattern.charAt(i++) : d;
      }
      words.push(word);
      continue;
    }
    let word = "";
    while (i < pattern.length && !/\s/.test(pattern.charAt(i)))
      word += pattern.charAt(i++);
    // `git*` is the older spelling of `git *`.
    if (word.endsWith("*") && pattern.slice(i).trim() === "") {
      star = true;
      if (word !== "*") {
        words.push(word.slice(0, -1));
        attached = true;
      }
    } else words.push(word);
  }
  return attached ? { words, star, attached } : { words, star };
}

/** One argv word as a pattern word: quoted when it would not survive bare. */
function quoteWord(word: string): string {
  return word === "" || word.endsWith("*") || /[\s"\\]/.test(word)
    ? `"${word.replace(/[\\"]/g, (c) => `\\${c}`)}"`
    : word;
}

/**
 * Programs that run another program named in their arguments. Session
 * grants for them are exact, and deny/ask rules look through them.
 */
export const COMMAND_WRAPPERS: ReadonlySet<string> = new Set([
  "env",
  "xargs",
  "sudo",
  "doas",
  "su",
  "nice",
  "nohup",
  "time",
  "timeout",
  "command",
  "exec",
  "watch",
  "caffeinate",
  "chroot",
  "ssh",
  "find",
  "parallel",
  "script",
  "busybox",
]);

const SHELLS = new Set(["sh", "bash", "zsh", "dash", "ksh", "fish", "csh"]);

/** Wrapper options that take the next word as their value. */
const OPTION_VALUES: Record<string, ReadonlySet<string>> = {
  sudo: new Set(["-u", "-g", "-C", "-p", "-h", "-U", "-r", "-t", "-D"]),
  doas: new Set(["-u", "-C"]),
  su: new Set(["-c", "-s", "-g", "-G"]),
  env: new Set(["-u", "-C", "-S", "--unset", "--chdir"]),
  timeout: new Set(["-s", "-k", "--signal", "--kill-after"]),
  nice: new Set(["-n", "--adjustment"]),
  xargs: new Set(["-I", "-n", "-P", "-L", "-s", "-d", "-E", "-a"]),
  watch: new Set(["-n", "-d", "--interval"]),
  ssh: new Set(["-p", "-i", "-l", "-o", "-F", "-J", "-L", "-R", "-D", "-E"]),
  exec: new Set(["-a"]),
  script: new Set(["-c", "-t", "-T", "-F"]),
};

/** A program's name for matching: its basename, lowercased (APFS and NTFS
 * resolve `RM` to `rm`). */
const programName = (word: string): string =>
  (word.split("/").at(-1) ?? "").toLowerCase();

/**
 * The commands an argv runs besides itself, best effort, for deny and ask
 * rules: what follows a wrapper's options and assignments, a shell's `-c`
 * script split at `;`, `&`, `|` and newlines, and `find -exec …`. Not a
 * shell parser: deny rules are a guard rail, not a sandbox.
 */
function innerCommands(argv: readonly string[], depth = 0): string[][] {
  if (depth > 4 || argv.length < 2) return [];
  const program = programName(argv[0] ?? "");
  const found: string[][] = [];
  const add = (inner: string[]) => {
    if (inner.length === 0) return;
    found.push(inner, ...innerCommands(inner, depth + 1));
  };
  // A shell script's commands, split at `;`, `&`, `|` and newlines, with
  // quotes and backslashes dropped as a shell would (`r''m` is `rm`).
  const script = (text: string) => {
    for (const segment of text.split(/[;&|\n]+/))
      add(splitWords(segment.replace(/["'\\]/g, "").replace(/[`()]/g, " ")));
  };
  // `-c SCRIPT` (or `-lc`, `--command`) for shells, su and script(1).
  const dashC = (shell: boolean) => {
    const at = argv.findIndex(
      (word, i) =>
        i > 0 &&
        (word === "--command" ||
          (shell ? /^-[a-z]*c[a-z]*$/i.test(word) : word === "-c")),
    );
    const text = at > 0 ? argv[at + 1] : undefined;
    if (text !== undefined) script(text);
    return text !== undefined;
  };
  if (SHELLS.has(program)) {
    dashC(true);
  } else if (program === "su") {
    dashC(false);
  } else if (program === "find") {
    const at = argv.findIndex((word) =>
      /^-(exec|execdir|ok|okdir)$/.test(word),
    );
    if (at > 0) {
      const end = argv.findIndex((word, i) => i > at && /^[;+]$/.test(word));
      add(argv.slice(at + 1, end < 0 ? undefined : end));
    }
  } else if (program === "script" && dashC(false)) {
    // `script -c CMD FILE` (util-linux): the command is the -c value.
  } else if (COMMAND_WRAPPERS.has(program)) {
    // Skip options (and the values some take), env-style assignments and
    // bare numbers (`timeout 5`, `nice -n 10`); then, for ssh, the host.
    const values = OPTION_VALUES[program];
    let i = 1;
    while (i < argv.length) {
      const word = argv[i] ?? "";
      // `env -S "rm x"` splits its value into a command line.
      if (program === "env") {
        const split = word.match(/^(?:-S|--split-string=?)(.*)$/s);
        if (split) {
          const value = split[1] || argv[++i] || "";
          add(splitWords(value));
          i++;
          continue;
        }
      }
      if (values?.has(word)) i += 2;
      else if (/^(-|\d|[A-Za-z_][A-Za-z0-9_]*=)/.test(word)) i++;
      else break;
    }
    if (program === "ssh") {
      // The remote shell joins and re-splits what follows the host.
      script(argv.slice(i + 1).join(" "));
      return found;
    }
    // BSD script(1): `script [FILE [COMMAND …]]`.
    if (program === "script") i++;
    add(argv.slice(i));
  }
  return found;
}

/** The pre-quoting spelling: whitespace-split words and a trailing `*`. */
function legacyPattern(pattern: string): {
  words: string[];
  star: boolean;
  attached?: boolean;
} {
  const trimmed = pattern.trimEnd();
  const star = trimmed.endsWith("*");
  return { words: splitWords(star ? trimmed.slice(0, -1) : trimmed), star };
}

/**
 * An argv pattern, exact unless it ends with a bare `*`: `npm test *`
 * matches `npm test` itself and `npm test anything`. One approval is one
 * argv vector (no shell), so words compare one to one, never re-split.
 *
 * Allow rules match the argv as given and nothing else: never a planted
 * `./git` for `git *`, never an empty or malformed pattern. Deny and ask
 * rules lean the other way: the program matches by name, case-insensitively
 * (`rm *` refuses `/bin/rm` and `RM`), the commands a wrapper or `sh -c`
 * would run are checked too, an empty pattern matches every command (as it
 * always did), and a pattern with an unbalanced quote falls back to plain
 * words instead of silently matching nothing.
 */
function commandMatches(
  rule: PermissionRule,
  request: PermissionRequest,
): boolean {
  const restrict = rule.effect !== "allow";
  const parsed =
    commandPattern(rule.pattern) ??
    (restrict ? legacyPattern(rule.pattern) : undefined);
  if (!parsed) return false;
  // `cat notes*` reads as "notes, then any arguments", not a glob: only the
  // program word may carry the old attached star in an allow rule.
  if (!restrict && parsed.attached && parsed.words.length > 1) return false;
  const { words: core, star } = parsed;
  if (core.length === 0) return star || restrict;
  const argv = request.argv?.length
    ? [...request.argv]
    : splitWords(request.target);
  const matches = (words: readonly string[]) =>
    (star ? words.length >= core.length : words.length === core.length) &&
    core.every(
      (word, i) =>
        word === words[i] ||
        (i === 0 &&
          restrict &&
          !word.includes("/") &&
          programName(words[0] ?? "") === word.toLowerCase()),
    );
  return matches(argv) || (restrict && innerCommands(argv).some(matches));
}

/**
 * `domain:host`: `*` (or empty) matches any host; `*.example.com` matches
 * subdomains only (never the root); anything else is exact.
 */
function domainMatches(pattern: string, host: string): boolean {
  const domain = pattern.startsWith("domain:")
    ? pattern.slice("domain:".length)
    : pattern;
  if (domain === "" || domain === "*") return true;
  if (domain.startsWith("*.")) {
    const root = domain.slice(2);
    return host.endsWith(`.${root}`) && host.length > root.length + 1;
  }
  return domain === host;
}

/**
 * A path as a case-insensitive, normalization-insensitive filesystem sees
 * it (macOS's default APFS, Windows): `src/Secrets/k` and a decomposed `é`
 * name the same file as `src/secrets/k` and a composed one.
 */
const pathKey = (path: string) => path.normalize("NFC").toLowerCase();

/** `server__tool` with `*` wildcards anywhere. */
function mcpMatches(pattern: string, name: string): boolean {
  if (pattern === "" || pattern === "*") return true;
  return new RegExp(`^${pattern.split("*").map(reEsc).join(".*")}$`).test(name);
}

function ruleMatches(
  rule: PermissionRule,
  request: PermissionRequest,
): boolean {
  if (rule.tool !== request.tool) return false;
  switch (rule.tool) {
    case "edit":
      // A deny or ask rule matches every spelling of the file it names: on
      // a case-insensitive disk `src/Secrets/k` is `src/secrets/k`. An allow
      // rule matches only its own, so a case-sensitive disk's other file
      // is asked about, never allowed by it.
      return rule.effect === "allow"
        ? pathGlobMatches(rule.pattern, request.target)
        : pathGlobMatches(pathKey(rule.pattern), pathKey(request.target));
    case "run_command":
      return commandMatches(rule, request);
    case "fetch":
      return domainMatches(rule.pattern, request.target);
    case "web_search":
      // A search has no host: only the catch-all can match it.
      return rule.pattern === "" || rule.pattern === "*";
    case "mcp":
      // Another name of the call can block it, never allow it: a gateway's
      // alias may name a different tool on the same server.
      return [
        request.target,
        ...(rule.effect === "allow" ? [] : (request.also ?? [])),
      ].some((name) => mcpMatches(rule.pattern, name));
  }
}

/**
 * The applicable effect: deny beats ask beats allow (each its first matching
 * rule in file order), and no match at all is "ask" — the dialog.
 */
export function evaluate(
  rules: readonly PermissionRule[],
  request: PermissionRequest,
): PermissionVerdict {
  let ask: PermissionRule | undefined;
  let allow: PermissionRule | undefined;
  for (const rule of rules)
    if (ruleMatches(rule, request)) {
      if (rule.effect === "deny") return { effect: "deny", rule };
      if (rule.effect === "ask" && ask === undefined) ask = rule;
      if (rule.effect === "allow" && allow === undefined) allow = rule;
    }
  if (ask) return { effect: "ask", rule: ask };
  if (allow) return { effect: "allow", rule: allow };
  return { effect: "ask" };
}

// ------------------------------------------------------------------ proposing

/** Escapes glob metacharacters so a literal path matches only itself. */
function escapeGlob(path: string): string {
  return path.replace(/[\\*?[\]]/g, (c) => `\\${c}`);
}

/**
 * The rule an "always" verdict would save: exactly what was approved — a
 * literal escaped path anchored at the workspace root (bare, `src/a.ts`
 * would also match `vendor/src/a.ts`), the command's exact argv (quoted where needed; a
 * hand-written trailing `*` widens it), the fetch host, the exact MCP
 * tool, or everything for a web search.
 */
export function proposeRule(request: PermissionRequest): PermissionRule {
  switch (request.tool) {
    case "edit":
      return {
        effect: "allow",
        tool: "edit",
        pattern: `/${escapeGlob(request.target)}`,
      };
    case "run_command": {
      const argv = request.argv?.length
        ? request.argv
        : splitWords(request.target);
      // An empty argv names nothing; the empty pattern matches nothing.
      return {
        effect: "allow",
        tool: "run_command",
        pattern: argv.map(quoteWord).join(" "),
      };
    }
    case "fetch":
      return {
        effect: "allow",
        tool: "fetch",
        pattern: `domain:${request.target}`,
      };
    case "web_search":
      return { effect: "allow", tool: "web_search", pattern: "*" };
    case "mcp":
      return { effect: "allow", tool: "mcp", pattern: request.target };
  }
}

/** A representative target for a proposed pattern, so a deny rule that
 * would shadow it can be found by the plain `evaluate`. */
function patternTarget(rule: PermissionRule): string {
  if (rule.tool === "fetch" || rule.tool === "web_search")
    return rule.pattern.replace(/^domain:/, "");
  if (rule.tool === "run_command")
    return (commandPattern(rule.pattern)?.words ?? []).join(" ");
  return rule.pattern.replace(/\\/g, "");
}

/**
 * Appends a rule to the file, creating the `.local-agent/` directory and
 * file when missing, and adding `.local-agent/` to the workspace's
 * `.gitignore` when one exists without it. The new rule (only that one) is
 * trusted for this workspace. Refuses (throws) when an existing deny rule
 * already covers the new rule's requests, when the file is invalid (the
 * caller may then say so), or at 100 rules.
 */
export async function appendRule(
  workspaceRoot: string,
  rule: PermissionRule,
): Promise<{
  file: string;
  created: boolean;
  /** False when the file already held the rule (it was only trusted). */
  added: boolean;
  gitignoreAdded: boolean;
}> {
  // Under the policy lock: two clients pressing `!` at once must not drop
  // each other's rule (a lost deny rule would fail open).
  return withPolicyLock(async () => {
    const existing = await readPermissionFile(workspaceRoot);
    if (existing.warning) throw new Error(existing.warning);
    if (rule.effect === "allow") {
      const target = patternTarget(rule);
      const shadow = evaluate(existing.rules, {
        tool: rule.tool,
        target,
        ...(rule.tool === "run_command"
          ? { argv: commandPattern(rule.pattern)?.words ?? [] }
          : {}),
      });
      if (shadow.effect === "deny")
        throw new Error(
          `refused: the deny rule "${shadow.rule?.tool} ${shadow.rule?.pattern}" already covers ${rule.tool} ${rule.pattern}`,
        );
    }
    let file = permissionsPath(workspaceRoot);
    const present = existing.rules.some(
      (other) =>
        other.effect === rule.effect && ruleKey(other) === ruleKey(rule),
    );
    if (!present) {
      if (existing.rules.length >= RULES_MAX)
        throw new Error(
          `refused: permissions.json already holds ${RULES_MAX} rules`,
        );
      file = await writePermissionFile(workspaceRoot, {
        version: 1,
        rules: [...existing.rules, rule],
      });
    }
    if (rule.effect === "allow")
      await changeTrust(workspaceRoot, (keys) => keys.add(ruleKey(rule)));
    let gitignoreAdded = false;
    const gitignore = join(workspaceRoot, ".gitignore");
    try {
      // A planted symlink is not the workspace's .gitignore: skip it.
      await assertPlain(gitignore, "file");
      const text = await readFile(gitignore, "utf8");
      if (!text.split("\n").some((line) => line.trim() === ".local-agent/")) {
        const pad = text.endsWith("\n") || text === "" ? "" : "\n";
        await writeFile(gitignore, `${text}${pad}.local-agent/\n`);
        gitignoreAdded = true;
      }
    } catch {
      // No (plain) .gitignore: nothing to add the entry to.
    }
    return {
      file,
      created: existing.rules.length === 0,
      added: !present,
      gitignoreAdded,
    };
  });
}

/** `/permissions clear rules`: the file, rewritten with no rules, and its trust dropped. */
export async function clearRules(
  workspaceRoot: string,
): Promise<{ file: string }> {
  return withPolicyLock(async () => {
    const file = await writePermissionFile(workspaceRoot, NO_PERMISSIONS);
    await changeTrust(workspaceRoot, (keys) => keys.clear());
    return { file };
  });
}

/**
 * `/permissions trust`: trusts the allow rules the file holds right now,
 * for this workspace only — all of them, or only those in `only` (the
 * rules the user was shown) that are still in the file. Returns the rules
 * that were not trusted before, so the caller can name what it enabled.
 */
export async function trustRules(
  workspaceRoot: string,
  only?: readonly PermissionRule[],
): Promise<{ trusted: PermissionRule[] }> {
  return withPolicyLock(async () => {
    const file = await readPermissionFile(workspaceRoot);
    if (file.warning) throw new Error(file.warning);
    const trusted: PermissionRule[] = [];
    const shown = only && new Set(only.map(ruleKey));
    const allows = file.rules.filter(
      (rule) => rule.effect === "allow" && (!shown || shown.has(ruleKey(rule))),
    );
    if (allows.length === 0) return { trusted };
    await changeTrust(workspaceRoot, (keys) => {
      for (const rule of allows)
        if (!keys.has(ruleKey(rule))) {
          keys.add(ruleKey(rule));
          trusted.push(rule);
        }
    });
    return { trusted };
  });
}

/**
 * The runtime approval kinds as permission requests: edits and writes share
 * the "edit" family (target: the workspace-relative path), a command is its
 * joined argv (and the argv itself, so proposals never re-split), and an
 * MCP call is its `SERVER__TOOL` name, or through a gateway the tool it
 * runs, with the gateway's own name and the other spellings as `also`.
 */
export function permissionRequest(
  proposal: ApprovalRequest,
): PermissionRequest {
  switch (proposal.kind) {
    case "edit":
    case "write":
      return { tool: "edit", target: proposal.path };
    case "command":
      return {
        tool: "run_command",
        target: proposal.argv.join(" "),
        argv: proposal.argv,
      };
    case "mcp":
      return proposal.target
        ? {
            tool: "mcp",
            target: proposal.target,
            also: [proposal.name, ...(proposal.aliases ?? [])],
          }
        : { tool: "mcp", target: proposal.name };
  }
}

/**
 * A command with an argument that names a path the file tools refuse, or
 * with too many path words to check each for a link to one (see
 * ApprovedCommandProposal.secretPaths).
 */
export function namesSecretPath(proposal: ApprovalRequest): boolean {
  return proposal.kind === "command" && !!proposal.secretPaths?.length;
}

/**
 * A proposal's verdict under the workspace file's rules: the step every client
 * takes before its own answer (a card, a headless flag, the sandbox). A deny
 * refuses, an allow approves, and an ask is left to the client. A command
 * that names a secret path (see ApprovedCommandProposal) is never waved
 * through by an allow rule: it asks, so its card can say so.
 */
export function ruleVerdict(
  rules: readonly PermissionRule[],
  proposal: ApprovalRequest,
): PermissionVerdict {
  const verdict = evaluate(rules, permissionRequest(proposal));
  return verdict.effect === "allow" && namesSecretPath(proposal)
    ? { effect: "ask" }
    : verdict;
}

/**
 * The workspace file's rules as every client consults them, read fresh on
 * each call (an edit to the file applies to the next proposal): `verdict` is
 * ruleVerdict on the file as it is now, and `deniedRead` is the runtime's
 * hook for the reads that never ask. A warning (an untrusted or broken file)
 * reaches `onWarning` once per distinct text.
 */
export function workspaceRules(
  workspaceRoot: string,
  onWarning?: (warning: string) => void,
): {
  verdict(proposal: ApprovalRequest): Promise<PermissionVerdict>;
  deniedRead(request: PermissionRequest): Promise<string | undefined>;
} {
  let warned: string | undefined;
  const rules = async () => {
    const loaded = await loadPermissionFile(workspaceRoot);
    if (loaded.warning && loaded.warning !== warned) {
      warned = loaded.warning;
      onWarning?.(loaded.warning);
    }
    return loaded.rules;
  };
  return {
    verdict: async (proposal) => ruleVerdict(await rules(), proposal),
    deniedRead: async (request) => {
      const verdict = evaluate(await rules(), request);
      return verdict.effect === "deny"
        ? (verdict.rule?.pattern ?? "")
        : undefined;
    },
  };
}

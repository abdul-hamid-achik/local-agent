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
// directory, keyed by the workspace's real path: the "always" key and
// `/permissions trust` write it, nothing a repository contains can.

import { randomUUID } from "node:crypto";
import {
  mkdir,
  readFile,
  realpath,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { homedir } from "node:os";
import { basename, dirname, join } from "node:path";
import type { ApprovalRequest } from "#runtime/agent";

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
   * fetch: the URL hostname · web_search: the query · mcp: `SERVER__TOOL`.
   * Commands may also carry their argv so `proposeRule` never re-splits.
   */
  target: string;
  /** The raw argv, when the request came from a command approval. */
  argv?: readonly string[];
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
  const config = env.XDG_CONFIG_HOME?.trim()
    ? env.XDG_CONFIG_HOME
    : join(homedir(), ".config");
  return join(config, "local-agent", "trusted-rules.json");
}

type TrustStore = { version: 1; workspaces: Record<string, string[]> };

const ruleKey = (rule: PermissionRule): string =>
  JSON.stringify([rule.tool, rule.pattern]);

/** A missing or damaged store trusts nothing: every allow rule asks again. */
async function readTrust(): Promise<TrustStore> {
  try {
    const parsed = JSON.parse(await readFile(trustStorePath(), "utf8"));
    if (
      parsed?.version === 1 &&
      typeof parsed.workspaces === "object" &&
      parsed.workspaces !== null
    ) {
      const workspaces: Record<string, string[]> = {};
      for (const [root, keys] of Object.entries(parsed.workspaces))
        if (Array.isArray(keys))
          workspaces[root] = keys.filter(
            (key): key is string => typeof key === "string",
          );
      return { version: 1, workspaces };
    }
  } catch {
    // Missing or unreadable: the empty store.
  }
  return { version: 1, workspaces: {} };
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

/** The store's key for a workspace: its real path, so a moved clone starts untrusted. */
async function workspaceKey(workspaceRoot: string): Promise<string> {
  try {
    return await realpath(workspaceRoot);
  } catch {
    return workspaceRoot;
  }
}

async function updateTrust(
  workspaceRoot: string,
  change: (keys: Set<string>) => void,
): Promise<void> {
  const store = await readTrust();
  const root = await workspaceKey(workspaceRoot);
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
  const trusted = new Set(
    (await readTrust()).workspaces[await workspaceKey(workspaceRoot)] ?? [],
  );
  const untrusted = file.rules.filter(
    (rule) => rule.effect === "allow" && !trusted.has(ruleKey(rule)),
  );
  if (untrusted.length === 0) return { ...file, untrusted };
  return {
    version: 1,
    rules: file.rules.filter((rule) => !untrusted.includes(rule)),
    untrusted,
    warning: `permissions.json: ${untrusted.length} allow rule${untrusted.length === 1 ? "" : "s"} not trusted on this machine ${untrusted.length === 1 ? "is" : "are"} ignored (${untrusted
      .slice(0, 3)
      .map((rule) => `${rule.tool} ${rule.pattern}`)
      .join(
        ", ",
      )}${untrusted.length > 3 ? ", …" : ""}); review the file, then run /permissions trust`,
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
): { words: string[]; star: boolean } | undefined {
  const words: string[] = [];
  let star = false;
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
      if (word !== "*") words.push(word.slice(0, -1));
    } else words.push(word);
  }
  return { words, star };
}

/** One argv word as a pattern word: quoted when it would not survive bare. */
function quoteWord(word: string): string {
  return word === "" || word.endsWith("*") || /[\s"\\]/.test(word)
    ? `"${word.replace(/[\\"]/g, (c) => `\\${c}`)}"`
    : word;
}

/**
 * An argv pattern, exact unless it ends with a bare `*`: `npm test *`
 * matches `npm test` itself and `npm test anything`. One approval is one
 * argv vector (no shell), so words compare one to one, never re-split. A
 * deny or ask rule's program also matches by basename (`rm *` refuses
 * `/bin/rm -rf x`); an allow rule's program never does, so `git *` does
 * not approve a planted `./git`.
 */
function commandMatches(
  rule: PermissionRule,
  request: PermissionRequest,
): boolean {
  const parsed = commandPattern(rule.pattern);
  if (!parsed) return false;
  const { words: core, star } = parsed;
  const words = request.argv?.length
    ? [...request.argv]
    : splitWords(request.target);
  if (core.length === 0) return star;
  if (star ? words.length < core.length : words.length !== core.length)
    return false;
  return core.every(
    (word, i) =>
      word === words[i] ||
      (i === 0 &&
        rule.effect !== "allow" &&
        !word.includes("/") &&
        basename(words[0] ?? "") === word),
  );
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
      return pathGlobMatches(rule.pattern, request.target);
    case "run_command":
      return commandMatches(rule, request);
    case "fetch":
      return domainMatches(rule.pattern, request.target);
    case "web_search":
      // A search has no host: only the catch-all can match it.
      return rule.pattern === "" || rule.pattern === "*";
    case "mcp":
      return mcpMatches(rule.pattern, request.target);
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
 * literal escaped path, the command's exact argv (quoted where needed; a
 * hand-written trailing `*` widens it), the fetch host, the exact MCP
 * tool, or everything for a web search.
 */
export function proposeRule(request: PermissionRequest): PermissionRule {
  switch (request.tool) {
    case "edit":
      return {
        effect: "allow",
        tool: "edit",
        pattern: escapeGlob(request.target),
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
): Promise<{ file: string; created: boolean; gitignoreAdded: boolean }> {
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
  const file = permissionsPath(workspaceRoot);
  const present = existing.rules.some(
    (other) => other.effect === rule.effect && ruleKey(other) === ruleKey(rule),
  );
  if (!present) {
    if (existing.rules.length >= RULES_MAX)
      throw new Error(
        `refused: permissions.json already holds ${RULES_MAX} rules`,
      );
    const next: PermissionFile = {
      version: 1,
      rules: [...existing.rules, rule],
    };
    await mkdir(join(workspaceRoot, ".local-agent"), { recursive: true });
    await writeFile(file, `${JSON.stringify(next, null, 2)}\n`);
  }
  if (rule.effect === "allow")
    await updateTrust(workspaceRoot, (keys) => keys.add(ruleKey(rule)));
  let gitignoreAdded = false;
  const gitignore = join(workspaceRoot, ".gitignore");
  try {
    const text = await readFile(gitignore, "utf8");
    if (!text.split("\n").some((line) => line.trim() === ".local-agent/")) {
      const pad = text.endsWith("\n") || text === "" ? "" : "\n";
      await writeFile(gitignore, `${text}${pad}.local-agent/\n`);
      gitignoreAdded = true;
    }
  } catch {
    // No .gitignore: nothing to add the entry to.
  }
  return { file, created: existing.rules.length === 0, gitignoreAdded };
}

/** `/permissions clear rules`: the file, rewritten with no rules, and its trust dropped. */
export async function clearRules(
  workspaceRoot: string,
): Promise<{ file: string }> {
  const file = permissionsPath(workspaceRoot);
  await mkdir(join(workspaceRoot, ".local-agent"), { recursive: true });
  await writeFile(file, `${JSON.stringify(NO_PERMISSIONS, null, 2)}\n`);
  await updateTrust(workspaceRoot, (keys) => keys.clear());
  return { file };
}

/**
 * `/permissions trust`: trusts every allow rule the file holds right now,
 * for this workspace only. Returns the rules that were not trusted before,
 * so the caller can name what it just enabled.
 */
export async function trustRules(
  workspaceRoot: string,
): Promise<{ trusted: PermissionRule[] }> {
  const file = await readPermissionFile(workspaceRoot);
  if (file.warning) throw new Error(file.warning);
  const trusted: PermissionRule[] = [];
  const allows = file.rules.filter((rule) => rule.effect === "allow");
  if (allows.length === 0) return { trusted };
  await updateTrust(workspaceRoot, (keys) => {
    for (const rule of allows)
      if (!keys.has(ruleKey(rule))) {
        keys.add(ruleKey(rule));
        trusted.push(rule);
      }
  });
  return { trusted };
}

/**
 * The runtime approval kinds as permission requests: edits and writes share
 * the "edit" family (target: the workspace-relative path), a command is its
 * joined argv (and the argv itself, so proposals never re-split), and an
 * MCP call is its `SERVER__TOOL` name.
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
      return { tool: "mcp", target: proposal.name };
  }
}

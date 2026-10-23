// Durable permission rules: the `.local-agent/permissions.json` file a user
// (or the client's "always" key) writes so repeated approvals become policy.
// The file is loaded by the client on demand (mtime-checked) and evaluated
// before any approval dialog or headless flag; nothing here talks to the
// model, and every matcher is pure string work over the request's target.

import { mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
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

/**
 * Reads `<root>/.local-agent/permissions.json`. Missing → the empty file;
 * unreadable or schema-invalid → the empty file plus a warning string.
 * Never throws: a bad file degrades to "ask", never to a crash.
 */
export async function loadPermissionFile(
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
 * An argv-prefix pattern with a trailing `*`: `npm test *` matches
 * `npm test` itself and `npm test anything`. Without the star the match is
 * exact. One approval is one argv vector (no shell), so there is no compound
 * splitting to do: the joined target is one command.
 */
function commandMatches(pattern: string, target: string): boolean {
  const star = pattern.endsWith("*");
  const core = splitWords(star ? pattern.slice(0, -1) : pattern);
  const words = splitWords(target);
  if (core.length === 0) return true;
  if (!star)
    return words.length === core.length && core.every((w, i) => w === words[i]);
  return core.every((w, i) => w === words[i]);
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
      return commandMatches(rule.pattern, request.target);
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
 * The rule an "always" verdict would save: narrow enough to name exactly
 * what was approved — a literal escaped path, the command's first token
 * (plus a star once argv is long enough to be a stable prefix), the fetch
 * host, the exact MCP tool, or everything for a web search.
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
      if (argv.length === 0)
        return { effect: "allow", tool: "run_command", pattern: "*" };
      // One or two words name the command well; more than that is
      // call-specific, so keep the first token and let the star speak.
      const pattern =
        argv.length > 2
          ? `${argv[0] ?? ""} *`
          : argv.join(" ") || (argv[0] ?? "*");
      return { effect: "allow", tool: "run_command", pattern };
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
    return rule.pattern.replace(/\*$/, "").trim();
  return rule.pattern.replace(/\\/g, "");
}

/**
 * Appends a rule to the file, creating the `.local-agent/` directory and
 * file when missing, and adding `.local-agent/` to the workspace's
 * `.gitignore` when one exists without it. Refuses (throws) when an
 * existing deny rule already covers the new rule's requests, when the
 * file is invalid (the caller may then say so), or at 100 rules.
 */
export async function appendRule(
  workspaceRoot: string,
  rule: PermissionRule,
): Promise<{ file: string; created: boolean; gitignoreAdded: boolean }> {
  const existing = await loadPermissionFile(workspaceRoot);
  if (existing.warning) throw new Error(existing.warning);
  if (rule.effect === "allow") {
    const shadow = evaluate(existing.rules, {
      tool: rule.tool,
      target: patternTarget(rule),
    });
    if (shadow.effect === "deny")
      throw new Error(
        `refused: the deny rule "${shadow.rule?.tool} ${shadow.rule?.pattern}" already covers ${rule.tool} ${rule.pattern}`,
      );
  }
  if (existing.rules.length >= RULES_MAX)
    throw new Error(
      `refused: permissions.json already holds ${RULES_MAX} rules`,
    );
  const file = permissionsPath(workspaceRoot);
  const next: PermissionFile = { version: 1, rules: [...existing.rules, rule] };
  await mkdir(join(workspaceRoot, ".local-agent"), { recursive: true });
  await writeFile(file, `${JSON.stringify(next, null, 2)}\n`);
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

/** `/permissions clear rules`: the file, rewritten with no rules. */
export async function clearRules(
  workspaceRoot: string,
): Promise<{ file: string }> {
  const file = permissionsPath(workspaceRoot);
  await mkdir(join(workspaceRoot, ".local-agent"), { recursive: true });
  await writeFile(file, `${JSON.stringify(NO_PERMISSIONS, null, 2)}\n`);
  return { file };
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

// What a session changed in the workspace, read from its own history (`/diff`).
//
// The session's messages already hold every applied change: an assistant
// message's `edit` / `write` call carries the strings it was asked to apply,
// and the tool message after it carries the runtime's receipt ("Updated
// PATH. …", "Created PATH. …", "Replaced PATH. …"). So no file is read and
// no git is asked: the list is the session's own, and it is the same for a
// session that was resumed. Pure: no I/O.
//
// What the history cannot say, this does not claim: a `write` over an
// existing file keeps no earlier text (the call holds the new content only),
// so such a file is listed as replaced, with no diff; a command's changes
// (`run_command`) are not edits and are not listed.

import { changeLines, type Message } from "@local-agent/runtime";
import { cleanText } from "#cli/tuimark/blocks";

/** How a file was changed by its calls: `edited` and `created` can mix. */
export interface FileChange {
  /** The path as the runtime reported it (workspace-relative). */
  path: string;
  /** Applied `edit` calls, in order. */
  edits: number;
  /** A `write` created the file in this session. */
  created: boolean;
  /** A `write` replaced the file's whole content: no earlier text is kept. */
  replaced: boolean;
  /** Diff rows, in order (hunks of edits and the lines of a created file). */
  rows: ChangeRow[];
  added: number;
  removed: number;
}

export interface ChangeRow {
  kind: "add" | "del" | "same" | "gap";
  text: string;
}

export interface SessionChanges {
  files: FileChange[];
  /** Calls that may have changed a file but whose outcome is unknown. */
  unknown: number;
}

const VERIFICATION = ". Verification has not been run.";

/** The first line of an output, cut at the runtime's trailing sentence. */
function reportedPath(output: string, verb: string): string | undefined {
  const first = output.split("\n", 1)[0] ?? "";
  if (!first.startsWith(`${verb} `)) return undefined;
  const rest = first.slice(verb.length + 1);
  const end = rest.lastIndexOf(VERIFICATION);
  const path = end >= 0 ? rest.slice(0, end) : rest;
  return path || undefined;
}

function receiptOf(message: Message): { status: string; output: string } {
  try {
    const parsed = JSON.parse(message.content) as {
      status?: unknown;
      output?: unknown;
    };
    if (typeof parsed.output === "string")
      return {
        status: typeof parsed.status === "string" ? parsed.status : "unknown",
        output: parsed.output,
      };
  } catch {
    // Not the runtime's receipt: nothing applied can be read from it.
  }
  return { status: "unknown", output: message.content };
}

/**
 * The files `messages` changed through applied `edit` and `write` calls,
 * in the order each was first changed. A tool message is matched only
 * against the calls of the assistant message before it: providers reuse
 * call ids from one reply to the next.
 */
export function sessionChanges(messages: readonly Message[]): SessionChanges {
  const byPath = new Map<string, FileChange>();
  let unknown = 0;
  let calls: NonNullable<Message["tool_calls"]> = [];
  for (const message of messages) {
    if (message.role === "assistant") {
      calls = message.tool_calls ?? [];
      continue;
    }
    if (message.role !== "tool") continue;
    const name = message.tool_name;
    if (name !== "edit" && name !== "write") continue;
    const call = message.tool_call_id
      ? calls.find((candidate) => candidate.id === message.tool_call_id)
      : undefined;
    if (!call) continue;
    const { status, output } = receiptOf(message);
    if (status === "outcome_unknown") {
      unknown++;
      continue;
    }
    if (status !== "succeeded") continue;
    const args = call.function.arguments;
    const verb =
      name === "edit"
        ? "Updated"
        : output.startsWith("Created ")
          ? "Created"
          : "Replaced";
    const path = reportedPath(output, verb);
    if (path === undefined) continue;
    let change = byPath.get(path);
    if (!change) {
      change = {
        path,
        edits: 0,
        created: false,
        replaced: false,
        rows: [],
        added: 0,
        removed: 0,
      };
      byPath.set(path, change);
    }
    let before = "";
    let after: string | undefined;
    if (name === "edit") {
      if (
        typeof args.old_string !== "string" ||
        typeof args.new_string !== "string"
      )
        continue;
      change.edits++;
      [before, after] = [args.old_string, args.new_string];
    } else if (verb === "Created") {
      if (typeof args.content !== "string") continue;
      change.created = true;
      after = args.content;
    } else {
      change.replaced = true;
      continue;
    }
    if (change.rows.length > 0) change.rows.push({ kind: "gap", text: "…" });
    for (const row of changeLines(before, after)) {
      change.rows.push(row);
      if (row.kind === "add") change.added++;
      else if (row.kind === "del") change.removed++;
    }
  }
  return { files: [...byPath.values()], unknown };
}

/** The status when a session changed nothing. */
export const NO_CHANGES_STATUS = "No changes in this session";

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

/** The panel's args line, and the status: "2 files · +5 −2". */
export function changesSummary(changes: SessionChanges): string {
  const added = changes.files.reduce((total, file) => total + file.added, 0);
  const removed = changes.files.reduce(
    (total, file) => total + file.removed,
    0,
  );
  return `${plural(changes.files.length, "file")} · +${added} −${removed}`;
}

function fileHeader(file: FileChange): string {
  const how = [
    file.created ? "created" : "",
    file.replaced ? "replaced (earlier text not kept: no diff)" : "",
    file.edits > 0
      ? file.edits === 1
        ? "edited"
        : `edited ×${file.edits}`
      : "",
  ].filter(Boolean);
  const counts = [
    file.added > 0 ? `+${file.added}` : "",
    file.removed > 0 ? `−${file.removed}` : "",
  ]
    .filter(Boolean)
    .join(" ");
  return cleanText(
    `── ${file.path} · ${[...how, counts].filter(Boolean).join(" · ")}`,
  );
}

/**
 * The panel's text: one block per file (its header, then the rows as the
 * edit cards show them: `+ added`, `- removed`, two spaces for context,
 * `…` between hunks), and a closing line on what this does not cover.
 */
export function changesText(changes: SessionChanges): string {
  const out: string[] = [];
  for (const file of changes.files) {
    if (out.length > 0) out.push("");
    out.push(fileHeader(file));
    for (const row of file.rows) {
      const text = cleanText(row.text);
      out.push(
        row.kind === "add"
          ? `+ ${text}`
          : row.kind === "del"
            ? `- ${text}`
            : row.kind === "gap"
              ? "…"
              : `  ${text}`,
      );
    }
  }
  out.push("");
  out.push("Only applied edits and writes are listed, not commands.");
  if (changes.unknown > 0)
    out.push(
      `${plural(changes.unknown, "call")} with an unknown outcome not listed: /recover.`,
    );
  return out.join("\n");
}

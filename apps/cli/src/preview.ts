// Plain-text previews shared by the Vue and Tuimark clients. Nothing here may
// import a renderer: this module is loaded before any terminal is claimed.

import {
  type ApprovalRequest,
  commandLine,
  type Message,
  secretPathNotes,
  shellWord,
} from "@local-agent/runtime";
import { terminalText } from "#cli/text";

/** The directory a command runs in, as the approval says it: relative to the workspace when known. */
function commandWhere(cwd: string, root?: string): string {
  if (root === undefined) return cwd;
  if (cwd === root) return "the workspace";
  return cwd.startsWith(`${root}/`) ? cwd.slice(root.length + 1) : cwd;
}

/**
 * What an approval dialog shows. A command is one shell-quoted line, then where
 * it runs; `root` is the workspace root, which keeps absolute paths off the screen.
 */
export function approvalPreview(
  proposal: ApprovalRequest,
  root?: string,
): string {
  if (proposal.kind === "edit" || proposal.kind === "write")
    return terminalText(proposal.diff);
  if (proposal.kind === "mcp")
    return terminalText(
      `${proposal.inner && proposal.gateway ? `${proposal.inner} via ${proposal.gateway}` : proposal.name}\n${proposal.argumentsJson}\nMCP effects are not verified by transport success.`,
    );
  return terminalText(
    `$ ${commandLine(proposal.argv)}\nin ${commandWhere(proposal.cwd, root)} · timeout ${proposal.timeoutMs / 1000}s · runs with your permissions${secretNote(proposal)}`,
  );
}

/**
 * A command's arguments that name a path the file tools refuse, as a line
 * under the command, and a line when it has too many path words to check.
 */
export function secretNote(proposal: ApprovalRequest): string {
  if (proposal.kind !== "command") return "";
  const { paths, unchecked } = secretPathNotes(proposal.secretPaths);
  return `${
    paths.length
      ? `\nnames a path the workspace treats as secret: ${paths.map(shellWord).join(", ")}`
      : ""
  }${
    unchecked === undefined
      ? ""
      : `\nnames ${unchecked} path words, too many to check each for a link to a secret`
  }`;
}

/** "31 tok/s" from Ollama's counters, or undefined when it reported none. */
export function tokensPerSecond(
  evalCount: number,
  evalDurationNs: number,
): string | undefined {
  if (evalCount <= 0 || evalDurationNs <= 0) return undefined;
  const rate = evalCount / (evalDurationNs / 1e9);
  return `${rate >= 10 ? Math.round(rate) : rate.toFixed(1)} tok/s`;
}

export function messageText(message: Message): string {
  if (message.role !== "tool") return message.content;
  try {
    const result = JSON.parse(message.content) as {
      status?: unknown;
      output?: unknown;
    };
    return typeof result.output === "string"
      ? `${typeof result.status === "string" ? result.status : "unknown"}\n${result.output}`
      : message.content;
  } catch {
    return message.content;
  }
}

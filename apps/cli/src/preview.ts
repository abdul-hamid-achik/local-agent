// Plain-text previews shared by the Vue and Tuimark clients. Nothing here may
// import a renderer: this module is loaded before any terminal is claimed.

import type { ApprovalRequest, Message } from "@local-agent/runtime";
import { terminalText } from "#cli/text";

/** One argument as a POSIX shell would need it written, for display only (nothing is run through a shell). */
export function shellQuote(argument: string): string {
  if (argument === "") return "''";
  return /^[A-Za-z0-9_@%+=:,./-]+$/u.test(argument)
    ? argument
    : `'${argument.replaceAll("'", "'\\''")}'`;
}

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
      `${proposal.name}\n${proposal.argumentsJson}\nMCP effects are not verified by transport success.`,
    );
  return terminalText(
    `$ ${proposal.argv.map(shellQuote).join(" ")}\nin ${commandWhere(proposal.cwd, root)} · timeout ${proposal.timeoutMs / 1000}s · runs with your permissions`,
  );
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

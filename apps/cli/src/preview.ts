// Plain-text previews shared by the Vue and Tuimark clients. Nothing here may
// import a renderer: this module is loaded before any terminal is claimed.

import type { ApprovalRequest, Message } from "@local-agent/runtime";
import { terminalText } from "#cli/text";

export function approvalPreview(proposal: ApprovalRequest): string {
  if (proposal.kind === "edit") return terminalText(proposal.diff);
  if (proposal.kind === "mcp")
    return terminalText(
      `${proposal.name}\n${proposal.argumentsJson}\nMCP effects are not verified by transport success.`,
    );
  return terminalText(
    JSON.stringify(
      {
        argv: proposal.argv,
        cwd: proposal.cwd,
        timeoutMs: proposal.timeoutMs,
        permissions: "user",
      },
      null,
      2,
    ),
  );
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

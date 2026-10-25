// Plain-text previews shared by the Vue and Tuimark clients. Nothing here may
// import a renderer: this module is loaded before any terminal is claimed.

import { isAbsolute, relative, sep } from "node:path";
import {
  type ApprovalRequest,
  commandLine,
  type Message,
  secretPathNotes,
  shellWord,
  toolReceipt,
} from "@local-agent/runtime";
import { terminalText } from "#cli/text";

/** The directory a command runs in, as the approval says it: relative to the workspace when known. */
function commandWhere(cwd: string, root?: string): string {
  if (root === undefined) return cwd;
  const inside = relative(root, cwd);
  if (inside === "") return "the workspace";
  // Outside the root, relative() climbs out of it (or, on another drive, gives
  // an absolute path): the whole path says more than a run of "..".
  return inside === ".." || inside.startsWith(`..${sep}`) || isAbsolute(inside)
    ? cwd
    : inside;
}

/** The call as the user reads it: `SERVER__TOOL`, or `SERVER__TOOL via GATEWAY`. */
export function mcpCallName(
  proposal: Extract<ApprovalRequest, { kind: "mcp" }>,
): string {
  return proposal.inner && proposal.gateway
    ? `${proposal.inner} via ${proposal.gateway}`
    : proposal.name;
}

/** The closing line of every MCP approval: the dialogs differ in layout, not in this claim. */
export const MCP_UNVERIFIED =
  "MCP effects are not verified by transport success.";

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
      `${mcpCallName(proposal)}\n${proposal.argumentsJson}\n${MCP_UNVERIFIED}`,
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
  const receipt = toolReceipt(message.content);
  return receipt ? `${receipt.status}\n${receipt.output}` : message.content;
}

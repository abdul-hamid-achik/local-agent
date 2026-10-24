// `/export`: the visible conversation as markdown, or the raw model
// messages as JSON (`/export json`). Pure: the controller owns the file
// write; this module only turns the conversation into text.
//
// Markdown's source is the transcript's views — the same text the client
// renders (tool cards keep their summarized output, error notices are the
// client's UI-local views). The JSON format is the other export: the raw
// messages the runtime's history holds, tool JSON envelopes included.
import type { Message } from "@local-agent/runtime";

import { type Block, type MessageView, runsText } from "#cli/tuimark/blocks";

/**
 * A code fence for `text`: one backtick longer than its longest backtick run
 * (at least four), so the content cannot close it and forge what follows.
 */
function fenceFor(text: string): string {
  let longest = 3;
  for (const run of text.match(/`+/g) ?? [])
    longest = Math.max(longest, run.length);
  return "`".repeat(longest + 1);
}

/** One rendered block back as markdown text (marks were consumed by the parser). */
function blockText(block: Block): string {
  const text = runsText(block.runs);
  if (block.code) {
    const fence = fenceFor(text);
    return `${fence}${block.lang}\n${text}\n${fence}`;
  }
  if (block.head) return `## ${text}`;
  if (block.quote) return `> ${text}`;
  if (block.item) return `${block.nest ? "  " : ""}- ${text}`;
  return text;
}

function viewText(view: MessageView): string {
  return view.blocks.map(blockText).join("\n\n");
}

/**
 * What an export says instead of a message's images: their base64 stays out of
 * the file (a 2 MiB data URI per image makes the markdown unusable), so the
 * count is all a reader gets.
 */
function imagesNote(view: MessageView): string {
  const n = view.images.length + view.images_more;
  return n > 0 ? `_${n} image${n === 1 ? "" : "s"} not included_` : "";
}

/** The whole conversation the views show, as one markdown document. */
export function transcriptMarkdown(views: readonly MessageView[]): string {
  const parts = ["# Local Agent transcript", ""];
  for (const view of views) {
    const note = imagesNote(view);
    if (view.is_user) parts.push("## You", "", viewText(view), "");
    else if (view.is_asst) parts.push("## Assistant", "", viewText(view), "");
    else if (view.is_error) parts.push("## Error", "", viewText(view), "");
    else if (view.is_notice) parts.push("## Notice", "", viewText(view), "");
    else if (view.is_tool) {
      const { card } = view;
      parts.push(`## Tool · ${card.name} · ${card.status}`, "");
      if (card.args) parts.push(card.args, "");
      const output = runsText(card.out);
      if (output) {
        const fence = fenceFor(output);
        parts.push(fence, output, fence, "");
      }
    }
    if (note) parts.push(note);
  }
  return `${parts.join("\n").trimEnd()}\n`;
}

/**
 * The conversation's raw model messages (`/export json`): exactly what the
 * runtime's history holds, tool JSON envelopes included, unlike markdown.
 */
export function transcriptJson(messages: readonly Message[]): string {
  return `${JSON.stringify(messages, null, 2)}\n`;
}

/**
 * The export's file name for a moment: local-agent-transcript-YYYYMMDD-HHMMSS
 * (local time), with the format's extension (default `.md`).
 */
export function exportFileName(at: Date, ext = ".md"): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  const stamp = `${at.getFullYear()}${pad(at.getMonth() + 1)}${pad(at.getDate())}-${pad(at.getHours())}${pad(at.getMinutes())}${pad(at.getSeconds())}`;
  return `local-agent-transcript-${stamp}${ext}`;
}

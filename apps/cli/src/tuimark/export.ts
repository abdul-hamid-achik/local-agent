// `/export`: the visible conversation as markdown. Pure: the controller
// owns the file write; this module only turns transcript views into text.
//
// The source is the transcript's views — the same text the client renders
// (tool cards keep their summarized output, error notices are the
// client's UI-local views) — never the raw model messages, whose tool
// content is JSON the parser boundary keeps out of state like this.

import { type Block, type MessageView, runsText } from "#cli/tuimark/blocks";

/** One rendered block back as markdown text (marks were consumed by the parser). */
function blockText(block: Block): string {
  const text = runsText(block.runs);
  if (block.code) return `\`\`\`\`${block.lang}\n${text}\n\`\`\`\``;
  if (block.head) return `## ${text}`;
  if (block.quote) return `> ${text}`;
  if (block.item) return `${block.nest ? "  " : ""}- ${text}`;
  return text;
}

function viewText(view: MessageView): string {
  return view.blocks.map(blockText).join("\n\n");
}

/** The whole conversation the views show, as one markdown document. */
export function transcriptMarkdown(views: readonly MessageView[]): string {
  const parts = ["# Local Agent transcript", ""];
  for (const view of views) {
    if (view.is_user) parts.push("## You", "", viewText(view), "");
    else if (view.is_asst) parts.push("## Assistant", "", viewText(view), "");
    else if (view.is_notice) parts.push("## Error", "", viewText(view), "");
    else if (view.is_tool) {
      const { card } = view;
      parts.push(`## Tool · ${card.name} · ${card.status}`, "");
      if (card.args) parts.push(card.args, "");
      const output = runsText(card.out);
      if (output) parts.push("````", output, "````", "");
    }
  }
  return `${parts.join("\n").trimEnd()}\n`;
}

/** The export's file name for a moment: local-agent-transcript-YYYYMMDD-HHMMSS.md (local time). */
export function exportFileName(at: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  const stamp = `${at.getFullYear()}${pad(at.getMonth() + 1)}${pad(at.getDate())}-${pad(at.getHours())}${pad(at.getMinutes())}${pad(at.getSeconds())}`;
  return `local-agent-transcript-${stamp}.md`;
}

import type { Message } from "@local-agent/runtime";
import { expect, test } from "vitest";
import { type MessageView, ViewBuilder } from "#cli/tuimark/blocks";
import { exportFileName, transcriptMarkdown } from "#cli/tuimark/export";

function viewsOf(messages: Message[]): MessageView[] {
  const builder = new ViewBuilder();
  const views: MessageView[] = [];
  for (const message of messages) {
    const view = builder.fromMessage(message);
    if (view) views.push(view);
  }
  return views;
}

test("the export is the rendered conversation as markdown, never raw JSON", () => {
  const views = viewsOf([
    { role: "user", content: "Add a **retry** please" },
    { role: "assistant", content: "Done:\n\n```ts\nconst tries = 3;\n```" },
    {
      role: "assistant",
      content: "",
      tool_calls: [
        {
          id: "c1",
          function: { name: "read", arguments: { path: "src/big.txt" } },
        },
      ],
    },
    {
      role: "tool",
      tool_name: "read",
      tool_call_id: "c1",
      content: JSON.stringify({ status: "succeeded", output: "file body" }),
    },
  ]);
  const markdown = transcriptMarkdown(views);
  expect(markdown).toContain("# Local Agent transcript");
  expect(markdown).toContain("## You\n\nAdd a retry please");
  expect(markdown).toContain("## Assistant");
  expect(markdown).toContain("```ts\nconst tries = 3;\n```");
  expect(markdown).toContain("## Tool · read · succeeded");
  expect(markdown).toContain("file body");
  // The tool message's raw JSON envelope never reaches the file.
  expect(markdown).not.toContain('"status"');
  expect(markdown).not.toContain("tool_call_id");
});

test("an error notice exports under its own heading", () => {
  const builder = new ViewBuilder();
  const views = [
    ...viewsOf([{ role: "user", content: "go" }]),
    builder.notice("Ollama request failed: connection refused"),
  ];
  expect(transcriptMarkdown(views)).toContain(
    "## Error\n\n> Ollama request failed: connection refused",
  );
});

test("an empty conversation exports just the title", () => {
  expect(transcriptMarkdown([])).toBe("# Local Agent transcript\n");
});

test("the file name carries the local timestamp, zero-padded", () => {
  expect(exportFileName(new Date(2026, 9, 22, 9, 5, 3))).toBe(
    "local-agent-transcript-20261022-090503.md",
  );
});

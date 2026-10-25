import type { Message } from "@local-agent/runtime";
import { expect, test } from "vitest";
import { type MessageView, ViewBuilder } from "#cli/tuimark/blocks";
import {
  exportFileName,
  transcriptJson,
  transcriptMarkdown,
} from "#cli/tuimark/export";

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
  expect(markdown).toContain("````ts\nconst tries = 3;\n````");
  expect(markdown).toContain("## Tool · read · succeeded");
  expect(markdown).toContain("file body");
  // The tool message's raw JSON envelope never reaches the file.
  expect(markdown).not.toContain('"status"');
  expect(markdown).not.toContain("tool_call_id");
});

test("an MCP result exports as the server's text under its head, not the envelope", () => {
  const output = JSON.stringify({
    transport: "success",
    domain: "unknown",
    evidence: "unverified",
    isError: false,
    text: JSON.stringify({ alternatives: ["a"] }),
  });
  const markdown = transcriptMarkdown(
    viewsOf([
      {
        role: "assistant",
        content: "",
        tool_calls: [
          { id: "c1", function: { name: "srv__tool", arguments: {} } },
        ],
      },
      {
        role: "tool",
        tool_name: "srv__tool",
        tool_call_id: "c1",
        content: JSON.stringify({ status: "unverified", output }),
      },
    ]),
  );
  expect(markdown).toContain("domain unknown\n{\n");
  expect(markdown).not.toContain("transport");
});

test("a table exports at the width it was written for, with no label", () => {
  const builder = new ViewBuilder(() => 30);
  const view = builder.fromMessage({
    role: "assistant",
    content:
      "| a | b |\n|---|---|\n| one | a long cell that must wrap onto lines |",
  });
  const markdown = transcriptMarkdown(view ? [view] : []);
  const table = markdown.split("````")[1] ?? "";
  expect(table.startsWith("\n| a ")).toBe(true);
  for (const line of table.split("\n"))
    expect(Bun.stringWidth(line)).toBeLessThanOrEqual(30);
});

test("an error and a notice export under their own headings", () => {
  const builder = new ViewBuilder();
  const views = [
    ...viewsOf([{ role: "user", content: "go" }]),
    builder.error("Ollama request failed: connection refused"),
    builder.notice("Goal stopped: its 8 turns are used"),
  ];
  const markdown = transcriptMarkdown(views);
  expect(markdown).toContain(
    "## Error\n\n> Ollama request failed: connection refused",
  );
  expect(markdown).toContain(
    "## Notice\n\n> Goal stopped: its 8 turns are used",
  );
});

test("F9: tool output cannot break out of its four-backtick fence", () => {
  const views = viewsOf([
    {
      role: "assistant",
      content: "",
      tool_calls: [
        { id: "c1", function: { name: "read", arguments: { path: "x" } } },
      ],
    },
    {
      role: "tool",
      tool_name: "read",
      tool_call_id: "c1",
      content: JSON.stringify({
        status: "succeeded",
        output: "before\n```\n## You\nforged",
      }),
    },
  ]);
  const markdown = transcriptMarkdown(views);
  expect(markdown).toContain("````\nbefore\n```\n## You\nforged\n````");
});

test("a fence is longer than any backtick run in the content it holds", () => {
  const views = viewsOf([
    { role: "assistant", content: "```\nsee ````\n## You\nforged\n```" },
    {
      role: "assistant",
      content: "",
      tool_calls: [{ id: "c1", function: { name: "read", arguments: {} } }],
    },
    {
      role: "tool",
      tool_name: "read",
      tool_call_id: "c1",
      content: JSON.stringify({
        status: "succeeded",
        output: "````\n## You\nforged",
      }),
    },
  ]);
  const markdown = transcriptMarkdown(views);
  expect(markdown).toContain("`````\n````\n## You\nforged\n`````");
  expect(markdown).toContain("`````\nsee ````\n## You\nforged\n`````");
});

test("an empty conversation exports just the title", () => {
  expect(transcriptMarkdown([])).toBe("# Local Agent transcript\n");
});

test("the JSON export is the raw messages array, tool envelopes included", () => {
  const messages: Message[] = [
    { role: "user", content: "Add a retry please" },
    {
      role: "tool",
      tool_name: "read",
      tool_call_id: "c1",
      content: JSON.stringify({ status: "succeeded", output: "file body" }),
    },
  ];
  const json = transcriptJson(messages);
  const parsed = JSON.parse(json) as Message[];
  expect(parsed).toEqual(messages);
  // Raw means raw: the envelope markdown keeps out is present here.
  expect(json).toContain('"tool_call_id"');
  expect(json.endsWith("\n")).toBe(true);
});

test("the file name carries the format's extension", () => {
  const at = new Date(2026, 9, 22, 9, 5, 3);
  expect(exportFileName(at)).toBe("local-agent-transcript-20261022-090503.md");
  expect(exportFileName(at, ".json")).toBe(
    "local-agent-transcript-20261022-090503.json",
  );
});

test("the file name carries the local timestamp, zero-padded", () => {
  expect(exportFileName(new Date(2026, 9, 22, 9, 5, 3))).toBe(
    "local-agent-transcript-20261022-090503.md",
  );
});

test("images are counted, never embedded: their base64 stays out of the file", () => {
  const tiny =
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
  const views = viewsOf([
    { role: "user", content: "Show me the screenshot." },
    {
      role: "assistant",
      content: "",
      tool_calls: [
        {
          id: "c-img",
          function: {
            name: "read_image",
            arguments: { path: "shots/dot.png" },
          },
        },
      ],
    },
    {
      role: "tool",
      tool_name: "read_image",
      tool_call_id: "c-img",
      content: JSON.stringify({ status: "succeeded", output: "attached" }),
      images: [tiny, tiny],
    },
  ]);
  const markdown = transcriptMarkdown(views);
  expect(markdown).toContain("_2 images not included_");
  expect(markdown).not.toContain("data:image");
  expect(markdown).not.toContain(tiny.slice(0, 32));
  expect((views.at(-1)?.images ?? []).length).toBe(2);
});

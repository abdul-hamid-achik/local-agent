import { describe, expect, test } from "vitest";
import { renderMarkdown } from "#desktop/renderer/markdown";

describe("renderMarkdown", () => {
  test("escapes HTML before decorating", () => {
    const html = renderMarkdown('<script>alert("x")</script>');
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });

  test("renders headings, emphasis, code and lists", () => {
    const html = renderMarkdown(
      ["## Title", "", "**bold** and `code`", "", "- one", "- two"].join("\n"),
    );
    expect(html).toContain("<h2>Title</h2>");
    expect(html).toContain("<strong>bold</strong>");
    expect(html).toContain("<code>code</code>");
    expect(html).toContain("<ul>");
    expect(html).toContain("<li>one</li>");
  });

  test("renders fenced code blocks without interpreting content", () => {
    const html = renderMarkdown("```js\nconst a = '<b>';\n```");
    expect(html).toContain("<pre><code");
    expect(html).toContain("const a = '&lt;b&gt;';");
    expect(html).not.toContain("<b>");
  });
});

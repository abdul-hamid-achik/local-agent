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

  test("a language that is not a word still opens a fence, and the prose after it stays prose", () => {
    expect(renderMarkdown("```c++\nint a;\n```\nafter")).toBe(
      '<pre><code data-lang="c++">int a;</code></pre><p>after</p>',
    );
    expect(renderMarkdown('```ts title="a.ts"\nlet a;\n```')).toBe(
      '<pre><code data-lang="ts">let a;</code></pre>',
    );
  });

  test("only a bare fence of the same kind, at least as long, closes a block", () => {
    // A longer fence holds a shorter one, a fenced example included.
    expect(renderMarkdown("````md\n```js\nx\n```\n````\nafter")).toBe(
      '<pre><code data-lang="md">```js\nx\n```</code></pre><p>after</p>',
    );
    expect(renderMarkdown("~~~\n```\n~~~")).toBe("<pre><code>```</code></pre>");
    expect(renderMarkdown("```\n```py\n```")).toBe(
      "<pre><code>```py</code></pre>",
    );
  });

  test("an open fence runs to the end, as a reply still streaming does", () => {
    expect(renderMarkdown("```py\nprint(1)")).toBe(
      '<pre><code data-lang="py">print(1)</code></pre>',
    );
  });

  test("backticks in what follows a backtick fence make it a line of text", () => {
    expect(renderMarkdown("```a`b\nnext")).toBe(
      "<p>``<code>a</code>b</p><p>next</p>",
    );
  });
});

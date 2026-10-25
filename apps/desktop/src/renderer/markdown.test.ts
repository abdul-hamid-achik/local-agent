import { describe, expect, test } from "vitest";
import { renderMarkdown } from "#desktop/renderer/markdown";

/** The HTML with the renderer's own tags taken out: what is left is text. */
function onlyOwnTags(html: string): string {
  return html.replaceAll(
    /<\/?(?:p|em|strong|code|pre|ul|li|h[1-4])(?: data-lang="[^"<>]*")?>/g,
    "",
  );
}

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

  test("an indented fence opens, and its lines lose as much indent as it had", () => {
    expect(renderMarkdown("  ```js\n    x\n y\n  ```")).toBe(
      '<pre><code data-lang="js">  x\ny</code></pre>',
    );
    // Spaces only: a tab is the line's own.
    expect(renderMarkdown("  ```\n\tx\n  ```")).toBe(
      "<pre><code>\tx</code></pre>",
    );
  });

  test("a fence after a list item ends the list, then the block", () => {
    expect(renderMarkdown("- one\n```\ncode\n```\n- two")).toBe(
      "<ul><li>one</li></ul><pre><code>code</code></pre><ul><li>two</li></ul>",
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
  test("two emphasis marks one space apart both apply, and snake_case does not", () => {
    expect(renderMarkdown("use _foo_ _bar_")).toBe(
      "<p>use <em>foo</em> <em>bar</em></p>",
    );
    expect(renderMarkdown("_a_ _b_ _c_")).toBe(
      "<p><em>a</em> <em>b</em> <em>c</em></p>",
    );
    expect(renderMarkdown("a_b_c and (_x_).")).toBe(
      "<p>a_b_c and (<em>x</em>).</p>",
    );
  });

  // Every place text lands in the HTML: a payload must stay text in each.
  const payload = '<img src=x onerror="alert(1)">';
  test.each([
    ["a heading", `# ${payload}`],
    ["a list item", `- ${payload}`],
    ["a paragraph", payload],
    ["emphasis", `_${payload}_`],
    ["bold", `**${payload}**`],
    ["inline code", `\`${payload}\``],
    ["a fenced block", `\`\`\`\n${payload}\n\`\`\``],
    ["an unclosed fence, as a reply still streaming has", `\`\`\`\n${payload}`],
  ])("%s: a tag in it stays text", (_where, source) => {
    const html = renderMarkdown(source);
    expect(html).not.toMatch(/<img/);
    expect(onlyOwnTags(html)).not.toMatch(/[<>]/);
    expect(html).toContain("&lt;img src=x onerror=&quot;alert(1)&quot;&gt;");
  });

  test("a fence's language cannot break out of its attribute", () => {
    const html = renderMarkdown('```x"onmouseover="alert(1)\ncode\n```');
    expect(html).toBe(
      '<pre><code data-lang="x&quot;onmouseover=&quot;alert(1)">code</code></pre>',
    );
    expect(
      renderMarkdown('```"><script>alert(1)</script>\nx\n```'),
    ).not.toContain("<script");
  });

  test("links are text: nothing here produces an anchor or an attribute", () => {
    for (const source of [
      "[click](javascript:alert(1))",
      "<a href='javascript:alert(1)'>x</a>",
      "![x](https://example.test/x.png)",
      "<https://example.test>",
    ]) {
      const html = renderMarkdown(source);
      expect(html, source).not.toMatch(/<(a|img)\b/);
      expect(onlyOwnTags(html), source).not.toMatch(/[<>]/);
    }
  });
});

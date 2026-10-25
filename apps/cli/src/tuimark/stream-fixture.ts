// The token stream the stream tests and `stream-bytes.ts` feed in. It lives
// apart from the measuring script, which pulls in the whole controller.

/**
 * A reply of about `tokens` tokens, as the pieces a model would stream. "mixed"
 * is paragraphs, a list and short code blocks; "fence" is one code block that
 * runs the whole way (a model writing a file).
 */
export function fixtureTokens(
  tokens: number,
  shape: "mixed" | "fence" = "mixed",
): string[] {
  if (shape === "fence") {
    let code = "Here is the file:\n\n```ts\n";
    for (let n = 0; code.length < tokens * 4; n++)
      code += `export function step${n}(input: number): number {\n  return input * ${n} + 1; // line ${n}\n}\n\n`;
    const pieces: string[] = [];
    for (let i = 0; i < code.length && pieces.length < tokens; i += 4)
      pieces.push(code.slice(i, i + 4));
    return pieces;
  }
  const paragraph =
    "The helper wraps the call in a **bounded retry** with exponential backoff, and `AbortError` is never retried because the caller asked for it. ";
  const code = [
    "```ts",
    "export async function fetchRetry(url: string, tries = 3) {",
    "  for (let attempt = 1; ; attempt++) {",
    "    try { return await fetch(url); }",
    "    catch (e) { if (attempt === tries) throw e; }",
    "  }",
    "}",
    "```",
    "",
  ].join("\n");
  const list =
    "- the last error is rethrown\n- the delay doubles\n- stream complete\n\n";
  let text = "";
  for (let n = 0; text.length < tokens * 4; n++)
    text += `${paragraph}${paragraph}\n\n${n % 3 === 1 ? code : ""}${n % 3 === 2 ? list : ""}`;
  const pieces: string[] = [];
  for (let i = 0; i < text.length && pieces.length < tokens; i += 4)
    pieces.push(text.slice(i, i + 4));
  return pieces;
}

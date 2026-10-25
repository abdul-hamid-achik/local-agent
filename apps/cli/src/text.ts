/**
 * Safe plain terminal output for the Tuimark client and the previews. Same rule
 * as `@local-agent/vue-tui/text`, kept here so the Tuimark path does not depend
 * on a package that goes away with the Vue client. Renderer-free.
 */
export function terminalText(text: string): string {
  return Bun.stripANSI(text).replace(
    // biome-ignore lint/suspicious/noControlCharactersInRegex: remove terminal control characters from untrusted text
    /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/g,
    "",
  );
}

/**
 * terminalText for a field that is one line, such as a tool name the model
 * chose: a line break or a tab in it would start a line of its own, so each
 * run of whitespace is one space.
 */
export function terminalLine(text: string): string {
  return terminalText(text).replace(/\s+/g, " ").trim();
}

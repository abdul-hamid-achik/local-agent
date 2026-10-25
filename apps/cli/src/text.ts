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
 * An escape sequence that has begun and not ended: a bare ESC, a CSI without
 * its final byte, an OSC/DCS-style string without its terminator, or
 * intermediate bytes still waiting for one.
 */
// biome-ignore lint/suspicious/noControlCharactersInRegex: match the ESC and BEL of a terminal sequence
const OPEN_ESCAPE = /^\u001b(?:\[[0-?]*[ -/]*|[\]PX^_][^\u0007]*|[ -/]+)?$/;

/** What an open sequence may grow to before it is taken for text and cleaned as it is. */
const OPEN_ESCAPE_MAX = 1024;

/**
 * terminalText for text that arrives in pieces (a model's reply as it
 * streams): a sequence cut between two pieces is held back until the next one
 * completes it. Cleaning each piece alone removes the ESC of "ESC [31m" cut
 * after the ESC and leaves "[31m" on the screen. The text is the same as the
 * whole of it through terminalText; a sequence still open when the pieces end
 * is dropped, as terminalText drops it.
 */
export function terminalTextStream(): (piece: string) => string {
  let held = "";
  return (piece) => {
    let text = held + piece;
    held = "";
    const start = text.lastIndexOf("\u001b");
    if (
      start >= 0 &&
      text.length - start <= OPEN_ESCAPE_MAX &&
      OPEN_ESCAPE.test(text.slice(start))
    ) {
      held = text.slice(start);
      text = text.slice(0, start);
    }
    return terminalText(text);
  };
}

/**
 * terminalText for a field that is one line, such as a tool name the model
 * chose: a line break or a tab in it would start a line of its own, so each
 * run of whitespace is one space.
 */
export function terminalLine(text: string): string {
  return terminalText(text).replace(/\s+/g, " ").trim();
}

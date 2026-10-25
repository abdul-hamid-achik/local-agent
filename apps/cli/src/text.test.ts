import { expect, test } from "vitest";
import { terminalText, terminalTextStream } from "#cli/text";

/** What the screen gets when the pieces are cleaned as they arrive. */
function streamed(pieces: string[]): string {
  const clean = terminalTextStream();
  return pieces.map(clean).join("");
}

test("cleaning each piece alone leaves the rest of a split sequence on the screen", () => {
  // The reason terminalTextStream exists: the ESC is removed with the part
  // that is there, and the part that comes next is plain text to terminalText.
  expect(terminalText("a\u001b") + terminalText("[31mred")).toBe("a[31mred");
  expect(terminalText("a\u001b[3") + terminalText("1mred")).toBe("a1mred");
});

const SAMPLES = [
  "plain text, then \u001b[31mred\u001b[0m and done",
  "title \u001b]0;pwned\u0007 after a BEL-ended OSC",
  "title \u001b]8;;https://example.test\u001b\\link\u001b]8;;\u001b\\ after ST",
  "charset \u001b(B and a clear \u001b[2J after",
  "bell \u0007 and a lone ESC \u001bz tail",
  "back to back \u001b[1m\u001b[4m\u001b[38;5;196mx\u001b[0m",
];

test.each(SAMPLES)(
  "a sequence cut anywhere reads as if it were whole: %j",
  (sample) => {
    const whole = terminalText(sample);
    for (let at = 0; at <= sample.length; at++)
      expect(
        streamed([sample.slice(0, at), sample.slice(at)]),
        `cut at ${at}`,
      ).toBe(whole);
    // One character at a time cuts every sequence at every place at once.
    expect(streamed([...sample])).toBe(whole);
  },
);

test("text with no escape is passed on at once", () => {
  const clean = terminalTextStream();
  expect(clean("hel")).toBe("hel");
  expect(clean("lo")).toBe("lo");
});

test("a sequence that never ends is dropped, as terminalText drops it", () => {
  expect(streamed(["tail \u001b[31", ""])).toBe("tail ");
  expect(terminalText("tail \u001b[31")).toBe("tail ");
});

test("a long open sequence is not held for ever", () => {
  const clean = terminalTextStream();
  const long = `x${"y".repeat(5000)}`;
  clean("a \u001b]0;");
  expect(clean(long)).not.toContain("]0;");
  expect(clean("later")).toBe("later");
});

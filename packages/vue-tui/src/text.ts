/** Safe plain terminal output. This entry point never loads the native renderer. */
export function terminalText(text: string): string {
  return Bun.stripANSI(text).replace(
    // biome-ignore lint/suspicious/noControlCharactersInRegex: remove terminal control characters from untrusted text
    /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/g,
    "",
  );
}

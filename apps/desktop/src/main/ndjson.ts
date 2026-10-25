/**
 * Splits a byte stream into lines. The decoder is streaming, so a multi-byte
 * UTF-8 character split across two chunks is kept intact.
 */
export class LineDecoder {
  private readonly decoder = new TextDecoder("utf-8");
  // The line in progress, in pieces: joined once its newline comes, so a
  // long line costs its length, not its length times its chunks.
  private parts: string[] = [];
  private size = 0;

  /** Characters of the line not yet ended by a newline. */
  get pending(): number {
    return this.size;
  }

  push(chunk: Uint8Array): string[] {
    const text = this.decoder.decode(chunk, { stream: true });
    if (!text.includes("\n")) {
      if (text) {
        this.parts.push(text);
        this.size += text.length;
      }
      return [];
    }
    this.parts.push(text);
    const lines = this.parts.join("").split("\n");
    const rest = lines.pop() ?? "";
    this.parts = rest ? [rest] : [];
    this.size = rest.length;
    return lines;
  }
}

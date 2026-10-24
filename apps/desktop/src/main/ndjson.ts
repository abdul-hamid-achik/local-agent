/**
 * Splits a byte stream into lines. The decoder is streaming, so a multi-byte
 * UTF-8 character split across two chunks is kept intact.
 */
export class LineDecoder {
  private readonly decoder = new TextDecoder("utf-8");
  private buffer = "";

  push(chunk: Uint8Array): string[] {
    this.buffer += this.decoder.decode(chunk, { stream: true });
    const lines = this.buffer.split("\n");
    this.buffer = lines.pop() ?? "";
    return lines;
  }
}

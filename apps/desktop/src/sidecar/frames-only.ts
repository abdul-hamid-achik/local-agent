/**
 * The sidecar's stdout carries frames only: the desktop treats any other
 * line as a broken protocol and stops the sidecar. The console is replaced
 * by one that writes everything (log, count, time, group, table, Bun's
 * write, …) to stderr, which the desktop shows as the sidecar's own.
 * Imported first, so a module that logs while it loads is covered too.
 */
import { Console } from "node:console";

const toStderr = new Console({
  stdout: process.stderr,
  stderr: process.stderr,
});

globalThis.console = Object.assign(toStderr, {
  write(...data: (string | ArrayBufferView | ArrayBuffer)[]): number {
    let bytes = 0;
    for (const chunk of data) {
      const buffer =
        typeof chunk === "string"
          ? Buffer.from(chunk)
          : ArrayBuffer.isView(chunk)
            ? Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength)
            : Buffer.from(chunk);
      process.stderr.write(buffer);
      bytes += buffer.byteLength;
    }
    return bytes;
  },
});

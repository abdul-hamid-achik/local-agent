/**
 * The bounds a client's reference view lists. The code that enforces each one
 * reads it from here, so a view cannot quote a bound the runtime no longer
 * has. Narrower bounds (one argument's length, a search's per-file cap, the
 * saved session's size) stay beside the code they bound.
 */
export const LIMITS = Object.freeze({
  /** A workspace file read or written, in bytes. */
  fileBytes: 8 * 1024 * 1024,
  /** An image read_image attaches, in bytes. */
  imageBytes: 5 * 1024 * 1024,
  /** The body fetch reads, in bytes. */
  fetchBytes: 512 * 1024,
  /** A command's arguments, and their bytes together. */
  commandArgs: 256,
  commandArgBytes: 128 * 1024,
  /** A command's output the runtime keeps, in bytes. */
  commandOutputBytes: 1 << 20,
  /** An MCP config file, in bytes, and the servers it may name. */
  mcpConfigBytes: 1_000_000,
  mcpServers: 16,
  /** The tools one MCP server may list. */
  mcpToolsPerServer: 128,
  /** The text of one MCP result the model sees, in UTF-16 code units; past
   * it the text is cut and ends in an ellipsis. */
  mcpTextChars: 16_384,
  /** One model response: the JSON of its stream parts in characters, and its
   * tool calls. One call is bounded in characters too, as the provider's
   * stream carries it: the whole call's JSON from Ollama, its name and
   * argument text from an OpenAI-compatible server. */
  responseChars: 1_000_000,
  toolCallsPerResponse: 32,
  toolCallChars: 100_000,
  /** How long a stream may stay silent when the provider sets no timeout. */
  streamIdleMs: 300_000,
});

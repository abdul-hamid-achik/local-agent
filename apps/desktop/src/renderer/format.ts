/** A byte count for people: B, KB or MB with one decimal, GB with two. */
export function formatBytes(bytes: number): string {
  const size = Math.max(0, bytes);
  if (size < 1024) return `${size} B`;
  if (size < 1024 ** 2) return `${(size / 1024).toFixed(1)} KB`;
  if (size < 1024 ** 3) return `${(size / 1024 ** 2).toFixed(1)} MB`;
  return `${(size / 1024 ** 3).toFixed(2)} GB`;
}

/**
 * The first `limit` characters of a text, for a view that would stall laying
 * out all of a large file. It never cuts a surrogate pair in two.
 */
export function previewText(
  text: string,
  limit: number,
): { shown: string; clipped: boolean } {
  if (text.length <= limit) return { shown: text, clipped: false };
  let end = limit;
  const last = text.charCodeAt(end - 1);
  if (last >= 0xd800 && last <= 0xdbff) end -= 1;
  return { shown: text.slice(0, end), clipped: true };
}

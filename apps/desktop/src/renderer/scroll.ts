/**
 * Whether a scroller shows its end (within `slack` pixels of it). A view
 * that follows new content scrolls down only for a reader who is at the
 * end: one who scrolled up to read is not pulled back on every event.
 */
export function atEnd(
  scroller: Pick<HTMLElement, "scrollTop" | "clientHeight" | "scrollHeight">,
  slack = 32,
): boolean {
  return (
    scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight <= slack
  );
}

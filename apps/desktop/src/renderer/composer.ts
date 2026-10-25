/**
 * Enter sends and Shift+Enter breaks the line, except while an input method
 * is composing (Japanese, Chinese, Korean…): that Enter picks the candidate.
 * Some browsers report the composing Enter only as keyCode 229.
 */
export function isSendKey(
  event: Pick<KeyboardEvent, "key" | "shiftKey" | "isComposing" | "keyCode">,
): boolean {
  return (
    event.key === "Enter" &&
    !event.shiftKey &&
    !event.isComposing &&
    event.keyCode !== 229
  );
}

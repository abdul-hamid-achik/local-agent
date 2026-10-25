import { errorMessage } from "#desktop/renderer/api";

/**
 * What the app's global error handler does with an error Vue caught in a
 * component (a render, a lifecycle hook, a handler): it is logged with where
 * it happened, and its reason is returned for the page to show, so a view
 * that stopped drawing is not just a blank one.
 */
export function reportUiError(
  error: unknown,
  info: string,
  log: (message: string, error: unknown) => void = console.error,
): string {
  log(`[renderer] ${info}`, error);
  return `The interface hit an error: ${errorMessage(error)}`;
}

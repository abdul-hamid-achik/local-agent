/**
 * Wraps the resolver of an approval so that an abort of `signal` refuses it
 * (false), and so that the abort listener goes once either has settled it.
 * Left in place, each approval of a long run would add a listener to the
 * run's signal for the rest of its life.
 */
export function decidable(
  signal: AbortSignal,
  resolve: (allowed: boolean) => void,
): (allowed: boolean) => void {
  const onAbort = () => settle(false);
  const settle = (allowed: boolean) => {
    signal.removeEventListener("abort", onAbort);
    resolve(allowed);
  };
  // An abort that came first would never fire the listener.
  if (signal.aborted) settle(false);
  else signal.addEventListener("abort", onAbort, { once: true });
  return settle;
}

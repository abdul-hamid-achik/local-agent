/**
 * Holds the app's quit until `stop` has settled, so nothing the sidecar
 * started outlives the app. `handle` is the before-quit listener: the first
 * quit calls `stop` and is held (unless there is nothing to stop), a quit
 * while it waits is held too, and once `stop` settles `quit` runs again and
 * goes through. `quitting` says a quit is under way, so nothing new starts.
 */
export function holdQuit(
  stop: () => Promise<void> | undefined,
  quit: () => void,
): {
  handle: (event: { preventDefault: () => void }) => void;
  readonly quitting: boolean;
} {
  let stopping: Promise<void> | undefined;
  let stopped = false;
  return {
    handle(event) {
      if (stopped) return;
      if (!stopping) {
        const pending = stop();
        if (!pending) return;
        // Settled either way: a stop that failed still lets the app quit.
        const done = () => {
          stopped = true;
          quit();
        };
        stopping = pending.then(done, done);
      }
      event.preventDefault();
    },
    get quitting() {
      return stopping !== undefined;
    },
  };
}

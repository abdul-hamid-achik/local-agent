/**
 * "Latest request wins" for async work that fills view state. Each `begin()`
 * supersedes every earlier one, and the returned check says whether this
 * request is still the newest: a slow answer that is no longer current must
 * not overwrite a newer one. `invalidate()` supersedes the requests in flight
 * without starting one, for when what they were asked about has changed.
 */
export type Latest = {
  begin: () => () => boolean;
  invalidate: () => void;
};

export function latest(): Latest {
  let newest = 0;
  return {
    begin: () => {
      const mine = ++newest;
      return () => mine === newest;
    },
    invalidate: () => {
      newest += 1;
    },
  };
}

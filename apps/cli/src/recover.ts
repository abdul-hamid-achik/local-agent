// `local-agent --recover ID`: the terminal twin of `/recover` (ADR-0006).
// Alone it lists the session's calls whose outcome is unknown. With
// `--resolve EXECUTION=applied|not-applied|undone` it shows what would be
// recorded, and records it only with `--apply`. Nothing runs again.

import {
  type Disposition,
  RECONCILE_NOTE_MAX,
  reconcileExecution,
  recoveryItems,
  type SessionStore,
} from "@local-agent/runtime";

const OUTCOMES: Record<string, Disposition> = {
  applied: "effect_applied",
  "not-applied": "effect_not_applied",
  undone: "effect_compensated",
};

/** Prints one JSON document on `print`; returns the exit code. */
export async function recoverCommand(
  store: SessionStore,
  root: string,
  id: string,
  options: { resolve?: string; note?: string; apply?: boolean },
  print: (line: string) => void = (line) => console.log(line),
): Promise<number> {
  const session = await store.loadSession(id, root);
  const items = recoveryItems(store, session);
  if (options.resolve === undefined) {
    print(
      JSON.stringify({
        session: session.publicId,
        items,
        ...(items.length
          ? {
              next: `--recover ${session.publicId} --resolve EXECUTION=applied|not-applied|undone [--note TEXT], then the same with --apply`,
            }
          : {}),
      }),
    );
    return 0;
  }
  const match = options.resolve.match(/^([^=\s]+)=(\S+)$/);
  const outcome = match?.[2]?.toLowerCase() ?? "";
  const disposition = Object.hasOwn(OUTCOMES, outcome)
    ? OUTCOMES[outcome]
    : undefined;
  if (!match || !disposition)
    throw new Error(
      "--resolve takes EXECUTION=applied, EXECUTION=not-applied or EXECUTION=undone",
    );
  const executionId = match[1] ?? "";
  const note = (options.note ?? "").replace(/\s+/g, " ").trim();
  if (note.length > RECONCILE_NOTE_MAX)
    throw new Error(`A note is at most ${RECONCILE_NOTE_MAX} characters`);
  const item = items.find((candidate) => candidate.executionId === executionId);
  if (!item)
    throw new Error(
      `No execution ${executionId} awaits recovery in this session; --recover ${session.publicId} lists them`,
    );
  if (!options.apply) {
    print(
      JSON.stringify({
        dryRun: true,
        session: session.publicId,
        item,
        disposition,
        note,
        apply: "add --apply to record it",
      }),
    );
    return 0;
  }
  const { state, closed } = await reconcileExecution(store, id, root, {
    executionId,
    disposition,
    note,
  });
  print(
    JSON.stringify({
      recorded: true,
      session: state.publicId,
      executionId,
      disposition,
      closed,
      revision: state.revision,
      left: recoveryItems(store, state).length,
    }),
  );
  return 0;
}

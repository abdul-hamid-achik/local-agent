import type { SessionStore } from "#runtime/session";

/** Review tokens deliberately exclude conversation, arguments and raw receipts. */
export async function inspectRecovery(
  store: SessionStore,
  publicId: string,
  workspace: string,
) {
  const initial = await store.loadSession(publicId, workspace);
  const release = await store.acquireLease(initial.id, workspace);
  try {
    const state = await store.loadSession(publicId, workspace);
    const hazards = store.listHazards(state.id, state.workspaceId);
    if (hazards.length > 100)
      throw new Error(
        "Recovery inspection exceeds 100 hazards; manual investigation is required",
      );
    return {
      session: state.publicId,
      workspace: state.workspaceId,
      revision: state.revision,
      hazards: hazards.map((event) => ({
        eventId: event.id,
        executionId: event.execution_id,
        turnId: event.turn_id,
        toolName: event.tool_name,
        eventType: event.event_type,
        effectClass: event.effect_class,
        argumentsSha256: event.arguments_sha256,
      })),
    };
  } finally {
    await release();
  }
}

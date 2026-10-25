import { describe, expect, test } from "vitest";
import {
  type ChatApi,
  ChatRun,
  clearConversation,
} from "#desktop/renderer/chat-run";
import type { AgentStreamEvent } from "#desktop/shared/protocol";

/**
 * A sidecar as the handlers in sidecar.ts answer: decide and cancel name a
 * run (an empty id is refused), decide applies only the token the run waits
 * on, and a run the sidecar does not know is not cancelled.
 */
function sidecar() {
  const listeners = new Map<string, (event: AgentStreamEvent) => void>();
  const runs = new Set<string>();
  const waiting = new Map<string, string>();
  const decisions: { token: string; allowed: boolean }[] = [];
  let next = 0;
  let failStart: Error | undefined;
  const emit = (runId: string, event: AgentStreamEvent) =>
    listeners.get(runId)?.(event);
  const api: ChatApi = {
    start: async (params) => {
      if (failStart) throw failStart;
      runs.add(params.runId);
      return { runId: params.runId, sessionId: "s-1" };
    },
    decide: async (runId, token, allowed) => {
      if (!runId) throw new Error("runId is required");
      if (waiting.get(runId) !== token) return { decided: false };
      waiting.delete(runId);
      decisions.push({ token, allowed });
      return { decided: true };
    },
    cancel: async (runId) => {
      if (!runs.has(runId)) return { cancelled: false };
      waiting.delete(runId);
      emit(runId, { type: "run_error", message: "Run cancelled" });
      emit(runId, { type: "run_done" });
      runs.delete(runId);
      return { cancelled: true };
    },
    subscribe: (runId, callback) => {
      listeners.set(runId, callback);
      return () => listeners.delete(runId);
    },
    newRunId: () => `run-${++next}`,
  };
  return {
    api,
    emit,
    runs,
    waiting,
    decisions,
    listeners,
    /** A card the run is now waiting on. */
    ask: (runId: string, token: string) => {
      waiting.set(runId, token);
      emit(runId, {
        type: "approval_requested",
        token,
        proposal: { kind: "command", argv: ["ls"], timeout_ms: 1000, cwd: "/" },
      });
    },
    /** A new sidecar process: it knows none of the old one's runs. */
    restart: () => {
      runs.clear();
      waiting.clear();
    },
    failNextStart: (error: Error) => {
      failStart = error;
    },
  };
}

const params = { workspace: "/w", intent: "build" as const, model: "m" };

describe("ChatRun", () => {
  test("a run streams, finishes, and keeps its session", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    expect(await chat.send("hi", params)).toBe("s-1");
    expect(chat.state.running).toBe(true);
    side.emit("run-1", { type: "text", text: "hel" });
    side.emit("run-1", { type: "text", text: "lo" });
    expect(chat.state.liveText).toBe("hello");
    side.emit("run-1", {
      type: "message",
      message: { role: "assistant", content: "hello" },
    });
    side.emit("run-1", { type: "run_done" });
    expect(chat.state.running).toBe(false);
    expect(chat.state.liveText).toBe("");
    expect(chat.state.messages.map((m) => m.content)).toEqual(["hi", "hello"]);
    expect(chat.state.sessionId).toBe("s-1");
    // The run's stream is closed: a late frame changes nothing.
    side.emit("run-1", { type: "text", text: "late" });
    expect(chat.state.liveText).toBe("");
  });

  test("Clear starts a new conversation: the next send does not resume the old session", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    const store = { resumeSession: "s-0" };
    await chat.send("hi", { ...params, resume: store.resumeSession });
    // A running turn keeps its transcript and its session.
    clearConversation(chat, store);
    expect(chat.state.messages).toHaveLength(1);
    expect(store.resumeSession).toBe("s-0");
    side.emit("run-1", { type: "run_done" });
    clearConversation(chat, store);
    expect(chat.state.messages).toEqual([]);
    expect(chat.state.sessionId).toBe("");
    expect(store.resumeSession).toBe("");
  });

  test("Stop on a run the sidecar no longer knows finishes it here", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    await chat.send("long task", params);
    // Providers saved: main replaced the sidecar under the run.
    side.restart();
    await chat.stop();
    expect(chat.state.running).toBe(false);
    expect(chat.state.notice).toBe("The run had already ended.");
    // The next turn can be sent.
    await chat.send("again", params);
    expect(chat.state.running).toBe(true);
  });

  test("Stop on a live run lets the sidecar end it", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    await chat.send("task", params);
    await chat.stop();
    expect(chat.state.running).toBe(false);
    expect(chat.state.error).toBe("Run cancelled");
    expect(chat.state.notice).toBe("");
  });

  test("a replaced or dead sidecar ends the run it was running", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    await chat.send("task", params);
    side.ask("run-1", "t1");
    chat.sidecarGone("The sidecar restarted, which ended the run.");
    expect(chat.state.running).toBe(false);
    expect(chat.state.error).toBe(
      "The sidecar restarted, which ended the run.",
    );
    expect(chat.state.approvals[0]?.status).toBe("ended");
    // Idle, the notice is not repeated over a later error.
    chat.state.error = "";
    chat.sidecarGone("again");
    expect(chat.state.error).toBe("");
  });

  test("a card the run no longer waits on is settled as ended, never as approved", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    await chat.send("edit it", params);
    side.ask("run-1", "t1");
    // The sidecar resolved it already (a cancel resolves the wait as a denial).
    side.waiting.delete("run-1");
    const card = chat.state.approvals[0];
    if (!card) throw new Error("no card");
    await chat.decide(card, true);
    expect(card.status).toBe("ended");
    expect(side.decisions).toEqual([]);

    side.ask("run-1", "t2");
    const second = chat.state.approvals[1];
    if (!second) throw new Error("no card");
    await chat.decide(second, true);
    expect(second.status).toBe("approved");
    expect(side.decisions).toEqual([{ token: "t2", allowed: true }]);
  });

  test("the end of a run settles its open cards, and a new turn starts without them", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    await chat.send("edit it", params);
    side.ask("run-1", "t1");
    side.emit("run-1", { type: "run_done" });
    const card = chat.state.approvals[0];
    if (!card) throw new Error("no card");
    expect(card.status).toBe("ended");
    // No request with an empty run id, and no error to show.
    await chat.decide(card, true);
    expect(card.status).toBe("ended");
    expect(chat.state.error).toBe("");
    await chat.send("next", params);
    expect(chat.state.approvals).toEqual([]);
  });

  test("a failed start shows why and leaves the chat ready", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    side.failNextStart(new Error("Sidecar is not running"));
    expect(await chat.send("hi", params)).toBeUndefined();
    expect(chat.state.running).toBe(false);
    expect(chat.state.error).toBe("Sidecar is not running");
    expect(side.listeners.size).toBe(0);
  });

  test("a stopped run's partial text goes when the next turn starts", async () => {
    const side = sidecar();
    const chat = new ChatRun(side.api);
    await chat.send("one", params);
    side.emit("run-1", { type: "text", text: "half an ans" });
    await chat.stop();
    expect(chat.state.liveText).toBe("half an ans");
    await chat.send("two", params);
    side.emit("run-2", { type: "text", text: "fresh" });
    expect(chat.state.liveText).toBe("fresh");
  });
});

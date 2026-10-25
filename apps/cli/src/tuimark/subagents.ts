// The live card of `task` calls (read-only subagents).
//
// The runtime drains a subagent's own events (agent.ts, the `task` branch),
// so while one runs the client hears only tool_requested, tool_start and,
// at the end, tool_result and the tool message. There is no step count to
// show before the end; what a card can honestly say while it waits is that
// it is a subagent, what it was asked, and how long it has run.
//
// Several `task` calls in one reply run together (the runtime starts a
// read-only batch at once and sends the messages in call order). The
// controller has one live-tool slot, so without this the last request would
// stand for all of them. A batch is shown as one "Subagents" card that lists
// the ones still to be committed; a lone call keeps its own card.

import type { ToolCall } from "@local-agent/runtime";
import {
  callSummary,
  clip,
  type LiveView,
  liveToolView,
  type Run,
  type TaskSlot,
} from "#cli/tuimark/blocks";
import { formatElapsed } from "#cli/tuimark/store";

interface Member {
  call: ToolCall | undefined;
  slot: TaskSlot | undefined;
  startedAt?: number;
  /** Set by tool_result: the subagent is done, its message is still to come. */
  endedAt?: number;
  failed?: boolean;
}

/** The objective a row names: its first line, short enough for a card row. */
const ROW_CHARS = 56;

/** Whole seconds once a second has passed ("" before: "<0.1s" would only flicker). */
function elapsed(ms: number): string {
  return ms >= 1000 ? formatElapsed(ms / 1000) : "";
}

export class SubagentBatch {
  private readonly members = new Map<string, Member>();

  /** A `task` call was requested; its place in the reply comes from the builder. */
  request(id: string, call: ToolCall | undefined, slot?: TaskSlot): void {
    this.members.set(id, { call, slot });
  }

  start(id: string, at: number): void {
    const member = this.members.get(id);
    if (member && member.startedAt === undefined) member.startedAt = at;
  }

  /** The runtime's tool_result: the call ended (its message follows in call order). */
  finish(id: string, at: number, status: string): void {
    const member = this.members.get(id);
    if (!member) return;
    member.endedAt = at;
    member.failed = status !== "succeeded" && status !== "unverified";
  }

  /** The call's message was committed: its own finished card is in the transcript. */
  commit(id: string): void {
    this.members.delete(id);
  }

  clear(): void {
    this.members.clear();
  }

  /** There is a subagent whose card is still to come. */
  get live(): boolean {
    return this.members.size > 0;
  }

  /**
   * The live card for the subagents not yet committed, or undefined when
   * there are none. One subagent keeps its own card ("Subagent" and its
   * objective, the time it has run); a batch is one "Subagents" card with a
   * row for each.
   */
  view(now: number, still = false): LiveView | undefined {
    const live = [...this.members.values()];
    const first = live[0];
    if (!first) return undefined;
    const started = live.filter((m) => m.startedAt !== undefined);
    const since = Math.min(...started.map((m) => m.startedAt as number));
    const took = started.length > 0 ? elapsed(now - since) : "";
    const state = started.length > 0 ? "running" : "requested";
    if (!first.slot) {
      const view = liveToolView(first.call, "task", state, still);
      return { ...view, card: { ...view.card, took } };
    }
    const total = first.slot.total;
    const base = liveToolView(first.call, "task", state, still);
    const out: Run[] = [];
    live.forEach((m, i) => {
      const label = m.slot ? `${m.slot.n}/${m.slot.total}` : "";
      const done = m.endedAt !== undefined;
      const mark = !done ? "…" : m.failed ? "✗" : "✓";
      const time =
        m.startedAt === undefined
          ? ""
          : elapsed((m.endedAt ?? now) - m.startedAt);
      const objective = clip(callSummary(m.call), ROW_CHARS);
      const text = `${label} ${mark} ${objective}${time ? ` · ${time}` : ""}`;
      if (i > 0) out.push("\n");
      out.push(done ? text : { text, class: "note" });
    });
    const finished = live.filter((m) => m.endedAt !== undefined).length;
    return {
      ...base,
      card: {
        ...base.card,
        title: "Subagents",
        took,
        args: `${total} subagents · ${total - live.length + finished} done`,
        args_path: false,
        args_line: true,
        out,
      },
    };
  }
}

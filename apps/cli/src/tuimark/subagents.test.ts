import type { Message } from "@local-agent/runtime";
import { expect, test } from "vitest";
import {
  liveToolView,
  resultSummary,
  runsText,
  toolTitle,
  ViewBuilder,
} from "#cli/tuimark/blocks";
import { SubagentBatch } from "#cli/tuimark/subagents";

const taskCall = (id: string, prompt: string, extra = {}) => ({
  id,
  function: { name: "task", arguments: { prompt, ...extra } },
});
const taskOutput = (turns: number, answer: string, note = "") =>
  `subagent finished after ${turns} turn${turns === 1 ? "" : "s"}${note ? ` (${note})` : ""}\n\n${answer}`;
const taskMessage = (id: string, output: string, status = "succeeded") =>
  ({
    role: "tool",
    tool_name: "task",
    tool_call_id: id,
    content: JSON.stringify({ status, output }),
  }) satisfies Message;

test("a running subagent card names the subagent, its objective and its budget", () => {
  const live = liveToolView(
    taskCall("t1", "find where the retry is configured\nthen report", {
      max_turns: 5,
    }),
    "task",
    "running",
  );
  expect(live.card).toMatchObject({
    title: "Subagent",
    status: "running",
    running: true,
    args: "find where the retry is configured",
    args_line: true,
  });
  expect(runsText(live.card.out)).toBe("read-only · up to 5 turns");
  // The runtime's default, when the model leaves the budget out.
  expect(
    runsText(
      liveToolView(taskCall("t1", "x", { max_turns: null }), "task", "running")
        .card.out,
    ),
  ).toBe("read-only · up to 8 turns");
});

test("a finished subagent card is a summary: turns, the answer's first line, a pointer", () => {
  const builder = new ViewBuilder();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [taskCall("t1", "why is the build slow?")],
  });
  const answer = [
    "## The build is slow because of the type check",
    "",
    "- tsc runs twice",
    "- the cache is cold",
  ].join("\n");
  const message = taskMessage("t1", taskOutput(3, answer));
  const view = builder.fromMessage(message);
  expect(view?.card).toMatchObject({
    title: "Subagent",
    status: "succeeded",
    mark: "✓",
    args: "why is the build slow?",
  });
  expect(runsText(view?.card.out ?? [])).toBe(
    [
      "3 turns",
      "The build is slow because of the type check",
      "… 2 more lines · /details 1 for the whole answer",
    ].join("\n"),
  );
  // /details has the whole answer, the objective and how it ended.
  expect(builder.receiptOf(message)).toEqual({
    n: 1,
    name: "task",
    status: "succeeded",
    args: "why is the build slow?",
    output: [
      "3 turns",
      "",
      "Objective",
      "why is the build slow?",
      "",
      "Answer",
      answer,
    ].join("\n"),
  });
});

test("a one-line answer needs no pointer; a long first line is clipped and says so", () => {
  const builder = new ViewBuilder();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [taskCall("t1", "a"), taskCall("t2", "b")],
  });
  const short = builder.fromMessage(
    taskMessage("t1", taskOutput(1, "It is in src/retry.ts.")),
  );
  expect(runsText(short?.card.out ?? [])).toBe(
    "1 turn\nIt is in src/retry.ts.",
  );
  const long = builder.fromMessage(
    taskMessage("t2", taskOutput(2, "word ".repeat(100))),
  );
  const out = runsText(long?.card.out ?? []).split("\n");
  expect(out[1]?.endsWith("…")).toBe(true);
  expect(out[2]).toBe("… line cut · /details 2 for the whole answer");
});

test("a subagent that stopped early says why in its head", () => {
  const builder = new ViewBuilder();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [taskCall("t1", "a")],
  });
  const view = builder.fromMessage(
    taskMessage(
      "t1",
      taskOutput(8, "partial notes", "stopped at the turn limit"),
    ),
  );
  expect(runsText(view?.card.out ?? []).split("\n")[0]).toBe(
    "8 turns · stopped at the turn limit",
  );
});

test("several task calls in one reply are numbered; a lone one is not", () => {
  const builder = new ViewBuilder();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [
      taskCall("t1", "first"),
      { id: "r1", function: { name: "read", arguments: { path: "a" } } },
      taskCall("t2", "second"),
      taskCall("t3", "third"),
    ],
  });
  const titles = ["t1", "t2", "t3"].map(
    (id) =>
      builder.fromMessage(taskMessage(id, taskOutput(1, "ok")))?.card.title,
  );
  expect(titles).toEqual(["Subagent 1/3", "Subagent 2/3", "Subagent 3/3"]);
  // The read in between is not a subagent and is not numbered.
  expect(
    builder.fromMessage({
      role: "tool",
      tool_name: "read",
      tool_call_id: "r1",
      content: JSON.stringify({ status: "succeeded", output: "x" }),
    })?.card.title,
  ).toBe("Read");
  // The next reply starts over, though its ids repeat the earlier ones.
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [taskCall("t1", "again")],
  });
  expect(
    builder.fromMessage(taskMessage("t1", taskOutput(1, "ok")))?.card.title,
  ).toBe("Subagent");
  // A live card takes its slot as the finished one does.
  const call = taskCall("t9", "live");
  expect(
    liveToolView(call, "task", "running", false, { n: 2, total: 3 }).card.title,
  ).toBe("Subagent 2/3");
  expect(toolTitle("task", call, { n: 1, total: 2 })).toBe("Subagent 1/2");
  expect(toolTitle("read", call, { n: 1, total: 2 })).toBe("Read");
});

test("a task that failed or answered with something else shows its text as it is", () => {
  const builder = new ViewBuilder();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [taskCall("t1", "a")],
  });
  const failed = builder.fromMessage(
    taskMessage("t1", "Tool failed: provider unreachable", "failed"),
  );
  expect(failed?.card).toMatchObject({ bad: true, mark: "✗" });
  expect(runsText(failed?.card.out ?? [])).toBe(
    "Tool failed: provider unreachable",
  );
  expect(resultSummary("task", "plain text", "succeeded")).toBe("plain text");
  expect(resultSummary("task", taskOutput(2, "the answer"), "succeeded")).toBe(
    "2 turns\nthe answer",
  );
});

test("a running subagent shows its elapsed time from the first second", () => {
  const batch = new SubagentBatch();
  const call = taskCall("t1", "look around");
  batch.request("t1", call);
  expect(batch.view(0)?.card).toMatchObject({
    title: "Subagent",
    status: "requested",
    took: "",
  });
  batch.start("t1", 1_000);
  expect(batch.view(1_400)?.card).toMatchObject({
    status: "running",
    took: "",
  });
  expect(batch.view(13_000)?.card).toMatchObject({
    title: "Subagent",
    status: "running",
    took: "12s",
    args: "look around",
  });
  batch.commit("t1");
  expect(batch.live).toBe(false);
  expect(batch.view(14_000)).toBeUndefined();
});

test("a batch of subagents is one group card listing the ones still to be committed", () => {
  const builder = new ViewBuilder();
  const calls = [
    taskCall("t1", "read the router"),
    taskCall("t2", "read the store"),
    taskCall("t3", "read the tests"),
  ];
  builder.fromMessage({ role: "assistant", content: "", tool_calls: calls });
  const batch = new SubagentBatch();
  for (const call of calls) {
    batch.request(call.id, call, builder.slotOf(call));
    batch.start(call.id, 0);
  }
  batch.finish("t2", 4_000, "succeeded");
  const group = batch.view(6_000)?.card;
  expect(group).toMatchObject({
    title: "Subagents",
    status: "running",
    running: true,
    took: "6s",
    args: "3 subagents · 1 done",
  });
  expect(runsText(group?.out ?? [])).toBe(
    [
      "1/3 … read the router · 6s",
      "2/3 ✓ read the store · 4s",
      "3/3 … read the tests · 6s",
    ].join("\n"),
  );
  // Messages arrive in call order: the first one's card is committed, the
  // others stay in the group.
  batch.commit("t1");
  batch.commit("t2");
  const rest = batch.view(7_000)?.card;
  expect(rest?.args).toBe("3 subagents · 2 done");
  expect(runsText(rest?.out ?? [])).toBe("3/3 … read the tests · 7s");
  batch.commit("t3");
  expect(batch.view(8_000)).toBeUndefined();
});

test("under reduced motion a running subagent card, alone or a group, is still", () => {
  for (const total of [1, 3]) {
    const batch = new SubagentBatch();
    for (let n = 1; n <= total; n++) {
      const id = `t${n}`;
      batch.request(
        id,
        taskCall(id, `question ${n}`),
        total > 1 ? { n, total } : undefined,
      );
      batch.start(id, 0);
    }
    expect(batch.view(5_000)?.card, `${total} moving`).toMatchObject({
      spin: true,
      still: false,
    });
    expect(batch.view(5_000, true)?.card, `${total} still`).toMatchObject({
      spin: false,
      still: true,
    });
  }
});

test("a reply that runs its task calls one at a time counts only those done", () => {
  // [task, edit, task] runs serially: the second task is requested only after
  // the first one's message is committed.
  const batch = new SubagentBatch();
  batch.request("t1", taskCall("t1", "first"), { n: 1, total: 2 });
  batch.start("t1", 0);
  expect(batch.view(2_000)?.card.args).toBe("2 subagents · 0 done");
  batch.finish("t1", 3_000, "succeeded");
  expect(batch.view(3_000)?.card.args).toBe("2 subagents · 1 done");
  batch.commit("t1");
  batch.request("t2", taskCall("t2", "second"), { n: 2, total: 2 });
  batch.start("t2", 4_000);
  expect(batch.view(5_000)?.card.args).toBe("2 subagents · 1 done");
  batch.finish("t2", 6_000, "succeeded");
  expect(batch.view(6_000)?.card.args).toBe("2 subagents · 2 done");
});

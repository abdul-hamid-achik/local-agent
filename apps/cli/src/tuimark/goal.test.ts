import { expect, test } from "vitest";
import {
  commandLine,
  effectful,
  GOAL_OUTPUT_TOKENS,
  GOAL_TIME_MS,
  GOAL_TURN_MESSAGES,
  GoalRun,
  type GoalTurn,
  goalNotice,
  newGoalTurn,
  parseGoal,
} from "#cli/tuimark/goal";

test("/goal parses an objective, its turns, a stop and a look", () => {
  expect(parseGoal("")).toEqual({ kind: "show" });
  expect(parseGoal("  STOP ")).toEqual({ kind: "stop" });
  expect(parseGoal("make the tests pass")).toEqual({
    kind: "start",
    objective: "make the tests pass",
    turns: 8,
  });
  expect(parseGoal("fix  the\tbuild --turns 3")).toEqual({
    kind: "start",
    objective: "fix the build",
    turns: 3,
  });
  expect(parseGoal("--turns=12 port the parser")).toMatchObject({
    objective: "port the parser",
    turns: 12,
  });
  // "stop" inside an objective is an objective.
  expect(parseGoal("stop the flaky test")).toMatchObject({ kind: "start" });
  for (const bad of [
    "x --turns 0",
    "x --turns 51",
    "x --turns 2.5",
    "x --turns",
    "x --turns=",
    "x --turns 1e1",
    "x --turns 0x5",
    "x --turns +3",
    "x --turns 3 --turns 4",
    "--turns=3x port it",
  ])
    expect(parseGoal(bad)).toEqual({
      kind: "error",
      status: "--turns takes one whole number from 1 to 50",
    });
  // A word that only starts like the flag is part of the objective.
  expect(parseGoal("drop the --turnstile hack")).toMatchObject({
    objective: "drop the --turnstile hack",
    turns: 8,
  });
  expect(parseGoal("--turns 3")).toMatchObject({ kind: "error" });
  expect(parseGoal("x".repeat(2_001))).toMatchObject({ kind: "error" });
});

test("the first message names the goal; the next ones its last status, and a missed report once", () => {
  const goal = new GoalRun("make the tests pass", 8, 0);
  expect(goal.nextMessage()).toBe("[goal 1/8] Goal: make the tests pass");
  expect(goal.label).toBe("goal 1/8");
  const context = { now: 0, historyLeft: 5_000, build: true };
  // A turn with no report: the next message says so, once.
  expect(goal.afterTurn({ ...newGoalTurn(), effects: 1 }, context)).toBe(
    undefined,
  );
  const second = goal.nextMessage();
  expect(second).toBe(
    "[goal 2/8] Continue toward the goal: make the tests pass. Last status: none reported.\nYou ended the last turn without goal_status: call it before you end this one.",
  );
  goal.afterTurn({ ...newGoalTurn(), effects: 1 }, context);
  expect(goal.nextMessage()).not.toContain("without goal_status");
  goal.afterTurn(
    {
      ...newGoalTurn(),
      effects: 1,
      report: { state: "continue", summary: "parser done", evidence: [] },
    },
    context,
  );
  expect(goal.nextMessage()).toBe(
    "[goal 4/8] Continue toward the goal: make the tests pass. Last status: parser done.",
  );
  // Its first line is the notice a transcript shows for it.
  expect(goalNotice(second)).toBe(
    "[goal 2/8] Continue toward the goal: make the tests pass. Last status: none reported.",
  );
  expect(goalNotice("make the tests pass")).toBeUndefined();
  expect(goalNotice("see [goal 1/8] later")).toBeUndefined();
  // Only the host's own wording is a notice, not any text that starts
  // with the bracket.
  expect(goalNotice("[goal 1/8] why does this fail")).toBeUndefined();
  // A report written over several lines stays on the next message's line.
  const multi = new GoalRun("ship it", 8, 0);
  multi.nextMessage();
  multi.afterTurn(
    {
      ...newGoalTurn(),
      effects: 1,
      report: {
        state: "continue",
        summary: "parser done.\nNext: lexer",
        evidence: [],
      },
    },
    context,
  );
  expect(multi.nextMessage()).toBe(
    "[goal 2/8] Continue toward the goal: ship it. Last status: parser done. Next: lexer.",
  );
  expect(multi.describe(0)).toContain(
    "Last status: continue: parser done. Next: lexer\n",
  );
});

/** A goal at its first turn, and a turn record with `extra`. */
function first(turns = 8, startedAt = 0) {
  const goal = new GoalRun("ship it", turns, startedAt);
  goal.nextMessage();
  return goal;
}
const turn = (extra: Partial<GoalTurn> = {}): GoalTurn => ({
  ...newGoalTurn(),
  effects: 1,
  ...extra,
});
const at = { now: 0, historyLeft: 5_000, build: true };

test("each stop rule ends the goal in its own words", () => {
  const report = (state: "continue" | "done" | "blocked", summary = "s") => ({
    state,
    summary,
    evidence: [],
  });
  const cases: [GoalTurn, string][] = [
    [turn({ end: "aborted" }), "stopped: you cancelled its turn"],
    [turn({ declined: true }), "stopped: you skipped its question"],
    [
      turn({
        uncertain: true,
        end: "failed",
        error: "Tool outcome is uncertain",
      }),
      "stopped: an action's outcome is uncertain; review the workspace before going on",
    ],
    [
      turn({ end: "failed", error: "provider down" }),
      "stopped: its turn failed (provider down)",
    ],
    [turn({ report: report("done") }), "reported done without evidence"],
    [
      turn({ report: report("blocked", "need the API key") }),
      "blocked: need the API key",
    ],
  ];
  for (const [record, reason] of cases)
    expect(first().afterTurn(record, at)).toEqual({ reason });
  // The step limit is a turn boundary, not a failure.
  expect(first().afterTurn(turn({ end: "step_limit" }), at)).toBeUndefined();
  // A cancelled turn stops it even when the model said done.
  expect(
    first().afterTurn(turn({ end: "aborted", report: report("done") }), at),
  ).toEqual({ reason: "stopped: you cancelled its turn" });
});

test("two turns in a row that change nothing stop it; one that does resets the count", () => {
  const goal = first();
  expect(goal.afterTurn(turn({ effects: 0 }), at)).toBeUndefined();
  goal.nextMessage();
  expect(goal.afterTurn(turn({ effects: 2 }), at)).toBeUndefined();
  goal.nextMessage();
  expect(goal.afterTurn(turn({ effects: 0 }), at)).toBeUndefined();
  goal.nextMessage();
  expect(goal.afterTurn(turn({ effects: 0 }), at)).toEqual({
    reason:
      "stopped: two turns in a row changed nothing (no edit, write, command or MCP call succeeded)",
  });
});

test("the budgets stop it between turns: turns, time, output tokens, room in the history, and the mode", () => {
  expect(first(1).afterTurn(turn(), at)).toEqual({
    reason: "stopped: its 1 turn is used",
  });
  const two = first(2);
  expect(two.afterTurn(turn(), at)).toBeUndefined();
  two.nextMessage();
  expect(two.afterTurn(turn(), at)).toEqual({
    reason: "stopped: its 2 turns are used",
  });
  expect(
    first(8, 1_000).afterTurn(turn(), { ...at, now: 1_000 + GOAL_TIME_MS }),
  ).toEqual({ reason: "stopped: its 30 minutes are up" });
  expect(
    first(8, 1_000).afterTurn(turn(), {
      ...at,
      now: 1_000 + GOAL_TIME_MS - 1,
    }),
  ).toBeUndefined();
  expect(
    first().afterTurn(turn({ outputTokens: GOAL_OUTPUT_TOKENS }), at),
  ).toEqual({ reason: "stopped: its 200k output tokens are spent" });
  expect(
    first().afterTurn(turn({ outputTokens: GOAL_OUTPUT_TOKENS - 1 }), at),
  ).toBeUndefined();
  expect(
    first().afterTurn(turn(), { ...at, historyLeft: GOAL_TURN_MESSAGES - 1 }),
  ).toEqual({
    reason:
      "stopped: the conversation is nearly full; /sessions new starts over",
  });
  expect(
    first().afterTurn(turn(), { ...at, historyLeft: GOAL_TURN_MESSAGES }),
  ).toBeUndefined();
  expect(first().afterTurn(turn(), { ...at, build: false })).toEqual({
    reason: "stopped: the mode is no longer build",
  });
});

test("a provider that reports no usage leaves the token budget unmeasured", () => {
  const goal = first();
  goal.afterTurn(turn({ streamed: true, outputTokens: 0 }), at);
  expect(goal.measured).toBe(false);
  goal.nextMessage();
  // Past the budget, but unmeasured: the turn and time budgets still apply.
  expect(
    goal.afterTurn(turn({ outputTokens: GOAL_OUTPUT_TOKENS * 2 }), at),
  ).toBeUndefined();
  expect(goal.describe(0)).toContain("output tokens unmeasured");
  // A turn that streamed nothing at all says nothing about usage.
  const quiet = first();
  quiet.afterTurn(turn({ streamed: false, outputTokens: 0 }), at);
  expect(quiet.measured).toBe(true);
});

test("done is 'with evidence' only for a command that succeeded during the goal, matched as run", () => {
  const goal = first();
  goal.afterTurn(turn({ commands: ["bun test", "bun run lint"] }), at);
  goal.nextMessage();
  const done = (ref: string, kind: "command" | "file" | "note" = "command") =>
    goal.afterTurn(
      turn({
        report: { state: "done", summary: "green", evidence: [{ kind, ref }] },
      }),
      at,
    );
  expect(done("bun  test")).toEqual({
    reason: "reported done — evidence: `bun test` exited 0",
  });
  // Not run, or named as something other than a command: no evidence.
  expect(done("bun test --watch")).toEqual({
    reason: "reported done without evidence",
  });
  expect(done("bun test", "note")).toEqual({
    reason: "reported done without evidence",
  });
  // A command of this very turn counts too.
  const now = first();
  expect(
    now.afterTurn(
      turn({
        commands: ["make check"],
        report: {
          state: "done",
          summary: "ok",
          evidence: [{ kind: "command", ref: "make check" }],
        },
      }),
      at,
    ),
  ).toEqual({ reason: "reported done — evidence: `make check` exited 0" });
});

test("argv becomes one command line; progress is an effect or an MCP call", () => {
  expect(commandLine(["bun", "test", "a b"])).toBe("bun test a b");
  expect(commandLine(" bun   test ")).toBe("bun test");
  expect(commandLine([1, 2])).toBeUndefined();
  expect(commandLine(undefined)).toBeUndefined();
  expect(commandLine("   ")).toBeUndefined();
  const mcp = new Set(["cortex__remember"]);
  for (const name of ["edit", "write", "run_command", "cortex__remember"])
    expect(effectful(name, mcp)).toBe(true);
  for (const name of [
    "read",
    "search",
    "todo_write",
    "task",
    "ask_user",
    "goal_status",
  ])
    expect(effectful(name, mcp)).toBe(false);
});

test("/goal shows the objective, the turn, what is left and the last status", () => {
  const goal = first(5, 0);
  goal.afterTurn(
    turn({
      outputTokens: 12_400,
      report: { state: "continue", summary: "half way", evidence: [] },
    }),
    at,
  );
  expect(goal.describe(10 * 60_000)).toBe(
    "Goal: ship it\nTurn 1 of 5 · 20 min left · 12k of 200k output tokens\nLast status: continue: half way\n/goal stop ends it",
  );
  // Past its time (it stops at the next turn's end), never negative.
  expect(goal.describe(GOAL_TIME_MS + 5 * 60_000)).toContain(
    " · 0 min left · ",
  );
});

test("a turn the user typed during the goal neither asks for a report nor counts as idle", () => {
  const goal = first();
  expect(goal.afterTurn(turn({ effects: 0 }), at)).toBeUndefined();
  // A typed question that changes nothing: still one idle turn, not two.
  expect(goal.afterTurn(turn({ effects: 0, own: false }), at)).toBeUndefined();
  expect(goal.nextMessage()).toBe(
    "[goal 2/8] Continue toward the goal: ship it. Last status: none reported.\nYou ended the last turn without goal_status: call it before you end this one.",
  );
  // ...but its stops are the goal's: a cancel ends it.
  expect(goal.afterTurn(turn({ own: false, end: "aborted" }), at)).toEqual({
    reason: "stopped: you cancelled its turn",
  });
});

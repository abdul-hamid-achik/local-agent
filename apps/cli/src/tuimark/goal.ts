// The bounded foreground goal loop (ADR 0005 in the vault): `/goal
// OBJECTIVE` runs turns until the model reports done or blocked, or a stop
// rule ends it. The loop adds no authority: every turn is an ordinary turn
// with the session's approvals, rules and sandbox. Nothing here is
// persisted; a goal ends with the process. This module is the pure part
// (parsing, the stop rules, the messages); the controller runs the turns.

import { commandLine, type GoalReport } from "@local-agent/runtime";

/** Turns a goal runs at most unless `--turns` says otherwise... */
export const GOAL_TURNS = 8;
/** ...and never more than this. */
export const GOAL_TURNS_MAX = 50;
/** Wall time a goal may take, checked between turns. */
export const GOAL_TIME_MS = 30 * 60_000;
/** Output tokens a goal may spend, when the provider reports them. */
export const GOAL_OUTPUT_TOKENS = 200_000;
/**
 * A turn at its worst: 20 steps of one assistant message and 32 results
 * (both transports refuse a response with more than 32 calls).
 */
export const GOAL_TURN_MESSAGES = 20 * 33;
/** An objective longer than this is refused, not cut. */
export const GOAL_OBJECTIVE_MAX = 2_000;

/**
 * The marker of a host-written goal message, as nextMessage writes it; its
 * view is a notice. A saved session has only the text to go by.
 */
const MARKER = /^\[goal \d+\/\d+\] (?:Goal|Continue toward the goal): /;

/** One line: what a model wrote, whitespace collapsed. */
const oneLine = (text: string) => text.replace(/\s+/g, " ").trim();

/** What `/goal …` asks for. */
export type GoalCommand =
  | { kind: "show" }
  | { kind: "stop" }
  | { kind: "start"; objective: string; turns: number }
  | { kind: "error"; status: string };

export const GOAL_USAGE =
  "Use /goal OBJECTIVE [--turns N], /goal to see it, /goal stop to end it";

/** Parses what follows `/goal`. */
export function parseGoal(args: string): GoalCommand {
  const text = args.trim();
  if (!text) return { kind: "show" };
  if (/^stop$/i.test(text)) return { kind: "stop" };
  const badTurns: GoalCommand = {
    kind: "error",
    status: `--turns takes one whole number from 1 to ${GOAL_TURNS_MAX}`,
  };
  let turns = GOAL_TURNS;
  const flag = /(?:^|\s)--turns(?:=|\s+)(\d+)(?=\s|$)/.exec(text);
  let objective = text;
  if (flag) {
    turns = Number(flag[1]);
    if (turns < 1 || turns > GOAL_TURNS_MAX) return badTurns;
    objective = `${text.slice(0, flag.index)} ${text.slice(flag.index + flag[0].length)}`;
  }
  // What is left of a flag is a mistake, never part of the objective:
  // `--turns`, `--turns=`, `--turns 2.5`, `--turns 1e1`, a second one.
  if (/(?:^|\s)--turns(?:=|\s|$)/.test(objective)) return badTurns;
  objective = oneLine(objective);
  if (!objective) return { kind: "error", status: GOAL_USAGE };
  if (objective.length > GOAL_OBJECTIVE_MAX)
    return {
      kind: "error",
      status: `The objective is ${objective.length} characters; at most ${GOAL_OBJECTIVE_MAX}`,
    };
  return { kind: "start", objective, turns };
}

/** What one turn of a goal did, as the client saw its events. */
export type GoalTurn = {
  /** The last goal_status report of the turn. */
  report?: GoalReport;
  /** Effects that succeeded: an edit, write, command or MCP call. */
  effects: number;
  /** A result whose outcome is unknown. */
  uncertain: boolean;
  /** A question the user skipped (or answered with nothing). */
  declined: boolean;
  /** Output tokens the turn's responses reported. */
  outputTokens: number;
  /** Whether text or thinking arrived (a turn of no usage then is unmeasured). */
  streamed: boolean;
  /** run_command calls that succeeded, as they were run. */
  commands: string[];
  /**
   * One of the goal's own turns (host-written), not one the user typed
   * during it: only its own say whether the model reported, or count
   * toward a run of turns that changed nothing.
   */
  own: boolean;
  /** How the turn ended. */
  end: "finished" | "step_limit" | "aborted" | "failed";
  /** A failed turn's error. */
  error?: string;
};

export function newGoalTurn(): GoalTurn {
  return {
    effects: 0,
    uncertain: false,
    declined: false,
    outputTokens: 0,
    streamed: false,
    commands: [],
    own: true,
    end: "finished",
  };
}

/** Tools whose success is progress (MCP tools count too; see `effectful`). */
const EFFECT_TOOLS = new Set(["edit", "write", "run_command"]);

/** Whether a succeeded call of `name` changed something. */
export function effectful(
  name: string,
  mcpTools: ReadonlySet<string>,
): boolean {
  return EFFECT_TOOLS.has(name) || mcpTools.has(name);
}

/**
 * A command as a key to match it by: argv joined by spaces, whitespace
 * collapsed. Not for display (see the runtime's commandLine).
 */
export function commandKey(argv: unknown): string | undefined {
  const line = Array.isArray(argv)
    ? argv.every((part) => typeof part === "string")
      ? argv.join(" ")
      : undefined
    : typeof argv === "string"
      ? argv
      : undefined;
  return line?.replace(/\s+/g, " ").trim() || undefined;
}

/** Why a goal ended, in the words its notice uses. */
export type GoalStop = { reason: string };

/** One running goal. Lives in the controller; never persisted. */
export class GoalRun {
  /** Turns started, the first included. */
  turnsUsed = 0;
  outputTokens = 0;
  /** False once a turn streamed with no usage reported: tokens are unmeasured. */
  measured = true;
  /** The last report, from any turn. */
  last?: GoalReport;
  /** Turns in a row that changed nothing. */
  idle = 0;
  /** Commands that succeeded during the goal, as one line each. */
  readonly commands = new Set<string>();
  /**
   * Calls refused during the goal: one refused in a turn is refused in
   * the later ones without a card.
   */
  readonly refused = new Set<string>();
  /** Whether the last turn ended without a report, and the note was sent. */
  private unreported = false;
  private noted = false;

  constructor(
    readonly objective: string,
    readonly turnsMax: number,
    readonly startedAt: number,
  ) {}

  /** The header's short form: the turn running (or last run) of the most. */
  get label(): string {
    return `goal ${this.turnsUsed}/${this.turnsMax}`;
  }

  /** The user message of the goal's next turn, host-written and marked. */
  nextMessage(): string {
    this.turnsUsed++;
    const head = `[goal ${this.turnsUsed}/${this.turnsMax}]`;
    if (this.turnsUsed === 1) return `${head} Goal: ${this.objective}`;
    const note =
      this.unreported && !this.noted
        ? "\nYou ended the last turn without goal_status: call it before you end this one."
        : "";
    if (note) this.noted = true;
    const last = this.last ? oneLine(this.last.summary) : "none reported";
    return `${head} Continue toward the goal: ${this.objective}. Last status: ${last}.${note}`;
  }

  /**
   * Takes a finished turn's record; returns why the goal stops, or
   * undefined when the next turn should follow. A turn the user typed
   * during the goal counts the same, though it is not one of the goal's
   * turns (turnsUsed does not move for it): every turn while a goal runs
   * is told of the goal, so its report, its cancel and its failure are the
   * goal's. Only the idle count and the missed-report note are for the
   * goal's own turns.
   */
  afterTurn(
    turn: GoalTurn,
    context: {
      now: number;
      /** Messages the conversation can still take before it is full. */
      historyLeft: number;
      build: boolean;
    },
  ): GoalStop | undefined {
    this.outputTokens += turn.outputTokens;
    if (turn.streamed && turn.outputTokens === 0) this.measured = false;
    for (const command of turn.commands) this.commands.add(command);
    if (turn.report) this.last = turn.report;
    if (turn.own) this.unreported = turn.report === undefined;
    if (turn.end === "aborted")
      return { reason: "stopped: you cancelled its turn" };
    if (turn.declined) return { reason: "stopped: you skipped its question" };
    // Before the failure it causes: the runtime fails a turn after an
    // uncertain result, and the uncertainty is what the user must know.
    if (turn.uncertain)
      return {
        reason:
          "stopped: an action's outcome is uncertain; review the workspace before going on",
      };
    if (turn.end === "failed")
      return {
        reason: `stopped: its turn failed (${turn.error ?? "unknown error"})`,
      };
    if (turn.report?.state === "done")
      return { reason: this.doneLine(turn.report) };
    if (turn.report?.state === "blocked")
      return { reason: `blocked: ${oneLine(turn.report.summary)}` };
    if (turn.effects > 0) this.idle = 0;
    else if (turn.own) this.idle++;
    if (this.idle >= 2)
      return {
        reason:
          "stopped: two turns in a row changed nothing (no edit, write, command or MCP call succeeded)",
      };
    if (this.turnsUsed >= this.turnsMax)
      return {
        reason:
          this.turnsMax === 1
            ? "stopped: its 1 turn is used"
            : `stopped: its ${this.turnsMax} turns are used`,
      };
    if (context.now - this.startedAt >= GOAL_TIME_MS)
      return {
        reason: `stopped: its ${GOAL_TIME_MS / 60_000} minutes are up`,
      };
    if (this.measured && this.outputTokens >= GOAL_OUTPUT_TOKENS)
      return {
        reason: `stopped: its ${GOAL_OUTPUT_TOKENS / 1000}k output tokens are spent`,
      };
    if (context.historyLeft < GOAL_TURN_MESSAGES)
      return {
        reason:
          "stopped: the conversation is nearly full; /sessions new starts over",
      };
    if (!context.build)
      return { reason: "stopped: the mode is no longer build" };
    return undefined;
  }

  /**
   * A done report's line. It is the model's word: "with evidence" only
   * when it names, as kind command, a command that succeeded during the
   * goal (exit 0), matched as run. Never "verified".
   */
  private doneLine(report: GoalReport): string {
    const seen = report.evidence.find(
      (item) =>
        item.kind === "command" &&
        this.commands.has(commandKey(item.ref) ?? ""),
    );
    if (!seen) return "reported done without evidence";
    // As an approval shows it: an argv as shell words, a line as written.
    const shown =
      Array.isArray(seen.ref) &&
      seen.ref.every((part) => typeof part === "string")
        ? commandLine(seen.ref)
        : commandKey(seen.ref);
    return `reported done — evidence: \`${shown}\` exited 0`;
  }

  /** What `/goal` shows while it runs. */
  describe(now: number): string {
    const left = Math.max(
      0,
      Math.ceil((GOAL_TIME_MS - (now - this.startedAt)) / 60_000),
    );
    const tokens = this.measured
      ? `${Math.round(this.outputTokens / 1000)}k of ${GOAL_OUTPUT_TOKENS / 1000}k output tokens`
      : "output tokens unmeasured";
    const last = this.last
      ? `${this.last.state}: ${oneLine(this.last.summary)}`
      : "none reported";
    return `Goal: ${this.objective}\nTurn ${this.turnsUsed} of ${this.turnsMax} · ${left} min left · ${tokens}\nLast status: ${last}\n/goal stop ends it`;
  }
}

/**
 * The notice a goal message shows in place of the user's words: its first
 * line. Undefined for any other message.
 */
export function goalNotice(content: string): string | undefined {
  if (!MARKER.test(content)) return undefined;
  return content.split("\n")[0];
}

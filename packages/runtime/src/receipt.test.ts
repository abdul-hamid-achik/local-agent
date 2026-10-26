import { expect, test } from "vitest";
import { z } from "zod";
import {
  AdmissionError,
  type AgentEvent,
  StepLimitError,
} from "#runtime/agent";
import { type Provider, resolveProvider } from "#runtime/config";
import {
  receiptOutcome,
  receiptProblem,
  receiptRunId,
  TurnReceiptRecorder,
} from "#runtime/receipt";
import { ConversationFullError } from "#runtime/session";
import { TunnelLostError } from "#runtime/transport";

/**
 * Chalupa's receipt contract, frozen from its src/inference-report.ts
 * (chalupa 5ae0a800, 2026-10-24) and made strict here, so a receipt carries
 * exactly its keys. When Chalupa changes the contract, change both.
 */
const count = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
const duration = count.max(7 * 24 * 60 * 60 * 1000);
const identifier = z
  .string()
  .min(1)
  .max(128)
  .regex(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/);
const ChalupaReceipt = z
  .object({
    schema: z.literal("local-agent.turn-receipt.v1"),
    run_id: identifier,
    model: z
      .object({
        name: z
          .string()
          .min(1)
          .max(200)
          .regex(/^[A-Za-z0-9][A-Za-z0-9._:/-]*$/),
        digest: z
          .string()
          .regex(/^(?:sha256:)?[a-f0-9]{64}$/)
          .optional(),
        num_ctx: count.positive().max(10_000_000),
        remote: z.literal(true),
      })
      .strict(),
    status: z.enum(["settled", "canceled", "failed", "not_admitted"]),
    stop_reason: identifier,
    truncated: z.boolean().optional(),
    usage: z.object({ prompt_tokens: count, eval_tokens: count }).strict(),
    tool_calls: z
      .array(z.object({ status: z.enum(["ok", "error"]) }).strict())
      .max(10000),
    tool_calls_omitted: count.max(1000000).optional(),
    timing: z
      .object({
        ttft_ms: duration,
        load_ms: duration,
        prompt_eval_ms: duration,
        eval_ms: duration,
        total_ms: duration,
      })
      .strict()
      .optional(),
  })
  .strict();

const remote: Provider = {
  type: "ollama_remote",
  model: "qwen3.5:9b-q4_K_M",
  base_url: "https://gpu.example.test",
  context_size: 32768,
  max_model_bytes: 40_000_000_000,
  stream_idle_timeout: "10m",
} as Provider;

/** A clock the test moves by hand, in milliseconds. */
function clock() {
  let now = 1000;
  return { now: () => now, at: (ms: number) => (now = ms) };
}

const done = (
  extra: Partial<Extract<AgentEvent, { type: "done" }>> = {},
): AgentEvent => ({ type: "done", inputTokens: 0, outputTokens: 0, ...extra });

test("a settled turn sums every model call, a subagent's too, and counts tool calls by outcome", () => {
  const time = clock();
  const recorder = new TurnReceiptRecorder("run-1", remote, time.now);
  recorder.start();
  time.at(1250);
  recorder.observe({ type: "thinking", text: "hm" });
  time.at(1400);
  recorder.observe({ type: "text", text: "late" });
  for (const event of [
    done({
      inputTokens: 900,
      outputTokens: 40,
      loadDuration: 2_000_000,
      promptEvalDuration: 300_400_000,
      evalDuration: 800_000_000,
    }),
    {
      type: "subagent_usage",
      inputTokens: 100,
      outputTokens: 10,
      loadDuration: 0,
      promptEvalDuration: 50_000_000,
      evalDuration: 100_000_000,
    },
    ...(
      [
        "succeeded",
        "denied",
        "failed",
        "outcome_unknown",
        "unverified",
      ] as const
    ).map(
      (status): AgentEvent => ({
        type: "tool_result",
        id: status,
        name: "read",
        status,
        output: "",
      }),
    ),
    done({
      inputTokens: 1000,
      outputTokens: 5,
      loadDuration: 1_000_000,
      promptEvalDuration: 100_000_000,
      evalDuration: 50_000_000,
      finishReason: "stop",
    }),
  ] satisfies AgentEvent[])
    recorder.observe(event);
  time.at(4000);
  const receipt = recorder.finish({
    status: "settled",
    stopReason: "completed",
  });
  expect(receipt).toEqual({
    schema: "local-agent.turn-receipt.v1",
    run_id: "run-1",
    model: { name: "qwen3.5:9b-q4_K_M", num_ctx: 32768, remote: true },
    status: "settled",
    stop_reason: "completed",
    truncated: false,
    usage: { prompt_tokens: 2000, eval_tokens: 55 },
    tool_calls: [
      { status: "ok" },
      { status: "error" },
      { status: "error" },
      { status: "error" },
      { status: "error" },
    ],
    timing: {
      ttft_ms: 250,
      load_ms: 3,
      prompt_eval_ms: 450,
      eval_ms: 950,
      total_ms: 3000,
    },
  });
  expect(ChalupaReceipt.parse(receipt)).toEqual(receipt);
  // Counts and states only: nothing the turn said or touched.
  expect(JSON.stringify(receipt)).not.toContain("late");
});

test("timing is left out unless every call reported its durations and a first token came", () => {
  const time = clock();
  const partial = new TurnReceiptRecorder("run-2", remote, time.now);
  partial.start();
  partial.observe({ type: "text", text: "a" });
  partial.observe(
    done({ loadDuration: 1, promptEvalDuration: 1, evalDuration: 1 }),
  );
  // An OpenAI-style reply: counts, no durations.
  partial.observe(done({ inputTokens: 3, outputTokens: 1 }));
  expect(
    partial.finish({ status: "settled", stopReason: "completed" }),
  ).not.toHaveProperty("timing");

  const silent = new TurnReceiptRecorder("run-3", remote, time.now);
  silent.start();
  silent.observe(
    done({ loadDuration: 1, promptEvalDuration: 1, evalDuration: 1 }),
  );
  expect(
    silent.finish({ status: "settled", stopReason: "completed" }),
  ).not.toHaveProperty("timing");

  // Never started: a turn that failed before it asked the model anything.
  const unstarted = new TurnReceiptRecorder("run-4", remote, time.now);
  const receipt = unstarted.finish({
    status: "not_admitted",
    stopReason: "not_admitted",
  });
  expect(receipt).not.toHaveProperty("timing");
  expect(receipt.usage).toEqual({ prompt_tokens: 0, eval_tokens: 0 });
  expect(ChalupaReceipt.parse(receipt)).toEqual(receipt);
});

test("a reply cut for length marks the receipt truncated", () => {
  const recorder = new TurnReceiptRecorder("run-5", remote);
  recorder.observe(done({ finishReason: "length" }));
  expect(
    recorder.finish({ status: "failed", stopReason: "step_limit" }).truncated,
  ).toBe(true);
});

test("past Chalupa's 10000 tool calls the rest are counted, not listed", () => {
  const recorder = new TurnReceiptRecorder("run-6", remote);
  for (let index = 0; index < 10_002; index++)
    recorder.observe({
      type: "tool_result",
      id: String(index),
      name: "read",
      status: "succeeded",
      output: "",
    });
  const receipt = recorder.finish({
    status: "settled",
    stopReason: "completed",
  });
  expect(receipt.tool_calls).toHaveLength(10_000);
  expect(receipt.tool_calls_omitted).toBe(2);
  expect(ChalupaReceipt.parse(receipt)).toEqual(receipt);
});

test("how a turn ended names its status and a stop reason from a closed list", () => {
  expect(receiptOutcome(new AdmissionError("missing"), false)).toEqual({
    status: "not_admitted",
    stopReason: "not_admitted",
  });
  expect(receiptOutcome(new StepLimitError("steps"), false)).toEqual({
    status: "failed",
    stopReason: "step_limit",
  });
  expect(receiptOutcome(new ConversationFullError("full"), false)).toEqual({
    status: "failed",
    stopReason: "conversation_full",
  });
  expect(receiptOutcome(new TunnelLostError("lost"), false)).toEqual({
    status: "failed",
    stopReason: "tunnel_lost",
  });
  expect(receiptOutcome(new Error("anything else"), false)).toEqual({
    status: "failed",
    stopReason: "error",
  });
  // A signal's stop is a cancel whatever error it surfaced as.
  expect(receiptOutcome(new AdmissionError("missing"), true)).toEqual({
    status: "canceled",
    stopReason: "interrupted",
  });
});

test("a run ID is Chalupa's identifier, given or a new UUID", () => {
  const made = receiptRunId();
  expect(made).toMatch(/^[0-9a-f-]{36}$/);
  expect(receiptRunId()).not.toBe(made);
  expect(receiptRunId("ci:build-42.retry_1")).toBe("ci:build-42.retry_1");
  for (const bad of ["", "-lead", "has space", "slash/no", "x".repeat(129)])
    expect(() => receiptRunId(bad), bad).toThrow(/--run-id/);
  expect(receiptRunId("x".repeat(128))).toHaveLength(128);
});

test("only a Chalupa profile has a receipt: it reports inference on a remote GPU", () => {
  expect(receiptProblem(remote)).toBeUndefined();
  // A name Chalupa's receipt refuses is refused before the run, not after.
  for (const model of ["has space", "at@host", "-lead", "x".repeat(201)])
    expect(receiptProblem({ ...remote, model }), model).toMatch(
      /does not accept the model name/,
    );
  expect(
    receiptProblem(
      resolveProvider({ OLLAMA_HOST: "127.0.0.1:11434" }, "fixture"),
    ),
  ).toMatch(/Chalupa profile/);
});

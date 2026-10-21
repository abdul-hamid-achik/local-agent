// How many bytes the client writes to fd 3 to stream one reply.
//
//   bun apps/cli/src/tuimark/stream-bytes.ts [--tokens 2000] [--token-ms 10] [--flush-ms 33]
//
// It runs the real TuimarkClient against a fake host that counts what the SDK
// would put on fd 3 (the JSON line of each batch) and applies the writes to a
// store, so the run also proves the store ends up holding the reply. The fixture
// stream is a markdown reply (paragraphs, a list, a code block) cut into tokens
// of about four characters, with a short reasoning block before it.

import type { AgentEvent, Provider, Workspace } from "@local-agent/runtime";
import { runsText } from "#cli/tuimark/blocks";
import { TuimarkClient } from "#cli/tuimark/controller";
import { FakeHost } from "#cli/tuimark/fake-host";

/** A markdown reply of about `tokens` tokens, as the pieces a model would stream. */
export function fixtureTokens(tokens: number): string[] {
  const paragraph =
    "The helper wraps the call in a **bounded retry** with exponential backoff, and `AbortError` is never retried because the caller asked for it. ";
  const code = [
    "```ts",
    "export async function fetchRetry(url: string, tries = 3) {",
    "  for (let attempt = 1; ; attempt++) {",
    "    try { return await fetch(url); }",
    "    catch (e) { if (attempt === tries) throw e; }",
    "  }",
    "}",
    "```",
    "",
  ].join("\n");
  const list =
    "- the last error is rethrown\n- the delay doubles\n- stream complete\n\n";
  let text = "";
  for (let n = 0; text.length < tokens * 4; n++)
    text += `${paragraph}${paragraph}\n\n${n % 3 === 1 ? code : ""}${n % 3 === 2 ? list : ""}`;
  const pieces: string[] = [];
  for (let i = 0; i < text.length && pieces.length < tokens; i += 4)
    pieces.push(text.slice(i, i + 4));
  return pieces;
}

export interface StreamBytes {
  tokens: number;
  bytes: number;
  batches: number;
  perToken: number;
  /** The live store just before the turn ends: what the screen would show. */
  shown: string;
  /** The reply as the runtime has it. */
  reply: string;
}

export async function measure(options: {
  tokens?: number;
  tokenMs?: number;
  flushMs?: number;
  thinkingTokens?: number;
}): Promise<StreamBytes> {
  const { tokens = 2000, tokenMs = 10, flushMs = 33 } = options;
  const thinkingTokens = options.thinkingTokens ?? 0;
  const pieces = fixtureTokens(tokens);
  const thinking = Array.from({ length: thinkingTokens }, (_, i) =>
    `step ${i} of the plan\n`.slice(0, 4 + (i % 3) * 5),
  );
  const reply = pieces.join("");
  const ui = new FakeHost();
  let shown = "";
  const controller = new AbortController();
  const client = new TuimarkClient({
    provider: {
      type: "ollama",
      model: "fixture",
      context_size: 8192,
    } as Provider,
    workspace: {} as Workspace,
    intent: "build",
    signal: controller.signal,
    flushMs,
    run: async function* () {
      for (const text of thinking) {
        yield { type: "thinking", text } as AgentEvent;
        await Bun.sleep(tokenMs);
      }
      for (const text of pieces) {
        yield { type: "text", text } as AgentEvent;
        await Bun.sleep(tokenMs);
      }
      // Let the last tick land before the message replaces the live view.
      await Bun.sleep(flushMs * 3);
      const live = ui.store.live as
        | { blocks?: { runs: unknown[] }[]; tail?: { runs: unknown[] }[] }
        | undefined;
      const parts = [...(live?.blocks ?? []), ...(live?.tail ?? [])];
      shown = parts
        .map((b) => runsText(b.runs as Parameters<typeof runsText>[0]))
        .join("\n");
      yield {
        type: "done",
        inputTokens: 100,
        outputTokens: tokens,
      } as AgentEvent;
      yield { type: "message", message: { role: "assistant", content: reply } };
      yield { type: "turn_complete", messages: [] };
    },
  });
  ui.store = JSON.parse(JSON.stringify(client.initialStore()));
  client.attach(ui);
  const send = ui.handlers.get("send");
  if (!send) throw new Error("no send handler");
  await send({ seq: 1, action: "send", source: "", keys: {}, value: "go" });
  await client.idle();
  return {
    tokens: pieces.length + thinking.length,
    bytes: ui.bytes,
    batches: ui.batches.length,
    perToken: ui.bytes / (pieces.length + thinking.length),
    shown,
    reply,
  };
}

if (import.meta.main) {
  const flag = (name: string, fallback: number) => {
    const at = Bun.argv.indexOf(`--${name}`);
    return at > 0 ? Number(Bun.argv[at + 1]) : fallback;
  };
  const result = await measure({
    tokens: flag("tokens", 2000),
    tokenMs: flag("token-ms", 10),
    flushMs: flag("flush-ms", 33),
    thinkingTokens: flag("thinking", 0),
  });
  console.log(
    `${result.tokens} tokens, ${result.batches} batches, ${result.bytes} bytes on fd 3, ${result.perToken.toFixed(1)} bytes per token`,
  );
}

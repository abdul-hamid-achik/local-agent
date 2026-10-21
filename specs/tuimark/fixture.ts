// Deterministic loopback Ollama for the Tuimark client specs (--ui tuimark).
//
//   bun specs/tuimark/fixture.ts chat|edit-approve|edit-deny
//
// The reply depends only on the last message, so one run can hold several turns:
//
//   last message is a tool result  -> "edit applied" or "edit denied"
//   prompt contains "edit"         -> one `edit` tool call on notes.txt
//   prompt contains "slow"         -> one chunk, then nothing until cancelled
//   anything else                  -> a streamed markdown reply with a code block
//
// No inference and no credentials. The Tuimark binary comes from
// TUIMARK_BINARY (CI installs the pinned release); the SDK may download it
// otherwise, so HOME and the cache variables are forwarded.

import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const mode = Bun.argv[2] ?? "chat";
const model = "fixture-model";
const port = { chat: 19210, "edit-approve": 19211, "edit-deny": 19212 }[mode];
if (!port) throw new Error(`unknown mode: ${mode}`);

const workspaceRoot = await mkdtemp(join(tmpdir(), "local-agent-tuimark-"));
await writeFile(join(workspaceRoot, "notes.txt"), "before\n");

const REPLY = [
  "Here is a **bounded retry** for the helper:\n\n",
  "```ts\n",
  "export async function fetchRetry(url, tries = 3) {\n",
  "  for (let attempt = 1; ; attempt++) {\n",
  "    try { return await fetch(url); }\n",
  "    catch (e) { if (attempt === tries) throw e; }\n",
  "  }\n",
  "}\n",
  "```\n\n",
  "## Notes\n",
  "- the last error is rethrown\n",
  "- stream complete\n",
];

const encoder = new TextEncoder();
const line = (value: unknown) => encoder.encode(`${JSON.stringify(value)}\n`);

function stream(
  chunks: Array<{ message?: unknown; done: boolean }>,
  delayMs: number,
  holdOpen = false,
) {
  let cancelled = false;
  return new Response(
    new ReadableStream({
      async start(controller) {
        for (const chunk of chunks) {
          if (cancelled) return;
          controller.enqueue(line({ model, ...chunk }));
          await Bun.sleep(delayMs);
        }
        // A reply that never finishes: the client has to cancel it.
        while (holdOpen && !cancelled) await Bun.sleep(50);
        if (!cancelled) controller.close();
      },
      cancel() {
        cancelled = true;
      },
    }),
    { headers: { "content-type": "application/x-ndjson" } },
  );
}

const assistant = (content: string, extra: object = {}) => ({
  message: { role: "assistant", content, ...extra },
  done: false,
});
const done = { done: true, prompt_eval_count: 40, eval_count: 60 };

const server = Bun.serve({
  port,
  hostname: "127.0.0.1",
  idleTimeout: 0,
  async fetch(request) {
    const path = new URL(request.url).pathname;
    if (path === "/api/tags")
      return Response.json({ models: [{ name: model, size: 1000 }] });
    if (path === "/api/show" && request.method === "POST") {
      const body = (await request.json()) as { model?: string };
      if (body.model !== model) return new Response("Wrong model", { status: 400 });
      return Response.json({
        capabilities: ["completion", "tools"],
        model_info: { "fixture.context_length": 32768 },
      });
    }
    if (request.method !== "POST" || path !== "/api/chat")
      return new Response("not found", { status: 404 });
    const body = (await request.json()) as {
      messages?: Array<{ role?: string; content?: string }>;
    };
    const last = body.messages?.at(-1);
    if (last?.role === "tool")
      return stream(
        [assistant(mode === "edit-approve" ? "edit applied" : "edit denied"), done],
        5,
      );
    const prompt = last?.content ?? "";
    if (prompt.includes("edit"))
      return stream(
        [
          assistant("", {
            tool_calls: [
              {
                function: {
                  name: "edit",
                  arguments: {
                    path: "notes.txt",
                    old_string: "before",
                    new_string: "after",
                  },
                },
              },
            ],
          }),
          done,
        ],
        5,
      );
    if (prompt.includes("slow"))
      return stream([assistant("Working on it, this takes a while")], 5, true);
    return stream([...REPLY.map((piece) => assistant(piece)), done], 25);
  },
});

const child = Bun.spawn(
  [
    process.execPath,
    "apps/cli/src/main.ts",
    "--model",
    model,
    "--mode",
    "fullscreen",
    "--ui",
    "tuimark",
    "--workspace",
    workspaceRoot,
  ],
  {
    stdin: "inherit",
    stdout: "inherit",
    stderr: "inherit",
    env: {
      PATH: process.env.PATH ?? "",
      TERM: process.env.TERM ?? "xterm-256color",
      COLORTERM: process.env.COLORTERM ?? "",
      OLLAMA_HOST: server.url.origin,
      // How the Tuimark SDK finds its binary.
      ...Object.fromEntries(
        ["TUIMARK_BINARY", "HOME", "XDG_CACHE_HOME", "TMPDIR"].flatMap((name) =>
          process.env[name] ? [[name, process.env[name]]] : [],
        ),
      ),
    },
  },
);

let cleaned = false;
async function shutdown(exitCode: number) {
  if (cleaned) return;
  cleaned = true;
  if (child.exitCode === null) child.kill("SIGTERM");
  await child.exited;
  server.stop(true);
  const contents = await readFile(join(workspaceRoot, "notes.txt"), "utf8");
  // Approved: the exact edit landed. Otherwise nothing may have changed.
  const expected = mode === "edit-approve" ? "after\n" : "before\n";
  if (contents !== expected) exitCode = 1;
  await rm(workspaceRoot, { recursive: true, force: true });
  process.exit(exitCode);
}

process.once("SIGINT", () => void shutdown(130));
process.once("SIGTERM", () => void shutdown(143));
await shutdown(await child.exited);

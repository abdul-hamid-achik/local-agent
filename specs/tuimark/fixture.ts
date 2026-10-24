// Deterministic loopback Ollama for the Tuimark client specs (--ui tuimark).
//
//   bun specs/tuimark/fixture.ts chat|edit-approve|edit-deny|session[-inline]
//
// A mode ending in -inline runs the same flow with --mode inline (the opt-in
// Tuimark inline client); its port is the fullscreen one plus 100.
//
// The reply depends only on the last message, so one run can hold several turns:
//
//   last message is a tool result  -> "edit applied N" (N counts tool replies) or "edit denied"
//   prompt starts with "count"     -> "The message had N lines: FIRST LINE" (one message,
//                                      however it was typed or pasted)
//   prompt contains "read the long file" -> one `read` tool call on long.txt
//                                      (20 lines: its card truncates, /details does not)
//   prompt contains "edit again"   -> one `edit` tool call, after -> final
//   prompt contains "edit"         -> one `edit` tool call on notes.txt
//   prompt contains "fail"         -> HTTP 500, so the turn fails
//   prompt contains "slow"         -> one chunk, then nothing until cancelled
//   anything else                  -> a streamed markdown reply with a code block
//
// In `session` mode the fixture first runs one headless turn into a session
// database, then opens the Tuimark client on that session with --resume.
// In `sessions` mode it runs two headless turns into the same database and
// opens the client with only --session-db (a fresh, empty current session):
// the spec switches to the second saved one from the /sessions picker.
//
// No inference and no credentials. The Tuimark binary comes from
// TUIMARK_BINARY (CI installs the pinned release); the SDK may download it
// otherwise, so HOME and the cache variables are forwarded.

import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const rawMode = Bun.argv[2] ?? "chat";
const inline = rawMode.endsWith("-inline");
const mode = inline ? rawMode.slice(0, -"-inline".length) : rawMode;
const model = "fixture-model";
const basePort = {
  chat: 19210,
  "edit-approve": 19211,
  "edit-deny": 19212,
  session: 19213,
  "edit-session": 19214,
  sessions: 19215,
}[mode];
if (!basePort) throw new Error(`unknown mode: ${rawMode}`);
const port = inline ? basePort + 100 : basePort;

const workspaceRoot = await mkdtemp(join(tmpdir(), "local-agent-tuimark-"));
const configHome = await mkdtemp(join(tmpdir(), "local-agent-config-"));
await writeFile(join(workspaceRoot, "notes.txt"), "before\n");
await writeFile(
  join(workspaceRoot, "long.txt"),
  `${Array.from({ length: 20 }, (_, i) =>
    i === 14 ? "long line 15 past the card" : `long line ${i + 1}`,
  ).join("\n")}\n`,
);

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

/** Tool-result replies so far: "edit applied 2" proves a second edit landed. */
let toolReplies = 0;

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
        [
          assistant(
            mode === "edit-approve" || mode === "edit-session"
              ? `edit applied ${++toolReplies}`
              : "edit denied",
          ),
          done,
        ],
        5,
      );
    const prompt = last?.content ?? "";
    if (prompt.startsWith("count"))
      return stream(
        [
          assistant(
            `The message had ${prompt.split("\n").length} lines: ${prompt.split("\n")[0]}`,
          ),
          done,
        ],
        5,
      );
    if (prompt.includes("read the long file"))
      return stream(
        [
          assistant("", {
            tool_calls: [
              {
                function: {
                  name: "read",
                  arguments: { path: "long.txt" },
                },
              },
            ],
          }),
          done,
        ],
        5,
      );
    if (prompt.includes("edit again"))
      return stream(
        [
          assistant("", {
            tool_calls: [
              {
                function: {
                  name: "edit",
                  arguments: {
                    path: "notes.txt",
                    old_string: "after",
                    new_string: "final",
                  },
                },
              },
            ],
          }),
          done,
        ],
        5,
      );
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
    if (prompt.includes("fail"))
      return new Response("fixture failure: the model exploded", {
        status: 500,
      });
    if (prompt.includes("perf stream"))
      // Startup/streaming spec: same reply, ~50ms per chunk.
      return stream([...REPLY.map((piece) => assistant(piece)), done], 50);
    if (prompt.includes("slow"))
      return stream([assistant("Working on it, this takes a while")], 5, true);
    return stream([...REPLY.map((piece) => assistant(piece)), done], 25);
  },
});

const baseArgs = ["apps/cli/src/main.ts", "--model", model, "--workspace", workspaceRoot];
const env = {
  PATH: process.env.PATH ?? "",
  TERM: process.env.TERM ?? "xterm-256color",
  COLORTERM: process.env.COLORTERM ?? "",
  OLLAMA_HOST: server.url.origin,
  // A private config home: the developer's MCP servers and rule trust stay
  // out of the specs (HOME is forwarded for the Tuimark SDK's cache).
  XDG_CONFIG_HOME: configHome,
  // How the Tuimark SDK finds its binary.
  ...Object.fromEntries(
    ["TUIMARK_BINARY", "HOME", "XDG_CACHE_HOME", "TMPDIR"].flatMap((name) =>
      process.env[name] ? [[name, process.env[name]]] : [],
    ),
  ),
};

const extraArgs: string[] = [];
let sessionDirectory: string | undefined;
if (mode === "session") {
  sessionDirectory = await mkdtemp(join(tmpdir(), "local-agent-tuimark-db-"));
  const database = join(sessionDirectory, "sessions.db");
  const first = Bun.spawn(
    [
      process.execPath,
      ...baseArgs,
      "--mode",
      "headless",
      "-p",
      "stream the first reply",
      "--session-db",
      database,
    ],
    { stdout: "ignore", stderr: "pipe", env },
  );
  const log = await new Response(first.stderr).text();
  const id = /Session: (\w+)/.exec(log)?.[1];
  if ((await first.exited) !== 0 || !id) {
    console.error(`headless session failed: ${log}`);
    process.exit(1);
  }
  extraArgs.push("--session-db", database, "--resume", id);
}
if (mode === "sessions") {
  sessionDirectory = await mkdtemp(join(tmpdir(), "local-agent-tuimark-db-"));
  const database = join(sessionDirectory, "sessions.db");
  for (const prompt of ["stream the first reply", "stream the second reply"]) {
    const run = Bun.spawn(
      [
        process.execPath,
        ...baseArgs,
        "--mode",
        "headless",
        "-p",
        prompt,
        "--session-db",
        database,
      ],
      { stdout: "ignore", stderr: "pipe", env },
    );
    const log = await new Response(run.stderr).text();
    if ((await run.exited) !== 0) {
      console.error(`headless session failed: ${log}`);
      process.exit(1);
    }
  }
  extraArgs.push("--session-db", database);
}

const child = Bun.spawn(
  [
    process.execPath,
    ...baseArgs,
    "--mode",
    inline ? "inline" : "fullscreen",
    "--ui",
    "tuimark",
    ...extraArgs,
  ],
  { stdin: "inherit", stdout: "inherit", stderr: "inherit", env },
);

let cleaned = false;
async function shutdown(exitCode: number) {
  if (cleaned) return;
  cleaned = true;
  if (child.exitCode === null) child.kill("SIGTERM");
  await child.exited;
  server.stop(true);
  const contents = await readFile(join(workspaceRoot, "notes.txt"), "utf8");
  // Approved: the exact edit landed (twice for edit-session: the second
  // edit only lands if the session grant let it through). Otherwise
  // nothing may have changed.
  const expected =
    mode === "edit-session"
      ? "final\n"
      : mode === "edit-approve"
        ? "after\n"
        : "before\n";
  if (contents !== expected) exitCode = 1;
  await rm(workspaceRoot, { recursive: true, force: true });
  await rm(configHome, { recursive: true, force: true });
  if (sessionDirectory) await rm(sessionDirectory, { recursive: true, force: true });
  process.exit(exitCode);
}

process.once("SIGINT", () => void shutdown(130));
process.once("SIGTERM", () => void shutdown(143));
await shutdown(await child.exited);

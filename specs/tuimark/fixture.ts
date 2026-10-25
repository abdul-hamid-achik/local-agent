// Deterministic loopback Ollama for the Tuimark client specs (--ui tuimark).
//
//   bun specs/tuimark/fixture.ts chat|edit-approve|edit-deny|session|goal[-inline]
//   bun specs/tuimark/fixture.ts old-binary
//
// A mode ending in -inline runs the same flow with --mode inline (the opt-in
// Tuimark inline client). `old-binary` passes no --ui, and TUIMARK_BINARY
// names a stand-in that says it is tuimark 0.4.6: the default client must
// fall back to the Vue client. The stand-in logs its arguments, and the run
// fails unless it was asked its version once and never started as a host.
//
// The reply depends only on the last message, so one run can hold several turns:
//
//   last message is a tool result  -> by its result: "edit applied N" (N counts applied
//                                      edits), "edit denied", or "TOOL STATUS: first line of
//                                      its output" for anything else;
//                                      an ask_user answer -> "noted: " and the answer, on one line;
//                                      a goal_status receipt -> "status recorded: STATE"
//   a goal message on "tidy the notes" ([goal N/M] …) -> by N: 1, a goal_status continue;
//                                      2, an edit of notes.txt and a continue; 3 on, a done
//   prompt starts with "count"     -> "The message had N lines: FIRST LINE" (one message,
//                                      however it was typed or pasted)
//   prompt contains "ask me"       -> one ask_user call: a choice of three stores
//   prompt contains "ask for text" -> one ask_user call with a typed answer
//   prompt contains "read the long file" -> one `read` tool call on long.txt
//                                      (20 lines: its card truncates, /details does not)
//   prompt contains "read outside" -> one `read` tool call on a path outside the workspace
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
// No inference and no credentials. LOCAL_AGENT_BIN, when set, is the
// executable to run instead of the source. The Tuimark binary comes from
// TUIMARK_BINARY (CI installs the pinned release); the SDK may download it
// otherwise, so HOME and the cache variables are forwarded.

import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const rawMode = Bun.argv[2] ?? "chat";
const inline = rawMode.endsWith("-inline");
const mode = inline ? rawMode.slice(0, -"-inline".length) : rawMode;
const model = "fixture-model";
const modes = ["chat", "edit-approve", "edit-deny", "session", "edit-session", "sessions", "goal", "old-binary"];
if (!modes.includes(mode)) throw new Error(`unknown mode: ${rawMode}`);

const workspaceRoot = await mkdtemp(join(tmpdir(), "local-agent-tuimark-"));
const configHome = await mkdtemp(join(tmpdir(), "local-agent-config-"));
// What the old-binary stand-in was called with (see the old-binary mode).
const oldCalls = join(configHome, "tuimark-0.4.6.calls");
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

/** An ask_user receipt's output on one line, or undefined for another tool's. */
function askAnswer(content: string): string | undefined {
  try {
    const output = (JSON.parse(content) as { output?: unknown }).output;
    return typeof output === "string" &&
      /^The user (answered|chose|declined|sent)/u.test(output)
      ? output.replace(/\s+/gu, " ")
      : undefined;
  } catch {
    return undefined;
  }
}

/** The state a goal_status receipt recorded, or undefined for another tool's. */
function recorded(content: string): string | undefined {
  try {
    const output = (JSON.parse(content) as { output?: unknown }).output;
    return typeof output === "string"
      ? /^Recorded: (\w+)\./u.exec(output)?.[1]
      : undefined;
  } catch {
    return undefined;
  }
}

const goalStatus = (args: object) => ({
  function: { name: "goal_status", arguments: args },
});

/** Edits applied so far: "edit applied 2" proves a second edit landed. */
let toolReplies = 0;

// Any free port: the client is given the server's own URL.
const server = Bun.serve({
  port: 0,
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
      messages?: Array<{ role?: string; content?: string; tool_name?: string }>;
    };
    const last = body.messages?.at(-1);
    const answered =
      last?.role === "tool" ? askAnswer(last.content ?? "") : undefined;
    if (answered !== undefined)
      return stream([assistant(`noted: ${answered}`), done], 5);
    const state =
      last?.role === "tool" ? recorded(last.content ?? "") : undefined;
    if (state !== undefined)
      return stream([assistant(`status recorded: ${state}`), done], 5);
    if (last?.role === "tool") {
      // The reply says what the call's result was, so a spec's wait proves
      // what the client did, not only the mode the fixture runs in.
      const result = JSON.parse(last.content ?? "{}") as { status?: string; output?: unknown };
      const reply =
        last.tool_name === "edit" && result.status === "succeeded"
          ? `edit applied ${++toolReplies}`
          : last.tool_name === "edit" && result.status === "denied"
            ? "edit denied"
            : `${last.tool_name} ${result.status}: ${String(result.output ?? "").split("\n")[0]}`;
      return stream([assistant(reply), done], 5);
    }
    const prompt = last?.content ?? "";
    const goalTurn = /^\[goal (\d+)\/\d+\] .*tidy the notes/u.exec(prompt)?.[1];
    if (goalTurn !== undefined) {
      const step = Number(goalTurn);
      const calls =
        step === 1
          ? [goalStatus({ state: "continue", summary: "read the notes" })]
          : step === 2
            ? [
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
                goalStatus({ state: "continue", summary: "notes.txt says after" }),
              ]
            : [
                goalStatus({
                  state: "done",
                  summary: "the notes are tidy",
                  evidence: [{ kind: "file", ref: "notes.txt" }],
                }),
              ];
      return stream([assistant("", { tool_calls: calls }), done], 5);
    }
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
    if (prompt.includes("ask for text"))
      return stream(
        [
          assistant("", {
            tool_calls: [
              {
                function: {
                  name: "ask_user",
                  arguments: { question: "Which port does staging use?" },
                },
              },
            ],
          }),
          done,
        ],
        5,
      );
    if (prompt.includes("ask me"))
      return stream(
        [
          assistant("", {
            tool_calls: [
              {
                function: {
                  name: "ask_user",
                  arguments: {
                    question: "Which store should the cache use?",
                    options: [
                      { id: "pg", label: "Postgres" },
                      { id: "lite", label: "SQLite", detail: "one file" },
                      { id: "mem", label: "Memory only" },
                    ],
                  },
                },
              },
            ],
          }),
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
    if (prompt.includes("read outside"))
      return stream(
        [
          assistant("", {
            tool_calls: [
              { function: { name: "read", arguments: { path: "../outside.txt" } } },
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

// LOCAL_AGENT_BIN runs a release's compiled executable instead of this
// checkout's source: release.yml runs specs against each archive it builds.
const cli = process.env.LOCAL_AGENT_BIN
  ? [process.env.LOCAL_AGENT_BIN]
  : [process.execPath, "apps/cli/src/main.ts"];
const baseArgs = ["--model", model, "--workspace", workspaceRoot];
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

/** Every process this fixture started, headless setup runs included. */
const children: Bun.Subprocess[] = [];
let sessionDirectory: string | undefined;

let stopping: Promise<never> | undefined;
/**
 * Stops every process, checks notes.txt, removes the directories and
 * exits, once: a later call gets the same promise, which never settles,
 * so whatever awaited it does not go on.
 */
function shutdown(exitCode: number): Promise<never> {
  stopping ??= stop(exitCode);
  return stopping;
}

/** The setup goes on only while the fixture is not shutting down. */
async function going(): Promise<void> {
  if (stopping) await stopping;
}

async function stop(exitCode: number): Promise<never> {
  for (const run of children) if (run.exitCode === null) run.kill("SIGTERM");
  await Promise.all(children.map((run) => run.exited));
  server.stop(true);
  try {
    const contents = await readFile(join(workspaceRoot, "notes.txt"), "utf8").catch(() => undefined);
    // Approved: the exact edit landed (twice for edit-session: the second
    // edit only lands if the session grant let it through; once for goal,
    // in its second turn). Otherwise nothing may have changed.
    const expected =
      mode === "edit-session"
        ? "final\n"
        : mode === "edit-approve" || mode === "goal"
          ? "after\n"
          : "before\n";
    if (contents !== expected) exitCode = 1;
    if (mode === "old-binary") {
      const calls = await readFile(oldCalls, "utf8").catch(() => "");
      if (calls !== "--version\n") {
        console.error(`the old tuimark was called ${JSON.stringify(calls)}, not asked its version once`);
        exitCode = 1;
      }
    }
  } finally {
    await rm(workspaceRoot, { recursive: true, force: true });
    await rm(configHome, { recursive: true, force: true });
    if (sessionDirectory) await rm(sessionDirectory, { recursive: true, force: true });
  }
  process.exit(exitCode);
}

// Before any process starts: a spec that times out during the headless
// setup still stops it and removes the directories.
process.on("SIGINT", () => void shutdown(130));
process.on("SIGTERM", () => void shutdown(143));

/** A headless turn for the setup; a failure ends the fixture with 1. */
async function headless(prompt: string, database: string): Promise<string> {
  await going();
  const run = Bun.spawn(
    [...cli, ...baseArgs, "--mode", "headless", "-p", prompt, "--session-db", database],
    { stdout: "ignore", stderr: "pipe", env },
  );
  children.push(run);
  const log = await new Response(run.stderr).text();
  if ((await run.exited) !== 0) {
    console.error(`headless session failed: ${log}`);
    await shutdown(1);
  }
  return log;
}

const extraArgs: string[] = [];
if (mode === "old-binary") {
  const old = join(configHome, "tuimark-0.4.6");
  await writeFile(old, `#!/bin/sh\necho "$*" >> '${oldCalls}'\necho "tuimark 0.4.6 (commit fixture)"\n`, {
    mode: 0o755,
  });
  env.TUIMARK_BINARY = old;
}
if (mode === "session") {
  await going();
  sessionDirectory = await mkdtemp(join(tmpdir(), "local-agent-tuimark-db-"));
  const database = join(sessionDirectory, "sessions.db");
  const log = await headless("stream the first reply", database);
  const id = /Session: (\w+)/.exec(log)?.[1];
  if (!id) {
    console.error(`headless session named no session: ${log}`);
    await shutdown(1);
  }
  extraArgs.push("--session-db", database, "--resume", id ?? "");
}
if (mode === "sessions") {
  await going();
  sessionDirectory = await mkdtemp(join(tmpdir(), "local-agent-tuimark-db-"));
  const database = join(sessionDirectory, "sessions.db");
  for (const prompt of ["stream the first reply", "stream the second reply"])
    await headless(prompt, database);
  extraArgs.push("--session-db", database);
}

await going();
const child = Bun.spawn(
  [
    ...cli,
    ...baseArgs,
    "--mode",
    inline ? "inline" : "fullscreen",
    ...(mode === "old-binary" ? [] : ["--ui", "tuimark"]),
    ...extraArgs,
  ],
  { stdin: "inherit", stdout: "inherit", stderr: "inherit", env },
);
children.push(child);
await shutdown(await child.exited);

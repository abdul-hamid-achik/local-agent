import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const mode = Bun.argv[2] ?? "headless";
const verifyMode = mode === "verify";
const cliMode =
  verifyMode
    ? "headless"
    : mode === "edit-deny" || mode === "edit-approve"
      ? "fullscreen"
      : mode;
const prompt = Bun.argv[3] ?? "hello";
const model = "fixture-model";
const editMode =
  mode === "edit-deny" || mode === "edit-approve" || verifyMode;
const configHome = await mkdtemp(join(tmpdir(), "local-agent-config-"));
const workspaceRoot = editMode
  ? await mkdtemp(join(tmpdir(), "local-agent-glyph-"))
  : undefined;
if (workspaceRoot) await writeFile(join(workspaceRoot, "notes.txt"), "before\n");
const fixturePort =
  {
    headless: 19200,
    inline: 19201,
    fullscreen: 19202,
    verify: 19203,
    "edit-approve": 19204,
    "edit-deny": 19205,
  }[mode] ?? 19206;
const server = Bun.serve({
  port: fixturePort,
  hostname: "127.0.0.1",
  async fetch(request) {
    if (new URL(request.url).pathname === "/api/tags") return Response.json({ models: [{ name: model, size: 1000 }] });
    if (new URL(request.url).pathname === "/api/show" && request.method === "POST") {
      const body = await request.json();
      if (body.model !== model) return new Response("Wrong model", { status: 400 });
      return Response.json({ capabilities: ["completion", "tools"], model_info: { "fixture.context_length": 65536 } });
    }
    if (request.method !== "POST" || new URL(request.url).pathname !== "/api/chat")
      return new Response("not found", { status: 404 });

    const body = (await request.json()) as {
      messages?: Array<{ content?: string; role?: string }>;
    };
    const requested = body.messages?.at(-1)?.content ?? "";
    const hasToolResult = body.messages?.some((message) => message.role === "tool");
    const toolCount = body.messages?.filter((message) => message.role === "tool").length ?? 0;
    if (verifyMode && hasToolResult) {
      const result = JSON.parse(body.messages?.findLast((message) => message.role === "tool")?.content ?? "{}");
      if (result.status !== "succeeded")
        return new Response("Unexpected tool outcome", { status: 500 });
      if (toolCount === 3) {
        const command = JSON.parse(result.output ?? "{}");
        if (command.exitCode !== 0 || !command.output.includes("verification passed"))
          return new Response("Verification command did not prove the edit", { status: 500 });
      }
    }
    const answer = hasToolResult
      ? verifyMode
        ? "verification complete"
        : mode === "edit-approve"
          ? "edit applied"
          : "edit denied"
      : `fixture reply to ${requested}`;
    const verifyCalls = [
      { name: "read", arguments: { path: "notes.txt" } },
      {
        name: "edit",
        arguments: { path: "notes.txt", old_string: "before", new_string: "after" },
      },
      {
        name: "run_command",
        arguments: {
          argv: [
            process.execPath,
            "-e",
            "if (await Bun.file('notes.txt').text() !== 'after\\n') process.exit(1); await Bun.write('verified.txt', 'checked'); console.log('verification passed')",
          ],
          timeout_ms: 5000,
        },
      },
    ];
    const verifyCall = verifyCalls[toolCount];
    const chunks = [
      {
        model,
        message: verifyMode && verifyCall
            ? {
                role: "assistant",
                content: "",
                ...(verifyCall ? { tool_calls: [{ function: verifyCall }] } : {}),
              }
          : hasToolResult
            ? { role: "assistant", content: answer }
            : editMode
            ? {
                role: "assistant",
                content: "",
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
              }
            : { role: "assistant", content: answer },
        done: false,
      },
      {
        model,
        message: { role: "assistant", content: "" },
        done: true,
        prompt_eval_count: 1,
        eval_count: 2,
      },
    ];
    return new Response(`${chunks.map((chunk) => JSON.stringify(chunk)).join("\n")}\n`, {
      headers: { "content-type": "application/x-ndjson" },
    });
  },
});

const args = [
  "apps/cli/src/main.ts",
  "--model",
  model,
  "--mode",
  cliMode,
  // These contracts exercise the Vue client; Tuimark is the default.
  "--ui",
  "vue",
  ...(workspaceRoot ? ["--workspace", workspaceRoot] : []),
  ...(mode === "headless" ? ["--prompt", prompt] : []),
  ...(verifyMode
    ? [
        "--prompt",
        "Read, edit and verify notes.txt",
        "--json",
        "--approve-edits",
        "--approve-commands",
      ]
    : []),
];
const child = Bun.spawn([process.execPath, ...args], {
  stdin: "inherit",
  stdout: "inherit",
  stderr: "inherit",
  env: {
    PATH: process.env.PATH ?? "",
    TERM: process.env.TERM ?? "xterm-256color",
    COLORTERM: process.env.COLORTERM ?? "",
    OLLAMA_HOST: server.url.origin,
    // A private config home: the developer's MCP servers and rule trust
    // stay out of the specs.
    XDG_CONFIG_HOME: configHome,
  },
});

let cleaned = false;
async function shutdown(exitCode: number) {
  if (cleaned) return;
  cleaned = true;
  if (child.exitCode === null) child.kill("SIGTERM");
  await child.exited;
  server.stop(true);
  if (workspaceRoot) {
    const contents = await readFile(join(workspaceRoot, "notes.txt"), "utf8");
    const expected = mode === "edit-approve" || verifyMode ? "after\n" : "before\n";
    if (contents !== expected) exitCode = 1;
    if (verifyMode && !(await Bun.file(join(workspaceRoot, "verified.txt")).text() === "checked")) exitCode = 1;
    await rm(workspaceRoot, { recursive: true, force: true });
  }
  await rm(configHome, { recursive: true, force: true });
  process.exit(exitCode);
}

process.once("SIGINT", () => void shutdown(130));
process.once("SIGTERM", () => void shutdown(143));
await shutdown(await child.exited);

import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { SessionStore } from "@local-agent/runtime";
import { expect, test } from "vitest";

test.each([
  [],
  ["--approve-edits"],
  ["--approve-commands"],
  ["--approve-edits", "--approve-commands"],
])(
  "headless read/edit/verify honors separate grants: %j",
  async (...flags: string[]) => {
    const root = await mkdtemp(join(tmpdir(), "local-agent-command-flow-"));
    await writeFile(join(root, "notes.txt"), "before");
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      async fetch(request) {
        const path = new URL(request.url).pathname;
        if (path === "/api/tags")
          return Response.json({ models: [{ name: "fixture", size: 1000 }] });
        if (path === "/api/show")
          return Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 16384 },
          });
        const body = (await request.json()) as { messages: { role: string }[] };
        const step = body.messages.filter(
          (message) => message.role === "tool",
        ).length;
        const calls = [
          { name: "read", arguments: { path: "notes.txt" } },
          {
            name: "edit",
            arguments: {
              path: "notes.txt",
              old_string: "before",
              new_string: "after",
            },
          },
          {
            name: "run_command",
            arguments: {
              argv: [
                process.execPath,
                "-e",
                "if (await Bun.file('notes.txt').text() !== 'after') process.exit(1); await Bun.write('verified.txt', 'checked'); console.log('verification passed')",
              ],
              timeout_ms: 5000,
            },
          },
        ];
        const call = calls[step];
        return new Response(
          `${JSON.stringify({ message: { role: "assistant", content: call ? "" : "Finished", ...(call ? { tool_calls: [{ function: call }] } : {}) }, done: true })}\n`,
        );
      },
    });
    try {
      const child = Bun.spawn(
        [
          process.execPath,
          new URL("./main.ts", import.meta.url).pathname,
          "--model",
          "fixture",
          "--mode",
          "headless",
          "--workspace",
          root,
          "--session-db",
          join(root, "sessions.sqlite"),
          "--prompt",
          "Read, edit and verify notes.txt",
          "--json",
          ...flags,
        ],
        {
          env: { PATH: process.env.PATH ?? "", OLLAMA_HOST: server.url.origin },
          stdout: "pipe",
          stderr: "pipe",
        },
      );
      const [stdout, stderr, code] = await Promise.all([
        new Response(child.stdout).text(),
        new Response(child.stderr).text(),
        child.exited,
      ]);
      expect(stderr).toBe("");
      expect(code).toBe(0);
      const results = stdout
        .trim()
        .split("\n")
        .map((line) => JSON.parse(line))
        .filter((event) => event.type === "tool_result");
      const edits = flags.includes("--approve-edits");
      const commands = flags.includes("--approve-commands");
      expect(results.map((event) => event.status)).toEqual([
        "succeeded",
        edits ? "succeeded" : "denied",
        commands ? (edits ? "succeeded" : "failed") : "denied",
      ]);
      expect(await readFile(join(root, "notes.txt"), "utf8")).toBe(
        edits ? "after" : "before",
      );
      expect(await Bun.file(join(root, "verified.txt")).exists()).toBe(
        edits && commands,
      );
      const store = await SessionStore.open(join(root, "sessions.sqlite"));
      try {
        const states = await store.listSessions(root);
        expect(states).toHaveLength(1);
        const state = states[0];
        if (!state) throw new Error("Missing durable session");
        expect(
          state.messages.filter((message) => message.role === "tool"),
        ).toHaveLength(3);
        expect(
          state.messages.filter((message) => message.tool_calls?.length),
        ).toHaveLength(3);
        expect(store.listHazards(state.id, state.workspaceId)).toEqual([]);
        expect(state.messages.at(-1)).toMatchObject({
          role: "assistant",
          content: "Finished",
        });
      } finally {
        store.close();
      }
    } finally {
      await server.stop(true);
      await rm(root, { recursive: true, force: true });
    }
  },
);

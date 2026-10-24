import { mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { appendRule } from "@local-agent/runtime";
import { expect, test } from "vitest";

// The desktop sidecar consults the workspace's permission file like every
// other client: a trusted allow rule skips the approval card, a deny rule
// refuses before one is requested, and a fetch deny rule applies through
// the runtime's read hook.
test("the sidecar applies the workspace's rules before requesting approval", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  await writeFile(join(root, "notes.txt"), "before");
  await appendRule(root, {
    effect: "allow",
    tool: "edit",
    pattern: "/notes.txt",
  });
  await appendRule(root, {
    effect: "deny",
    tool: "run_command",
    pattern: "rm *",
  });
  await appendRule(root, {
    effect: "deny",
    tool: "fetch",
    pattern: "domain:blocked.example",
  });
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
      const done = body.messages.some((message) => message.role === "tool");
      const message = done
        ? { role: "assistant", content: "Finished" }
        : {
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
              {
                function: {
                  name: "run_command",
                  arguments: { argv: ["sudo", "rm", "notes.txt"] },
                },
              },
              {
                function: {
                  name: "fetch",
                  arguments: { url: "https://blocked.example/x" },
                },
              },
            ],
          };
      return new Response(`${JSON.stringify({ message, done: true })}\n`);
    },
  });
  const child = Bun.spawn(
    [process.execPath, new URL("./sidecar.ts", import.meta.url).pathname],
    {
      env: {
        PATH: process.env.PATH ?? "",
        OLLAMA_HOST: server.url.origin,
        DESKTOP_REPO_ROOT: root,
        // The test's private config home: never the user's trust store.
        XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME ?? "",
      },
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  try {
    child.stdin.write(
      `${JSON.stringify({
        id: 1,
        method: "agent.start",
        params: {
          runId: "r1",
          workspace: root,
          intent: "build",
          model: "fixture",
          messages: [{ role: "user", content: "go" }],
        },
      })}\n`,
    );
    child.stdin.flush();
    const events: { type: string; [key: string]: unknown }[] = [];
    const reader = child.stdout.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    const deadline = Date.now() + 8_000;
    while (!events.some((event) => event.type.startsWith("run_"))) {
      if (Date.now() > deadline) throw new Error("sidecar run never ended");
      const chunk = await reader.read();
      if (chunk.done) break;
      buffer += decoder.decode(chunk.value, { stream: true });
      for (let at = buffer.indexOf("\n"); at >= 0; at = buffer.indexOf("\n")) {
        const frame = JSON.parse(buffer.slice(0, at));
        buffer = buffer.slice(at + 1);
        if (frame.stream === "r1") events.push(frame.event);
      }
    }
    expect(events.at(-1)?.type).toBe("run_done");
    expect(events.some((event) => event.type === "approval_requested")).toBe(
      false,
    );
    const results = events.filter((event) => event.type === "tool_result");
    expect(results.map((event) => event.status)).toEqual([
      "succeeded",
      "denied",
      "failed",
    ]);
    expect(await readFile(join(root, "notes.txt"), "utf8")).toBe("after");
  } finally {
    child.kill();
    await child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { type Message, SessionStore } from "@local-agent/runtime";
import { expect, test } from "vitest";

test("resumes completed state and inspects hazardous sessions without replaying effects", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-session-flow-"));
  const path = join(root, "sessions.sqlite");
  const store = await SessionStore.open(path);
  let chats = 0;
  let requests = 0;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(req) {
      requests++;
      const endpoint = new URL(req.url).pathname;
      if (endpoint === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (endpoint === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      const body = (await req.json()) as {
        messages: { role: string; content: string }[];
      };
      chats++;
      expect(body.messages).toContainEqual({
        role: "assistant",
        content: "Remembered answer",
      });
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: "Resumed answer" }, done: true })}\n`,
      );
    },
  });
  async function launch(args: string[], model = false) {
    const child = Bun.spawn(
      [
        process.execPath,
        new URL("./main.ts", import.meta.url).pathname,
        "--session-db",
        path,
        "--workspace",
        root,
        ...args,
      ],
      {
        env: model
          ? { OLLAMA_HOST: server.url.origin, LOCAL_AGENT_MODEL: "fixture" }
          : { LOCAL_AGENT_PROVIDER: "invalid-on-purpose" },
        stdout: "pipe",
        stderr: "pipe",
      },
    );
    const [stdout, stderr, code] = await Promise.all([
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
      child.exited,
    ]);
    return { stdout, stderr, code };
  }
  try {
    const state = await store.createSession(root, [
      { role: "user", content: "Remember this" },
      { role: "assistant", content: "Remembered answer" },
    ]);
    const listing = await launch(["--sessions"]);
    expect(listing.code).toBe(0);
    expect(JSON.parse(listing.stdout)).toEqual([
      { id: state.publicId, revision: 0, messages: 2 },
    ]);
    const resumed = await launch(
      ["--resume", state.publicId, "--prompt", "Continue", "--json"],
      true,
    );
    expect(resumed.stderr).toBe("");
    expect(resumed.code).toBe(0);
    const restored = await store.loadSession(state.publicId, root);
    expect(restored.messages).toHaveLength(4);
    expect(restored.messages.at(-1)?.content).toBe("Resumed answer");
    const identity = {
      sessionId: state.id,
      workspaceId: state.workspaceId,
      runId: "interrupted",
      turnId: "interrupted",
      executionId: "interrupted-tool",
      idempotencyKey: "interrupted-tool",
      canonicalCallId: "interrupted-tool",
      toolName: "run_command",
      effectClass: "effectful" as const,
      argumentsSha256: "0".repeat(64),
    };
    store.appendEvent({ ...identity, type: "requested" });
    store.appendEvent({
      ...identity,
      type: "started",
      detail: "private-recovery-detail",
    });
    const inspection = await launch(["--session-info", state.publicId]);
    expect(inspection.code).toBe(0);
    expect(JSON.parse(inspection.stdout).hazards).toHaveLength(1);
    const recovery = await launch(["--recovery-info", state.publicId]);
    expect(recovery.code).toBe(0);
    const review = JSON.parse(recovery.stdout);
    expect(review.revision).toBe(restored.revision);
    expect(review.hazards).toHaveLength(1);
    expect(review.hazards[0]).toEqual({
      eventId: expect.any(Number),
      executionId: "interrupted-tool",
      turnId: "interrupted",
      toolName: "run_command",
      eventType: "started",
      effectClass: "effectful",
      argumentsSha256: "0".repeat(64),
    });
    expect(recovery.stdout).not.toContain("private-recovery-detail");
    expect(recovery.stdout).not.toContain("Remembered answer");
    expect(recovery.stdout).not.toContain("messages");
    expect((await store.loadSession(state.publicId, root)).revision).toBe(
      restored.revision,
    );
    const blocked = await launch(
      ["--resume", state.publicId, "--prompt", "Retry", "--json"],
      true,
    );
    expect(blocked.code).toBe(1);
    expect(blocked.stderr).toContain("uncertain tool outcome");
    expect(chats).toBe(1);
    expect(
      (await store.loadSession(state.publicId, root)).messages,
    ).toHaveLength(4);
    const call: Message = {
      role: "assistant",
      content: "",
      tool_calls: [{ function: { name: "read", arguments: { path: "file" } } }],
    };
    const receipt: Message = {
      role: "tool",
      tool_name: "read",
      content: "result",
    };
    const invalidHistories: Message[][] = [
      [receipt, call],
      [call, { ...receipt, tool_name: "edit" }],
      [call, { role: "user", content: "Interrupted" }, receipt],
      [{ ...call, role: "user" }, receipt],
      [call],
      [
        {
          ...call,
          tool_calls: [
            {
              id: "first",
              function: { name: "read", arguments: { path: "first" } },
            },
            {
              id: "second",
              function: { name: "read", arguments: { path: "second" } },
            },
          ],
        },
        { ...receipt, tool_call_id: "second" },
        { ...receipt, tool_call_id: "first" },
      ],
    ];
    const previousRequests = requests;
    for (const messages of invalidHistories) {
      const invalid = await store.createSession(root, messages);
      const rejected = await launch(
        ["--resume", invalid.publicId, "--prompt", "Continue", "--json"],
        true,
      );
      expect(rejected.code).toBe(1);
      expect(rejected.stderr).toContain("recovery is required");
      expect((await store.loadSession(invalid.publicId, root)).revision).toBe(
        0,
      );
    }
    expect(requests).toBe(previousRequests);
  } finally {
    await server.stop(true);
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

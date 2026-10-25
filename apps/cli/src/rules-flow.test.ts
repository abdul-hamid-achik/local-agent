import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { trustRules } from "@local-agent/runtime";
import { expect, test } from "vitest";

// Headless runs consult the workspace's rules for reads and proposals
// alike: a fetch deny rule refuses before any DNS lookup, a command deny
// rule looks through `sudo` even with --approve-commands, and an allow rule
// approves an edit without --approve-edits only once this user trusts it.
test("headless runs apply the workspace's deny and trusted allow rules", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-rules-flow-"));
  await writeFile(join(root, "notes.txt"), "before");
  await mkdir(join(root, ".local-agent"));
  await writeFile(
    join(root, ".local-agent", "permissions.json"),
    JSON.stringify({
      version: 1,
      rules: [
        { effect: "deny", tool: "fetch", pattern: "domain:blocked.example" },
        { effect: "deny", tool: "run_command", pattern: "rm *" },
        { effect: "allow", tool: "edit", pattern: "/notes.txt" },
      ],
    }),
  );
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
        { name: "fetch", arguments: { url: "https://blocked.example/x" } },
        {
          name: "run_command",
          arguments: { argv: ["sudo", "rm", "notes.txt"], timeout_ms: 5000 },
        },
        {
          name: "edit",
          arguments: {
            path: "notes.txt",
            old_string: "before",
            new_string: "after",
          },
        },
      ];
      const call = calls[step % calls.length];
      const done = step > 0 && step % calls.length === 0;
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: done ? "Finished" : "", ...(done ? {} : { tool_calls: [{ function: call }] }) }, done: true })}\n`,
      );
    },
  });
  const run = async () => {
    const child = Bun.spawn(
      [
        process.execPath,
        fileURLToPath(new URL("./main.ts", import.meta.url)),
        "--model",
        "fixture",
        "--mode",
        "headless",
        "--workspace",
        root,
        "--prompt",
        "go",
        "--json",
        "--approve-commands",
      ],
      {
        env: {
          PATH: process.env.PATH ?? "",
          OLLAMA_HOST: server.url.origin,
          // The test's private config home: never the user's trust store.
          XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME ?? "",
        },
        stdout: "pipe",
        stderr: "pipe",
      },
    );
    const [stdout, stderr, code] = await Promise.all([
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
      child.exited,
    ]);
    expect(code).toBe(0);
    const results = stdout
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line))
      .filter((event) => event.type === "tool_result");
    return { stdout, stderr, statuses: results.map((event) => event.status) };
  };
  try {
    const first = await run();
    expect(first.statuses).toEqual(["failed", "denied", "denied"]);
    expect(first.stdout).toContain(
      "blocked.example is denied by rule: domain:blocked.example",
    );
    expect(first.stderr).toContain("1 allow rule not trusted");
    expect(await readFile(join(root, "notes.txt"), "utf8")).toBe("before");

    await trustRules(root);
    const second = await run();
    expect(second.statuses).toEqual(["failed", "denied", "succeeded"]);
    expect(second.stderr).not.toContain("not trusted");
    expect(await readFile(join(root, "notes.txt"), "utf8")).toBe("after");
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

// An ask rule wants a person. Headless there is none, so no --approve-* flag
// answers for it: the call is refused and stderr says which rule did it,
// while the same flag still approves a call no rule names.
test("headless refuses an ask rule's call whatever the flags say", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-rules-flow-"));
  await writeFile(join(root, "notes.txt"), "before");
  await mkdir(join(root, ".local-agent"));
  await writeFile(
    join(root, ".local-agent", "permissions.json"),
    JSON.stringify({
      version: 1,
      rules: [
        { effect: "ask", tool: "run_command", pattern: "echo *" },
        { effect: "ask", tool: "edit", pattern: "/notes.txt" },
      ],
    }),
  );
  const calls = [
    { name: "run_command", arguments: { argv: ["echo", "hi"] } },
    { name: "run_command", arguments: { argv: ["true"] } },
    {
      name: "edit",
      arguments: {
        path: "notes.txt",
        old_string: "before",
        new_string: "after",
      },
    },
  ];
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
      const call =
        calls[
          body.messages.filter((message) => message.role === "tool").length
        ];
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: call ? "" : "Finished", ...(call ? { tool_calls: [{ function: call }] } : {}) }, done: true })}\n`,
      );
    },
  });
  try {
    const child = Bun.spawn(
      [
        process.execPath,
        fileURLToPath(new URL("./main.ts", import.meta.url)),
        "--model",
        "fixture",
        "--workspace",
        root,
        "--prompt",
        "go",
        "--json",
        "--approve-commands",
        "--approve-edits",
      ],
      {
        env: {
          PATH: process.env.PATH ?? "",
          OLLAMA_HOST: server.url.origin,
          // The test's private config home: never the user's trust store.
          XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME ?? "",
        },
        stdout: "pipe",
        stderr: "pipe",
      },
    );
    const [stdout, stderr, code] = await Promise.all([
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
      child.exited,
    ]);
    expect(code).toBe(0);
    const statuses = stdout
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line))
      .filter((event) => event.type === "tool_result")
      .map((event) => event.status);
    expect(statuses).toEqual(["denied", "succeeded", "denied"]);
    expect(stderr).toContain(
      "refused by ask rule: echo * (a headless run has no one to ask)",
    );
    expect(stderr).toContain(
      "refused by ask rule: /notes.txt (a headless run has no one to ask)",
    );
    expect(await readFile(join(root, "notes.txt"), "utf8")).toBe("before");
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

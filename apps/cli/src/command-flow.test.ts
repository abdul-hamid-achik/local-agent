import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { SessionStore, trustRules } from "@local-agent/runtime";
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
          fileURLToPath(new URL("./main.ts", import.meta.url)),
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
          env: {
            PATH: process.env.PATH ?? "",
            OLLAMA_HOST: server.url.origin,
            // The test's private config home, never the developer's.
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

/** One headless run against a loopback Ollama that makes `calls` in order. */
async function headless(
  root: string,
  calls: { name: string; arguments: Record<string, unknown> }[],
  flags: string[],
) {
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
        ...flags,
      ],
      {
        env: {
          PATH: process.env.PATH ?? "",
          OLLAMA_HOST: server.url.origin,
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
    const results = stdout
      .trim()
      .split("\n")
      .filter(Boolean)
      .map((line) => JSON.parse(line))
      .filter((event) => event.type === "tool_result") as {
      status: string;
      output: string;
    }[];
    return { stderr, code, results };
  } finally {
    await server.stop(true);
  }
}

test("--sandbox-commands runs every command confined, refuses deny and ask rules, and never runs unconfined", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-sandbox-flow-"));
  try {
    // (Rule words are separated by spaces.)
    expect(process.execPath).not.toMatch(/\s/);
    await Bun.write(
      join(root, ".local-agent", "permissions.json"),
      JSON.stringify({
        version: 1,
        rules: [
          { effect: "ask", tool: "run_command", pattern: "git *" },
          { effect: "deny", tool: "run_command", pattern: "rm *" },
          {
            effect: "allow",
            tool: "run_command",
            pattern: `${process.execPath} -e *`,
          },
        ],
      }),
    );
    // Trusted in the test's private config home, never the user's.
    await trustRules(root);
    const run = await headless(
      root,
      [
        // An allow rule only spares the question: it runs confined too.
        {
          name: "run_command",
          arguments: {
            argv: [process.execPath, "-e", "await Bun.write('in.txt', 'x')"],
          },
        },
        { name: "run_command", arguments: { argv: ["git", "status"] } },
        { name: "run_command", arguments: { argv: ["rm", "in.txt"] } },
        // No rule: confined.
        { name: "run_command", arguments: { argv: ["/bin/ls"] } },
      ],
      ["--sandbox-commands"],
    );
    if (process.platform !== "darwin") {
      // Unattended, an unavailable sandbox stops the run before it starts.
      expect(run.code).toBe(1);
      expect(run.stderr).toContain(
        "--sandbox-commands: the command sandbox needs macOS (Seatbelt)",
      );
      expect(run.results).toEqual([]);
      expect(await Bun.file(join(root, "in.txt")).exists()).toBe(false);
      return;
    }
    // The ask rule's refusal says why; nothing else is said.
    expect(run.stderr).toBe(
      "refused by ask rule: git * (a headless run has no one to ask)\n",
    );
    expect(run.code).toBe(0);
    expect(run.results.map((result) => result.status)).toEqual([
      "succeeded",
      "denied",
      "denied",
      "succeeded",
    ]);
    for (const index of [0, 3])
      expect(JSON.parse(run.results[index]?.output ?? "{}")).toMatchObject({
        sandboxed: true,
        exitCode: 0,
      });
    expect(await Bun.file(join(root, "in.txt")).exists()).toBe(true);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("headless: a command that names a secret path is flagged on stderr, and no allow rule or sandbox flag lets it through", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-secret-flow-"));
  try {
    await writeFile(join(root, ".env"), "TOKEN=planted");
    await writeFile(join(root, "notes.txt"), "plain");
    await Bun.write(
      join(root, ".local-agent", "permissions.json"),
      JSON.stringify({
        version: 1,
        rules: [
          { effect: "allow", tool: "run_command", pattern: "/bin/cat *" },
        ],
      }),
    );
    await trustRules(root);
    const calls = [
      { name: "run_command", arguments: { argv: ["/bin/cat", "notes.txt"] } },
      { name: "run_command", arguments: { argv: ["/bin/cat", ".env"] } },
    ];
    const note = "names a path the workspace treats as secret: .env";
    // The trusted allow rule runs the plain one and says nothing; the
    // secret one is not waved through, and the flag is the only way in.
    const rule = await headless(root, calls, []);
    expect(rule.results.map((result) => result.status)).toEqual([
      "succeeded",
      "denied",
    ]);
    expect(rule.stderr).toContain(`/bin/cat .env\n${note}`);
    expect(rule.stderr.match(/names a path/g)).toHaveLength(1);
    // Pre-approved by the user's flag: it runs, and is not silent.
    const approved = await headless(root, calls, ["--approve-commands"]);
    expect(approved.results.map((result) => result.status)).toEqual([
      "succeeded",
      "succeeded",
    ]);
    expect(approved.stderr).toContain(note);
    // --sandbox-commands: the sandbox can read the workspace, so the secret
    // one is refused unattended, not run confined.
    if (process.platform === "darwin") {
      const sandboxed = await headless(root, calls, ["--sandbox-commands"]);
      expect(sandboxed.results.map((result) => result.status)).toEqual([
        "succeeded",
        "denied",
      ]);
      expect(sandboxed.stderr).toContain(note);
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("--sandbox-commands and --approve-commands cannot be combined", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-sandbox-flags-"));
  try {
    const run = await headless(
      root,
      [],
      ["--sandbox-commands", "--approve-commands"],
    );
    expect(run.code).toBe(1);
    expect(run.stderr).toContain(
      "Choose --approve-commands (commands run as you) or --sandbox-commands (confined), not both",
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

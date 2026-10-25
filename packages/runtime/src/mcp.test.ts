import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { createMcpHandler, McpServer } from "@modelcontextprotocol/server";
import { describe, expect, test } from "vitest";
import { z } from "zod";
import {
  connectMcp,
  connectMcpServers,
  defaultMcpConfigPath,
  gatewayTarget,
  McpServerConfigSchema,
  McpStartError,
  McpStoppedError,
  readMcpConfig,
  startupError,
} from "#runtime/mcp";

describe("MCP adapter boundary", () => {
  test("rejects remote endpoints unless explicitly enabled", async () => {
    await expect(
      connectMcp({
        name: "remote",
        transport: { kind: "http", url: "https://example.com/mcp" },
      }),
    ).rejects.toThrow("allowRemote");
  });

  test("rejects credentials and fragments in HTTP endpoints", async () => {
    await expect(
      connectMcp({
        name: "local",
        transport: { kind: "http", url: "http://user:secret@localhost/mcp#x" },
      }),
    ).rejects.toThrow("credentials or fragments");
  });

  test("a command that does not start says how to fix it", async () => {
    const reason = (transport: Record<string, unknown>) =>
      connectMcp({
        name: "x",
        transport: { kind: "stdio", ...transport },
      } as never).then(
        () => "connected",
        (error: Error) => error.message,
      );
    // Bare `bun` runs in the shell, not with the server's empty PATH.
    const bun = Bun.which("bun");
    expect(await reason({ command: "bun" })).toBe(
      bun
        ? `"bun" is not on its PATH (empty: its env sets none); use "command": "${bun}" or add its directory to env.PATH`
        : `"bun" is not on its PATH (empty: its env sets none); give its full path`,
    );
    expect(
      await reason({ command: "no-such-mcp-cmd", env: { PATH: "/usr/bin" } }),
    ).toBe(
      `"no-such-mcp-cmd" is not on the PATH its env sets; give its full path`,
    );
    expect(await reason({ command: "/no/such/mcp" })).toBe(
      `"/no/such/mcp" does not exist`,
    );
    expect(await reason({ command: "/bin/sh", cwd: "/no/such/dir" })).toBe(
      `its cwd "/no/such/dir" does not exist`,
    );
    // Every reason fits the 200 characters a failed server's line keeps.
    expect((await reason({ command: "bun" })).length).toBeLessThan(200);
  });

  test("a script whose #! interpreter is missing is not called missing from PATH", async () => {
    const reason = (transport: Record<string, unknown>) =>
      connectMcp({
        name: "x",
        transport: { kind: "stdio", ...transport },
      } as never).then(
        () => "connected",
        (error: Error) => error.message,
      );
    const dir = await mkdtemp(`${tmpdir()}/local-agent-shebang-`);
    try {
      await writeFile(`${dir}/server.sh`, "#!/no/such/interp\n", {
        mode: 0o755,
      });
      // Found on the PATH the server's env sets, so PATH is not the cause.
      expect(await reason({ command: "server.sh", env: { PATH: dir } })).toBe(
        `"server.sh" (${dir}/server.sh) did not start: the interpreter in "#!/no/such/interp" was not found`,
      );
      expect(await reason({ command: `${dir}/server.sh` })).toBe(
        `"${dir}/server.sh" did not start: the interpreter in "#!/no/such/interp" was not found`,
      );
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  test("validates strict JSON server configuration", () => {
    expect(() =>
      McpServerConfigSchema.parse({
        name: "fixture",
        transport: { kind: "stdio", command: "bun", extra: true },
      }),
    ).toThrow();
  });

  test("a server that exits before it answers is reported with the last line it wrote to stderr", async () => {
    const error = await connectMcp({
      name: "noisy",
      transport: {
        kind: "stdio",
        command: process.execPath,
        args: [
          "-e",
          'console.error("starting"); console.error("fatal: \\u001b[31mOLLAMA_URL is not set\\u001b[0m"); process.exit(1)',
        ],
        env: {},
      },
    }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).message).toMatch(
      /; it said: fatal: OLLAMA_URL is not set$/,
    );
  }, 15_000);

  test("a startup failure keeps what the server said whole in its one line", async () => {
    const said = `fatal: ${"x".repeat(153)}`;
    const startup = await connectMcpServers([
      {
        name: "noisy",
        transport: {
          kind: "stdio",
          command: process.execPath,
          args: [
            "-e",
            `console.error(${JSON.stringify(said)}); process.exit(1)`,
          ],
          env: {},
        },
      },
    ]);
    const error = startup.failed[0]?.error ?? "";
    expect(error.endsWith(`; it said: ${said}`)).toBe(true);
    expect(error.length).toBeLessThanOrEqual(200);

    const long = startupError(new McpStartError(`a ${"b".repeat(300)}`, said));
    expect(long).toHaveLength(200);
    expect(long).toMatch(/^a b+…; it said: fatal: x+$/);
    expect(startupError(new McpStartError("closed\n early", "it broke"))).toBe(
      "closed early; it said: it broke",
    );
    expect(startupError(new Error("x".repeat(300)))).toHaveLength(200);
    expect(
      startupError(new Error("\u001b]0;title\u0007bad\u0085\r\nreply")),
    ).toBe("bad reply");
  }, 15_000);

  test("a call to a server that has stopped is refused without being sent", async () => {
    // With no HOME, Bun keeps its transpiler cache under the cwd.
    const cwd = await mkdtemp(`${tmpdir()}/local-agent-mcp-stop-`);
    const adapter = await connectMcp({
      name: "stopping",
      transport: {
        kind: "stdio",
        command: process.execPath,
        args: [
          fileURLToPath(new URL("./fixtures/mcp_stop.ts", import.meta.url)),
        ],
        env: {},
        cwd,
      },
    }).catch(async (error: unknown) => {
      await rm(cwd, { recursive: true, force: true });
      throw error;
    });
    try {
      expect((await adapter.callTool("stopping__stop", {})).text).toBe(
        "stopping",
      );
      // The process exits on its own; its connection closes soon after.
      let error: unknown;
      for (let attempt = 0; attempt < 50; attempt++) {
        await Bun.sleep(100);
        error = await adapter.callTool("stopping__stop", {}).catch((e) => e);
        if (error instanceof McpStoppedError) break;
      }
      expect(error).toBeInstanceOf(McpStoppedError);
      expect((error as Error).message).toBe(
        "MCP server stopping has stopped, so the call was not sent. Do not call its tools again in this session; tell the user.",
      );
    } finally {
      await adapter.close();
      await rm(cwd, { recursive: true, force: true });
    }
  }, 15_000);

  test("connects to the official stdio server and projects only bounded text", async () => {
    process.env.MCP_SECRET = "parent-secret";
    const cwd = await mkdtemp(`${tmpdir()}/local-agent-mcp-`);
    const adapter = await connectMcp({
      name: "fixture",
      transport: {
        kind: "stdio",
        command: process.execPath,
        args: [
          fileURLToPath(new URL("./fixtures/mcp_stdio.ts", import.meta.url)),
        ],
        env: {},
        cwd,
      },
    });
    try {
      expect(adapter.tools).toEqual([
        expect.objectContaining({ name: "fixture__echo", readOnly: true }),
      ]);
      const result = await adapter.callTool("fixture__echo", { text: "hello" });
      expect(result.text).toBe("echo:hello:secret=missing:home=");
      expect(JSON.stringify(result)).not.toContain("do-not-project");
      expect(result).not.toHaveProperty("structuredContent");
      expect(result.domain).toBe("unknown");
      expect(result.evidence).toBe("unverified");
    } finally {
      delete process.env.MCP_SECRET;
      await adapter.close();
      await rm(cwd, { recursive: true, force: true });
    }
  }, 15_000);

  test("connects to an official SDK Streamable HTTP handler", async () => {
    const handler = createMcpHandler(
      () => {
        const server = new McpServer({
          name: "http-fixture",
          version: "1.0.0",
        });
        server.registerTool(
          "ping",
          { inputSchema: { value: z.string() } },
          async ({ value }) => ({
            content: [{ type: "text", text: `pong:${value}` }],
          }),
        );
        return server;
      },
      { responseMode: "json" },
    );
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      fetch: (request) => handler.fetch(request),
    });
    try {
      const adapter = await connectMcp({
        name: "httpfixture",
        transport: { kind: "http", url: `${server.url}mcp` },
      });
      try {
        const result = await adapter.callTool("httpfixture__ping", {
          value: "ok",
        });
        expect(result.text).toBe("pong:ok");
      } finally {
        await adapter.close();
      }
    } finally {
      server.stop(true);
      await handler.close();
    }
  }, 15_000);

  test("detects a gateway's call_tool and bounds each call by the server's timeout", async () => {
    const handler = createMcpHandler(
      () => {
        const server = new McpServer({ name: "hub", version: "1.0.0" });
        server.registerTool(
          "call_tool",
          {
            inputSchema: {
              server: z.string().optional(),
              tool: z.string(),
              arguments: z.record(z.string(), z.unknown()).optional(),
            },
          },
          async ({ tool }) => ({ content: [{ type: "text", text: tool }] }),
        );
        server.registerTool("slow", { inputSchema: {} }, async () => {
          await Bun.sleep(3_000);
          return { content: [{ type: "text" as const, text: "late" }] };
        });
        return server;
      },
      { responseMode: "json" },
    );
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      fetch: (request) => handler.fetch(request),
    });
    try {
      const adapter = await connectMcp({
        name: "hub",
        timeoutMs: 1_000,
        transport: { kind: "http", url: `${server.url}mcp` },
      });
      try {
        expect(adapter.gateway).toEqual({
          server: "hub",
          call: "hub__call_tool",
        });
        const started = Date.now();
        await expect(adapter.callTool("hub__slow", {})).rejects.toThrow();
        expect(Date.now() - started).toBeLessThan(2_500);
      } finally {
        await adapter.close();
      }
    } finally {
      server.stop(true);
      await handler.close();
    }
  }, 15_000);

  test("a timeout outside 1 s to 10 min is a configuration error", () => {
    for (const timeoutMs of [999, 600_001, 1.5])
      expect(() =>
        McpServerConfigSchema.parse({
          name: "x",
          timeoutMs,
          transport: { kind: "stdio", command: "bun" },
        }),
      ).toThrow();
  });
});

describe("gatewayTarget", () => {
  // The same rules as mcphub's call_tool: a missing server splits the tool
  // at its first `__`, a given server strips its own prefix from the tool.
  test("every spelling of one downstream tool has one identity", () => {
    const status = {
      target: "hub__cortex__status",
      inner: "cortex__status",
      aliases: ["hub__cortex__cortex_status"],
    };
    for (const args of [
      { server: "cortex", tool: "status" },
      { tool: "cortex__status" },
      { server: "cortex", tool: "cortex__status" },
      { server: "", tool: "cortex__status", arguments: { deep: true } },
    ])
      expect(gatewayTarget("hub", JSON.stringify(args))).toEqual(status);
    // A self-prefixed name keeps its spelling. mcphub may also run it as
    // `S_TOOL`, and shows it as the short name: both are aliases.
    expect(
      gatewayTarget(
        "hub",
        JSON.stringify({ tool: "hitspec__hitspec_search_web" }),
      ),
    ).toEqual({
      target: "hub__hitspec__hitspec_search_web",
      inner: "hitspec__hitspec_search_web",
      aliases: [
        "hub__hitspec__hitspec_hitspec_search_web",
        "hub__hitspec__search_web",
      ],
    });
    // A null server is no server, as Go decodes it.
    expect(
      gatewayTarget(
        "hub",
        JSON.stringify({ server: null, tool: "cortex__status" }),
      )?.target,
    ).toBe("hub__cortex__status");
  });

  test("arguments that name no server and tool have no identity", () => {
    for (const json of [
      "not json",
      "null",
      "[]",
      JSON.stringify({ tool: "status" }),
      JSON.stringify({ tool: 5 }),
      JSON.stringify({ server: "cortex" }),
      JSON.stringify({ tool: "__status" }),
      JSON.stringify({ tool: "cortex__" }),
      JSON.stringify({ server: "a b", tool: "status" }),
      JSON.stringify({ server: "cortex", tool: "st/atus" }),
      // Go reads a non-string server as an error, and these keys as
      // `server` or `tool`: the identity would not be what runs.
      JSON.stringify({ server: 5, tool: "cortex__status" }),
      JSON.stringify({ Server: "monitor", tool: "cortex__status" }),
      JSON.stringify({ tool: "cortex__status", TOOL: "monitor__kill" }),
      JSON.stringify({ "\u017fERVER": "monitor", tool: "cortex__status" }),
      // `a__b` + `c` and `a` + `b__c` would share one identity.
      JSON.stringify({ server: "a__b", tool: "c" }),
    ])
      expect(gatewayTarget("hub", json)).toBeUndefined();
  });
});

describe("MCP startup", () => {
  const fixture = (name: string, script: string, cwd: string) => ({
    name,
    transport: {
      kind: "stdio" as const,
      command: process.execPath,
      args: [script],
      env: {},
      cwd,
    },
  });
  const echo = fileURLToPath(
    new URL("./fixtures/mcp_stdio.ts", import.meta.url),
  );

  test("servers connect together; one that fails is reported, not fatal", async () => {
    const cwd = await mkdtemp(`${tmpdir()}/local-agent-mcp-startup-`);
    try {
      const startup = await connectMcpServers([
        fixture("missing", `${cwd}/missing.ts`, cwd),
        fixture("echo", echo, cwd),
      ]);
      try {
        expect(startup.connected.map((server) => server.name)).toEqual([
          "echo",
        ]);
        expect(startup.failed).toEqual([
          { name: "missing", error: expect.any(String) },
        ]);
        expect(startup.failed[0]?.error).not.toMatch(/\n/);
      } finally {
        await Promise.all(
          startup.connected.map(({ connection }) => connection.close()),
        );
      }
      await expect(
        connectMcpServers([fixture("echo", echo, cwd)], AbortSignal.abort()),
      ).rejects.toThrow();
    } finally {
      await rm(cwd, { recursive: true, force: true });
    }
  }, 15_000);

  test("an abort stops a server that never answers, without waiting for it", async () => {
    const cwd = await mkdtemp(`${tmpdir()}/local-agent-mcp-silent-`);
    try {
      const silent = {
        name: "silent",
        transport: {
          kind: "stdio" as const,
          command: process.execPath,
          args: ["-e", "setInterval(() => {}, 1000)"],
          env: {},
          cwd,
        },
      };
      const started = Date.now();
      await expect(
        connectMcpServers([silent], AbortSignal.timeout(200)),
      ).rejects.toThrow();
      expect(Date.now() - started).toBeLessThan(3_000);
    } finally {
      await rm(cwd, { recursive: true, force: true });
    }
  }, 15_000);

  test("a config file is an array of distinct servers", async () => {
    const cwd = await mkdtemp(`${tmpdir()}/local-agent-mcp-config-`);
    try {
      const path = `${cwd}/mcp.json`;
      const server = fixture("echo", echo, cwd);
      await writeFile(path, JSON.stringify([server]));
      expect(await readMcpConfig(path)).toEqual([server]);
      await writeFile(path, JSON.stringify([server, server]));
      await expect(readMcpConfig(path)).rejects.toThrow(
        "Duplicate MCP server names",
      );
      await writeFile(path, JSON.stringify(server));
      await expect(readMcpConfig(path)).rejects.toThrow();
      await writeFile(path, " ".repeat(1_000_001));
      await expect(readMcpConfig(path)).rejects.toThrow("exceeds 1 MB");
      // A FIFO named mcp.json is refused, not waited on forever.
      const fifo = `${cwd}/fifo.json`;
      Bun.spawnSync(["mkfifo", fifo]);
      await expect(readMcpConfig(fifo)).rejects.toThrow("not a regular file");
    } finally {
      await rm(cwd, { recursive: true, force: true });
    }
  });

  test("the default file is the user's, never the workspace's", () => {
    expect(defaultMcpConfigPath({ XDG_CONFIG_HOME: "/x" })).toBe(
      "/x/local-agent/mcp.json",
    );
    // A relative XDG_CONFIG_HOME would resolve against the workspace.
    expect(defaultMcpConfigPath({ XDG_CONFIG_HOME: ".config" })).toMatch(
      /^\/.*\/\.config\/local-agent\/mcp\.json$/,
    );
  });
});

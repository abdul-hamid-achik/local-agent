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
  readMcpConfig,
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

  test("validates strict JSON server configuration", () => {
    expect(() =>
      McpServerConfigSchema.parse({
        name: "fixture",
        transport: { kind: "stdio", command: "bun", extra: true },
      }),
    ).toThrow();
  });

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
    // A self-prefixed name keeps its spelling; the short one is its alias.
    expect(
      gatewayTarget(
        "hub",
        JSON.stringify({ tool: "hitspec__hitspec_search_web" }),
      ),
    ).toEqual({
      target: "hub__hitspec__hitspec_search_web",
      inner: "hitspec__hitspec_search_web",
      aliases: ["hub__hitspec__search_web"],
    });
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

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { createMcpHandler, McpServer } from "@modelcontextprotocol/server";
import { describe, expect, test } from "vitest";
import { z } from "zod";
import { connectMcp, McpServerConfigSchema } from "#runtime/mcp";

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
});

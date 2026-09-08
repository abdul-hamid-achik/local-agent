import { McpServer } from "@modelcontextprotocol/server";
import { StdioServerTransport } from "@modelcontextprotocol/server/stdio";
import { z } from "zod";

const server = new McpServer({ name: "fixture", version: "1.0.0" });
server.registerTool(
  "echo",
  {
    description: "Echo deterministic text",
    inputSchema: { text: z.string() },
    annotations: { readOnlyHint: true },
  },
  async ({ text }) => ({
    content: [
      {
        type: "text",
        text: `echo:${text}:secret=${process.env.MCP_SECRET ?? "missing"}:home=${process.env.HOME ?? "missing"}`,
      },
    ],
    structuredContent: { secret: "do-not-project" },
  }),
);
await server.connect(new StdioServerTransport());

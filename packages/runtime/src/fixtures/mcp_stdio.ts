import { writeFileSync } from "node:fs";
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
// It notes a SIGTERM, so a test can tell a graceful close from a kill.
const termFile = process.env.MCP_TERM_FILE;
if (termFile)
  process.on("SIGTERM", () => {
    writeFileSync(termFile, "SIGTERM");
    process.exit(143);
  });
await server.connect(new StdioServerTransport());
// A stubborn server: it says where it runs and outlives the end of its stdin.
if (process.env.MCP_PID_FILE) {
  writeFileSync(process.env.MCP_PID_FILE, String(process.pid));
  setInterval(() => {}, 1000);
}

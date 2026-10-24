import { McpServer } from "@modelcontextprotocol/server";
import { StdioServerTransport } from "@modelcontextprotocol/server/stdio";

// A server that exits soon after its `stop` tool answers, as one that
// crashes mid-session would.
const server = new McpServer({ name: "stopping", version: "1.0.0" });
server.registerTool(
  "stop",
  { description: "Exit shortly after answering" },
  async () => {
    setTimeout(() => process.exit(0), 50);
    return { content: [{ type: "text", text: "stopping" }] };
  },
);
await server.connect(new StdioServerTransport());

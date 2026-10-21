import { parseArgs } from "node:util";
import {
  connectMcp,
  inspectRecovery,
  type McpConnection,
  McpServerConfigSchema,
  resolveProvider,
  runAgent,
  type SessionContext,
  SessionStore,
  Workspace,
} from "@local-agent/runtime";
import { terminalText } from "@local-agent/vue-tui/text";

async function inspectMcpStatus(path: string, signal: AbortSignal) {
  const file = Bun.file(path);
  if (file.size > 1_000_000) throw new Error("MCP config exceeds 1 MB");
  const configs = McpServerConfigSchema.array()
    .max(16)
    .parse(await file.json());
  if (new Set(configs.map((config) => config.name)).size !== configs.length)
    throw new Error("Duplicate MCP server names");
  const servers = [];
  for (const config of configs) {
    signal.throwIfAborted();
    try {
      const connection = await connectMcp(config, signal);
      const tools = connection.tools.map((tool) => tool.name);
      await connection.close();
      servers.push({
        name: config.name,
        transport: config.transport.kind,
        status: "connected",
        tools,
      });
    } catch (error) {
      if (signal.aborted) throw error;
      servers.push({
        name: config.name,
        transport: config.transport.kind,
        status: "unavailable",
        tools: [],
      });
    }
  }
  return { servers };
}

try {
  const { values } = parseArgs({
    args: Bun.argv.slice(2),
    options: {
      help: { type: "boolean", short: "h" },
      model: { type: "string" },
      prompt: { type: "string", short: "p" },
      mode: { type: "string", default: "fullscreen" },
      ui: { type: "string", default: "vue" },
      json: { type: "boolean" },
      workspace: { type: "string", default: process.cwd() },
      intent: { type: "string", default: "build" },
      "approve-edits": { type: "boolean", default: false },
      "approve-commands": { type: "boolean", default: false },
      "approve-mcp": { type: "boolean", default: false },
      "mcp-config": { type: "string" },
      "session-db": { type: "string" },
      resume: { type: "string" },
      sessions: { type: "boolean" },
      "session-info": { type: "string" },
      "recovery-info": { type: "string" },
      "mcp-status": { type: "boolean", default: false },
    },
  });
  if (values.help) {
    console.log(`Bun Local Agent — experimental agent

bun dev --model MODEL [--mode fullscreen|inline|headless] [--ui vue|tuimark] [-p PROMPT] [--json]
  --ui vue|tuimark     Interactive client: vue (default) or the opt-in Tuimark client (fullscreen only)
  --workspace PATH     Workspace root (default: current directory)
  --intent plan|build  Plan forbids edits and commands; build asks before acting
  --approve-edits      Allow existing-file edits in headless mode
  --approve-commands   Allow command execution in headless mode (user permissions)
  --mcp-config PATH    Load an explicit trusted JSON array of MCP servers
  --approve-mcp        Allow configured MCP tool calls in headless mode
  --session-db PATH    Opt into experimental durable sessions in a separate database
  --resume HANDLE      Resume a completed session from --session-db
  --sessions           List sessions in this workspace without contacting a model
  --session-info ID    Inspect saved state and recovery hazards without running tools
  --recovery-info ID   Inspect recovery review tokens without transcript or raw receipts
  --mcp-status         Check only the explicitly supplied MCP config and list tools

Chalupa profiles come from the environment supplied by chalupa exec.
Interrupted tool outcomes require inspection; they are never automatically replayed.`);
  } else if (values["mcp-status"]) {
    if (!values["mcp-config"])
      throw new Error("--mcp-status requires --mcp-config PATH");
    const controller = new AbortController();
    const stop = () => controller.abort();
    process.once("SIGINT", stop);
    process.once("SIGTERM", stop);
    try {
      const report = await inspectMcpStatus(
        values["mcp-config"],
        controller.signal,
      );
      console.log(JSON.stringify(report));
      if (report.servers.some((server) => server.status === "unavailable"))
        process.exitCode = 1;
    } finally {
      process.off("SIGINT", stop);
      process.off("SIGTERM", stop);
    }
  } else {
    if (!["fullscreen", "inline", "headless"].includes(values.mode))
      throw new Error("Unknown presentation mode");
    if (values.ui !== "vue" && values.ui !== "tuimark")
      throw new Error("Unknown UI client; use --ui vue or --ui tuimark");
    if (values.intent !== "plan" && values.intent !== "build")
      throw new Error("Unknown agent intent");
    if (
      [
        values.resume,
        values.sessions,
        values["session-info"],
        values["recovery-info"],
      ].filter(Boolean).length > 1
    )
      throw new Error("Choose one session operation");
    if (
      (values.resume ||
        values.sessions ||
        values["session-info"] ||
        values["recovery-info"]) &&
      !values["session-db"]
    )
      throw new Error("Session operations require --session-db");
    const inspect =
      values.sessions ||
      values["session-info"] !== undefined ||
      values["recovery-info"] !== undefined;
    const prompt = values.prompt ?? "";
    const headless =
      values.mode === "headless" || values.prompt !== undefined || values.json;
    if (!inspect) {
      if (values.ui === "tuimark" && (headless || values.mode !== "fullscreen"))
        throw new Error(
          "--ui tuimark is fullscreen only; use it without --prompt, --json or --mode inline|headless",
        );
      if (headless && !prompt.trim())
        throw new Error("Headless mode requires --prompt");
      if (
        !headless &&
        (values["approve-edits"] ||
          values["approve-commands"] ||
          values["approve-mcp"])
      )
        throw new Error(
          "--approve-edits, --approve-commands and --approve-mcp are only available in headless mode",
        );
      if (!headless && (!process.stdin.isTTY || !process.stdout.isTTY))
        throw new Error(
          "Interactive modes require a terminal; use --mode headless --prompt",
        );
    }
    using workspace = await Workspace.create(values.workspace);
    const store = values["session-db"]
      ? await SessionStore.open(values["session-db"])
      : undefined;
    try {
      if (inspect) {
        if (!store) throw new Error("Session database is required");
        if (values["recovery-info"] !== undefined) {
          console.log(
            JSON.stringify(
              await inspectRecovery(
                store,
                values["recovery-info"],
                workspace.root,
              ),
            ),
          );
        } else if (values.sessions) {
          const sessions = await store.listSessions(workspace.root);
          console.log(
            JSON.stringify(
              sessions.map(({ publicId, revision, messages }) => ({
                id: publicId,
                revision,
                messages: messages.length,
              })),
            ),
          );
        } else {
          const state = await store.loadSession(
            values["session-info"] ?? "",
            workspace.root,
          );
          const release = await store.acquireLease(state.id, workspace.root);
          try {
            const current = await store.loadSession(
              state.publicId,
              workspace.root,
            );
            console.log(
              JSON.stringify({
                session: current,
                hazards: store.listHazards(current.id, current.workspaceId),
              }),
            );
          } finally {
            await release();
          }
        }
      } else {
        const provider = resolveProvider(process.env, values.model);
        const session: SessionContext | undefined = store
          ? {
              store,
              state: values.resume
                ? await store.loadSession(values.resume, workspace.root)
                : await store.createSession(workspace.root),
            }
          : undefined;
        const controller = new AbortController();
        const stop = () => controller.abort();
        process.once("SIGINT", stop);
        process.once("SIGTERM", stop);
        const mcp: McpConnection[] = [];
        try {
          if (values["mcp-config"]) {
            const file = Bun.file(values["mcp-config"]);
            if (file.size > 1_000_000)
              throw new Error("MCP config exceeds 1 MB");
            const configs = McpServerConfigSchema.array()
              .max(16)
              .parse(await file.json());
            if (
              new Set(configs.map((config) => config.name)).size !==
              configs.length
            )
              throw new Error("Duplicate MCP server names");
            for (const config of configs) {
              controller.signal.throwIfAborted();
              mcp.push(await connectMcp(config, controller.signal));
            }
          }
          if (headless) {
            if (session) {
              if (values.json)
                console.log(
                  JSON.stringify({
                    type: "session",
                    id: session.state.publicId,
                  }),
                );
              else process.stderr.write(`Session: ${session.state.publicId}\n`);
            }
            // Native terminal rendering stays behind the interactive dynamic import.
            for await (const event of runAgent(
              provider,
              [
                ...(session?.state.messages ?? []),
                { role: "user", content: prompt },
              ],
              {
                workspace,
                intent: values.intent,
                signal: controller.signal,
                session,
                mcp,
                approve: async (proposal) =>
                  proposal.kind === "edit"
                    ? values["approve-edits"]
                    : proposal.kind === "command"
                      ? values["approve-commands"]
                      : values["approve-mcp"],
              },
            )) {
              if (values.json) console.log(JSON.stringify(event));
              else if (event.type === "text")
                process.stdout.write(terminalText(event.text));
              else if (event.type === "tool_result")
                process.stderr.write(
                  `${terminalText(event.name)}: ${event.status}\n`,
                );
            }
            if (!values.json) process.stdout.write("\n");
          } else if (values.ui === "tuimark") {
            // Tuimark owns the terminal; the Vue renderer stays unloaded.
            const { runTuimark } = await import("#cli/tuimark/launch");
            await runTuimark(
              provider,
              controller.signal,
              workspace,
              values.intent,
              session,
              mcp,
            );
          } else {
            const { runTerminal } = await import("#cli/terminal");
            await runTerminal(
              provider,
              values.mode as "fullscreen" | "inline",
              controller.signal,
              workspace,
              values.intent,
              session,
              mcp,
            );
          }
        } finally {
          await Promise.allSettled(mcp.map((connection) => connection.close()));
          process.off("SIGINT", stop);
          process.off("SIGTERM", stop);
        }
      }
    } finally {
      store?.close();
    }
  }
} catch (error) {
  console.error(
    terminalText(
      error instanceof Error ? error.message : "Unable to run Local Agent",
    ),
  );
  process.exitCode = 1;
}

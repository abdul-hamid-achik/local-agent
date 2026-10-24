import { parseArgs } from "node:util";
import {
  admitOllamaModel,
  admitOpenAILocalModel,
  connectMcp,
  DEFAULT_OLLAMA_MODEL,
  evaluate,
  inspectRecovery,
  loadPermissionFile,
  type McpConnection,
  McpServerConfigSchema,
  permissionRequest,
  resolveProvider,
  runAgent,
  type SessionContext,
  SessionStore,
  Workspace,
} from "@local-agent/runtime";
import { terminalText } from "@local-agent/vue-tui/text";
import { interactiveClient, parseUi } from "#cli/ui-flag";

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
      version: { type: "boolean" },
      model: { type: "string" },
      think: { type: "string" },
      context: { type: "string" },
      prompt: { type: "string", short: "p" },
      mode: { type: "string", default: "fullscreen" },
      ui: { type: "string" },
      theme: { type: "string" },
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
  const ui = parseUi(values.ui);
  if (values.version) {
    const pkg = (await import("../package.json")) as { version?: string };
    console.log(`local-agent ${pkg.version ?? "0.0.0"}`);
    process.exit(0);
  }
  if (values.help) {
    console.log(`Bun Local Agent — experimental agent

bun dev [--model MODEL (default: ${DEFAULT_OLLAMA_MODEL})] [--mode fullscreen|inline|headless] [--ui vue|tuimark] [-p PROMPT] [--json]
  --version            Print the CLI version and exit
  --model MODEL        Ollama model (default: ${DEFAULT_OLLAMA_MODEL}, or LOCAL_AGENT_MODEL);
                       Chalupa keeps the model of its profile
  --think on|off       Turn the model's thinking on or off (or LOCAL_AGENT_THINK=1|0);
                       unset keeps the model's own default
  --context N          Context size in tokens for local Ollama (default 16384, or
                       LOCAL_AGENT_CONTEXT; 32768 and 32k both work); it cannot exceed
                       the model's native context. Chalupa negotiates its own
  --ui vue|tuimark     Interactive client: tuimark (default on macOS and Linux) or vue
                       (default on Windows). Tuimark inline is opt-in: --ui tuimark
                       --mode inline prints finished blocks into the terminal's own
                       scrollback; a bare --mode inline still uses vue. It runs the
                       tuimark binary from TUIMARK_BINARY, or downloads the release
  --theme NAME         Tuimark palette at launch: dark, light or auto, or a
                       named theme from the client's ui/themes folder (the
                       bundled ones: midnight, sepia)
  --workspace PATH     Workspace root (default: current directory)
  --intent plan|build|chat  Plan forbids edits and commands; chat is conversational (research
                       only); build asks before acting
  --approve-edits      Allow file edits and writes (create or replace) in headless mode
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
    if (
      values.intent !== "plan" &&
      values.intent !== "build" &&
      values.intent !== "chat"
    )
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
      if (values.ui === "tuimark" && headless)
        throw new Error(
          "--ui tuimark needs an interactive terminal; use it without --prompt, --json or --mode headless",
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
        const provider = resolveProvider(process.env, values.model, {
          context: values.context,
          think: values.think,
        });
        if (!headless) {
          // An interactive client must not open its screen for a model that is
          // missing, or a --context beyond its native one: say so in one line.
          // (Headless runs report the same line from the agent's own admission.)
          const admission =
            provider.type === "openai_local"
              ? await admitOpenAILocalModel(
                  provider,
                  AbortSignal.timeout(15_000),
                )
              : await admitOllamaModel(provider, AbortSignal.timeout(15_000));
          if (admission.status === "rejected")
            throw new Error(admission.message);
        }
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
        const mcpNames: string[] = [];
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
              mcpNames.push(config.name);
            }
          }
          let client = headless
            ? "vue"
            : interactiveClient(
                ui,
                values.mode as "fullscreen" | "inline",
                values.ui !== undefined,
              );
          if (values.theme !== undefined && client !== "tuimark")
            throw new Error(
              "--theme needs the Tuimark client; use --ui tuimark",
            );
          let bin: string | undefined;
          if (client === "tuimark" && values.ui === undefined) {
            // The default client must not fail a run that never asked for it.
            const { findBinary } = await import("#cli/tuimark/launch");
            const found = await findBinary();
            if ("bin" in found) bin = found.bin;
            else {
              process.stderr.write(
                `No tuimark binary (${terminalText(found.error)}); using the Vue client. Set TUIMARK_BINARY to a tuimark, or pass --ui vue to silence this.\n`,
              );
              client = "vue";
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
            // An untrusted or broken rule file is named once, not per call.
            let permissionWarning: string | undefined;
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
                // Headless precedence: the workspace's permission file is
                // the user's explicit, durable intent, so it outranks the
                // --approve-* flags in BOTH directions — a trusted allow
                // rule approves even with the flag off, a deny rule refuses
                // even with the flag on. Everything else asks the flags.
                // Allow rules this user never trusted (a cloned repo's) are
                // left out by loadPermissionFile.
                deniedRead: async (request) => {
                  const { rules } = await loadPermissionFile(workspace.root);
                  const verdict = evaluate(rules, request);
                  return verdict.effect === "deny"
                    ? (verdict.rule?.pattern ?? "")
                    : undefined;
                },
                approve: async (proposal) => {
                  const { rules, warning } = await loadPermissionFile(
                    workspace.root,
                  );
                  if (warning && warning !== permissionWarning) {
                    permissionWarning = warning;
                    process.stderr.write(`warning: ${warning}\n`);
                  }
                  const verdict = evaluate(rules, permissionRequest(proposal));
                  if (verdict.effect !== "ask")
                    return verdict.effect === "allow";
                  return proposal.kind === "edit" || proposal.kind === "write"
                    ? values["approve-edits"]
                    : proposal.kind === "command"
                      ? values["approve-commands"]
                      : values["approve-mcp"];
                },
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
          } else if (client === "tuimark") {
            // Tuimark owns the terminal; the Vue renderer stays unloaded.
            const { runTuimark } = await import("#cli/tuimark/launch");
            await runTuimark(
              provider,
              controller.signal,
              workspace,
              values.intent,
              session,
              mcp,
              values.mode as "fullscreen" | "inline",
              values.theme ?? "auto",
              {
                mcpNames,
                ...(bin ? { resolve: async () => bin } : {}),
              },
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

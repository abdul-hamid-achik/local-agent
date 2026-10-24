import { parseArgs } from "node:util";
import {
  admitOllamaModel,
  admitOpenAILocalModel,
  connectMcpServers,
  DEFAULT_OLLAMA_MODEL,
  defaultMcpConfigPath,
  inspectRecovery,
  type McpConnection,
  type McpServerConfig,
  PLANNED_CONTEXT_MAX,
  planContext,
  readMcpConfig,
  resolveProvider,
  runAgent,
  type SessionContext,
  SessionStore,
  sessionProblem,
  Workspace,
  workspaceRules,
} from "@local-agent/runtime";
import { terminalText } from "@local-agent/vue-tui/text";
import { terminalTrustIO, trustRulesCommand } from "#cli/trust-rules";
import { interactiveClient, parseUi } from "#cli/ui-flag";

async function inspectMcpStatus(path: string, signal: AbortSignal) {
  const configs = await readMcpConfig(path);
  const startup = await connectMcpServers(configs, signal);
  await Promise.allSettled(
    startup.connected.map(({ connection }) => connection.close()),
  );
  return {
    servers: configs.map((config) => {
      const connected = startup.connected.find(
        (server) => server.name === config.name,
      );
      return {
        name: config.name,
        transport: config.transport.kind,
        status: connected ? "connected" : "unavailable",
        tools: connected?.connection.tools.map((tool) => tool.name) ?? [],
      };
    }),
  };
}

/**
 * The MCP file a run loads: --mcp-config (explicit: a server that fails is
 * the run's failure, except in the Tuimark client, which shows it), else
 * the user's own file when it exists (a failure is a warning), else none.
 * --no-mcp skips the user's file.
 */
async function mcpSource(values: {
  "mcp-config"?: string;
  "no-mcp"?: boolean;
}): Promise<{ path: string; explicit: boolean } | undefined> {
  if (values["mcp-config"] && values["no-mcp"])
    throw new Error("Choose --mcp-config PATH or --no-mcp, not both");
  if (values["mcp-config"])
    return { path: values["mcp-config"], explicit: true };
  if (values["no-mcp"]) return undefined;
  const path = defaultMcpConfigPath();
  return (await Bun.file(path).exists())
    ? { path, explicit: false }
    : undefined;
}

const unavailable = (failed: { name: string; error: string }) =>
  `MCP server ${failed.name} unavailable: ${failed.error}`;

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
      "no-mcp": { type: "boolean", default: false },
      "session-db": { type: "string" },
      resume: { type: "string" },
      sessions: { type: "boolean" },
      "session-info": { type: "string" },
      "recovery-info": { type: "string" },
      "mcp-status": { type: "boolean", default: false },
      "trust-rules": { type: "boolean", default: false },
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
  --context N          Context size in tokens for local Ollama (or LOCAL_AGENT_CONTEXT;
                       32768 and 32k both work); it cannot exceed the model's native
                       context. Default: planned from the model's KV cache and this
                       machine's memory, at most ${PLANNED_CONTEXT_MAX}. Chalupa negotiates its own
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
  --mcp-config PATH    Load an explicit trusted JSON array of MCP servers (default:
                       ${defaultMcpConfigPath()} when it exists)
  --no-mcp             Start without the default MCP servers
  --approve-mcp        Allow configured MCP tool calls in headless mode
  --session-db PATH    Opt into experimental durable sessions in a separate database
  --resume HANDLE      Resume a completed session from --session-db
  --sessions           List sessions in this workspace without contacting a model
  --session-info ID    Inspect saved state and recovery hazards without running tools
  --recovery-info ID   Inspect recovery review tokens without transcript or raw receipts
  --mcp-status         Check the MCP config (--mcp-config or the default) and list tools
  --trust-rules        List the workspace's untrusted allow rules and trust them
                       after you type yes (the terminal form of /permissions trust)

Chalupa profiles come from the environment supplied by chalupa exec.
Interrupted tool outcomes require inspection; they are never automatically replayed.`);
  } else if (values["trust-rules"]) {
    using workspace = await Workspace.create(values.workspace);
    process.exitCode = await trustRulesCommand(
      workspace.root,
      terminalTrustIO(),
    );
  } else if (values["mcp-status"]) {
    const source = await mcpSource(values);
    if (!source)
      throw new Error(
        `--mcp-status needs --mcp-config PATH or ${defaultMcpConfigPath()}`,
      );
    const controller = new AbortController();
    const stop = () => controller.abort();
    process.once("SIGINT", stop);
    process.once("SIGTERM", stop);
    try {
      const report = await inspectMcpStatus(source.path, controller.signal);
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
        const resolved = resolveProvider(process.env, values.model, {
          context: values.context,
          think: values.think,
        });
        // A resumed session that cannot continue is refused before the
        // provider is asked anything.
        const resumed =
          store && values.resume
            ? await store.loadSession(values.resume, workspace.root)
            : undefined;
        const problem = store && resumed && sessionProblem(store, resumed);
        if (problem) throw new Error(problem);
        // Without --context, the size comes from the model and this
        // machine's memory (planContext); a failed look leaves 16384.
        const provider = await planContext(
          resolved,
          AbortSignal.timeout(15_000),
        );
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
              state: resumed ?? (await store.createSession(workspace.root)),
            }
          : undefined;
        const controller = new AbortController();
        const stop = () => controller.abort();
        process.once("SIGINT", stop);
        process.once("SIGTERM", stop);
        // Filled as servers connect (the Tuimark client opens first and
        // shows them arriving); every one is closed on the way out.
        const mcp: McpConnection[] = [];
        const mcpNames: string[] = [];
        const mcpAbort = new AbortController();
        let mcpReady: Promise<{ name: string; error: string }[]> | undefined;
        try {
          const source = await mcpSource(values);
          let configs: McpServerConfig[] = [];
          if (source)
            try {
              configs = await readMcpConfig(source.path);
            } catch (error) {
              if (source.explicit) throw error;
              process.stderr.write(
                `warning: ignoring ${terminalText(source.path)}: ${terminalText(error instanceof Error ? error.message : String(error))}\n`,
              );
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
          if (configs.length > 0) {
            const started = connectMcpServers(
              configs,
              AbortSignal.any([controller.signal, mcpAbort.signal]),
            ).then((startup) => {
              for (const { name, connection } of startup.connected) {
                mcp.push(connection);
                mcpNames.push(name);
              }
              return startup.failed;
            });
            if (client === "tuimark" && !headless)
              mcpReady = started.catch(() => []);
            else {
              const failed = await started;
              if (failed[0] && source?.explicit)
                throw new Error(failed.map(unavailable).join("; "));
              for (const server of failed)
                process.stderr.write(
                  `warning: ${terminalText(unavailable(server))}\n`,
                );
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
            const fileRules = workspaceRules(workspace.root, (warning) =>
              process.stderr.write(`warning: ${terminalText(warning)}\n`),
            );
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
                deniedRead: fileRules.deniedRead,
                // A run reloads its history from the session, so a long
                // turn may shorten it (and says so below).
                compact: true,
                approve: async (proposal) => {
                  const verdict = await fileRules.verdict(proposal);
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
              else if (event.type === "compacted")
                process.stderr.write(
                  `context compacted: ${event.elided} earlier messages shortened (about ${event.tokensBefore} → ${event.tokensAfter} tokens)\n`,
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
                ...(mcpReady ? { mcpReady } : {}),
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
          // A quit while servers still connect: stop them, then close the
          // ones that made it.
          mcpAbort.abort();
          await mcpReady;
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

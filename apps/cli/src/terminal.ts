import {
  type ApprovalRequest,
  type McpConnection,
  type Message,
  type Provider,
  runAgent,
  type SessionContext,
  type Workspace,
  workspaceRules,
} from "@local-agent/runtime";
import {
  Approval,
  Box,
  Composer,
  createApp,
  Markdown,
  onKeyDown,
  PanelLayout,
  type Presentation,
  ScrollBox,
  SyntaxStyle,
  Text,
  terminalOptions,
  terminalText,
  useExit,
} from "@local-agent/vue-tui";
import { defineComponent, h, nextTick, onUnmounted, ref } from "vue";
import { approvalPreview, messageText, tokensPerSecond } from "#cli/preview";

const messageSyntax = SyntaxStyle.fromStyles({
  default: { fg: "#d8dee9" },
  "markup.heading": { fg: "#88c0d0", bold: true },
  "markup.bold": { bold: true },
  "markup.italic": { italic: true },
  "markup.raw": { fg: "#a3be8c" },
});

/**
 * A command is one line whose first word is a name (`/mode plan`): a pasted
 * block, or a line that starts with a path (`/tmp/a.log is empty`), is a
 * message, as in the Tuimark client.
 */
function isCommand(text: string): boolean {
  return (
    !text.includes("\n") &&
    (text === "/" || /^\/[^\s/]+$/.test(text.split(/\s/, 1)[0] ?? ""))
  );
}

export function createTerminalRoot(
  provider: Provider,
  mode: Presentation,
  signal: AbortSignal,
  workspace: Workspace,
  intent: "plan" | "build" | "chat",
  session?: SessionContext,
  mcp: readonly McpConnection[] = [],
) {
  return defineComponent({
    setup() {
      const draft = ref("");
      const busy = ref(false);
      const idleStatus =
        "Enter send · Esc cancel · /mode plan|build|chat · Ctrl+C exit";
      const status = ref(idleStatus);
      const history = ref<Message[]>(session?.state.messages.slice() ?? []);
      const live = ref("");
      // The model is reasoning before it answers (never added to the history).
      const thinking = ref(false);
      const selectedIntent = ref(intent);
      const exit = useExit();
      let turn: AbortController | undefined;
      const pending = ref<ApprovalRequest>();
      let decide: ((allowed: boolean) => void) | undefined;
      // The workspace's permission file applies here as in every client:
      // deny refuses before any prompt, a trusted allow skips it.
      const fileRules = workspaceRules(workspace.root, (warning) => {
        status.value = terminalText(warning);
      });
      async function approve(
        proposal: ApprovalRequest,
        approvalSignal: AbortSignal,
      ): Promise<boolean> {
        approvalSignal.throwIfAborted();
        const verdict = await fileRules.verdict(proposal);
        approvalSignal.throwIfAborted();
        if (verdict.effect !== "ask") {
          status.value = terminalText(
            `${verdict.effect === "deny" ? "Denied" : "Allowed"} by rule: ${verdict.rule?.pattern ?? ""}`,
          );
          if (mode === "inline") process.stdout.write(`\n${status.value}\n`);
          return verdict.effect === "allow";
        }
        const preview = approvalPreview(proposal, workspace.root);
        if (mode === "inline") process.stdout.write(`\n${preview}\n`);
        pending.value = proposal;
        return new Promise((resolve) => {
          const deny = () => finish(false);
          const finish = (allowed: boolean) => {
            approvalSignal.removeEventListener("abort", deny);
            pending.value = undefined;
            decide = undefined;
            resolve(allowed);
          };
          decide = finish;
          approvalSignal.addEventListener("abort", deny, { once: true });
          if (approvalSignal.aborted) finish(false);
        });
      }
      onUnmounted(() => turn?.abort());

      async function submit() {
        if (busy.value) {
          if (isCommand(draft.value.trim()))
            status.value = "Busy · wait for the current turn to finish";
          return;
        }
        if (!draft.value.trim()) return;
        const prompt = draft.value;
        draft.value = "";
        await nextTick();
        const command = prompt.trim();
        const modeCommand = command.match(/^\/mode(?:\s+(\S+))?\s*$/i);
        if (modeCommand) {
          const requested = modeCommand[1]?.toLowerCase();
          if (!requested) {
            status.value = `Current mode: ${selectedIntent.value}. Use /mode plan, /mode build or /mode chat`;
          } else if (
            requested !== "plan" &&
            requested !== "build" &&
            requested !== "chat"
          ) {
            status.value = `Unknown mode: ${requested}. Use /mode plan, /mode build or /mode chat`;
          } else {
            selectedIntent.value = requested;
            status.value = `Mode changed to ${requested}`;
          }
          return;
        }
        if (
          /^\/(theme|mouse|copy)(\s|$)/i.test(command) ||
          command === "/theme" ||
          command === "/mouse" ||
          command === "/copy"
        ) {
          // Session theme/mouse are Tuimark-host features (@theme, <tui
          // mouse>); /copy needs the Tuimark client's clipboard tools.
          status.value =
            "/theme, /mouse and /copy are available in the Tuimark client";
          return;
        }
        if (command === "/help") {
          status.value =
            "/mode plan|build|chat · /mcp · /help · Esc cancel · Ctrl+C exit";
          return;
        }
        if (command === "/mcp") {
          status.value = mcp.length
            ? `MCP servers: ${mcp.length} · ${mcp.reduce((total, server) => total + server.tools.length, 0)} tools`
            : "MCP servers: 0 · use --mcp-config to connect trusted servers";
          return;
        }
        if (isCommand(command)) {
          status.value = `Unknown command: ${command}. Use /help`;
          return;
        }
        live.value = "";
        busy.value = true;
        const request = new AbortController();
        turn = request;
        let receivedMessage = false;
        let evalCount = 0;
        let evalDuration = 0;
        history.value.push({ role: "user", content: prompt });
        if (mode === "inline")
          process.stdout.write(`\nYou: ${terminalText(prompt)}\nAssistant: `);
        try {
          for await (const event of runAgent(provider, history.value, {
            workspace,
            intent: selectedIntent.value,
            approve,
            deniedRead: fileRules.deniedRead,
            signal: AbortSignal.any([signal, request.signal]),
            session,
            mcp,
          })) {
            if (event.type === "thinking") thinking.value = true;
            if (event.type === "text" || event.type === "tool_requested")
              thinking.value = false;
            if (event.type === "done") {
              evalCount += event.evalCount ?? 0;
              evalDuration += event.evalDuration ?? 0;
              thinking.value = false;
            }
            if (event.type === "message") {
              receivedMessage = true;
              history.value.push(event.message);
              live.value = "";
            }
            if (event.type === "tool_result") {
              status.value = `${event.name}: ${event.status}`;
              if (mode === "inline")
                process.stdout.write(`\n${terminalText(status.value)}\n`);
            }
            if (event.type === "text") {
              live.value += event.text;
              if (mode === "inline")
                process.stdout.write(terminalText(event.text));
            }
          }
          const speed = tokensPerSecond(evalCount, evalDuration);
          status.value = speed ? `${idleStatus} · ${speed}` : idleStatus;
        } catch (error) {
          // Keep completed messages and tool receipts even when a later request fails.
          if (!session) {
            if (!receivedMessage) history.value.pop();
            if (!receivedMessage && !draft.value) draft.value = prompt;
          }
          status.value = request.signal.aborted
            ? "Cancelled"
            : terminalText(
                error instanceof Error ? error.message : "Inference failed",
              );
        } finally {
          if (mode === "inline") process.stdout.write("\n");
          busy.value = false;
          thinking.value = false;
          live.value = "";
          if (turn === request) turn = undefined;
        }
      }
      onKeyDown((key) => {
        if (key.name === "escape" && !pending.value) {
          turn?.abort();
          key.preventDefault();
        }
        if (key.ctrl && key.name === "c") exit();
      });
      return () =>
        h(
          PanelLayout,
          { sidebar: mode === "fullscreen" },
          {
            default: () => [
              h(
                Text,
                { bold: true },
                () =>
                  `Local Agent · ${provider.type === "ollama_remote" ? "Chalupa / remote" : "Ollama / loopback"} · experimental`,
              ),
              mode === "fullscreen"
                ? h(
                    ScrollBox,
                    {
                      flexGrow: 1,
                      minHeight: 0,
                      stickyScroll: true,
                      stickyStart: "bottom",
                    },
                    () => [
                      ...history.value.map((message, index) =>
                        h(
                          Box,
                          {
                            key: `message-${index}`,
                            flexDirection: "column",
                            marginBottom: 1,
                            flexShrink: 0,
                          },
                          () => [
                            h(Text, { bold: true }, () =>
                              message.role === "user"
                                ? "You"
                                : message.role === "tool"
                                  ? `Tool · ${message.tool_name ?? "result"}`
                                  : "Assistant",
                            ),
                            message.role === "assistant"
                              ? h(Markdown, {
                                  content: terminalText(message.content),
                                  syntaxStyle: messageSyntax,
                                  width: "100%",
                                })
                              : h(Text, {}, () =>
                                  terminalText(messageText(message)),
                                ),
                          ],
                        ),
                      ),
                      live.value
                        ? h(
                            Box,
                            { flexDirection: "column", marginBottom: 1 },
                            () => [
                              h(Text, { bold: true }, () => "Assistant"),
                              h(Markdown, {
                                content: terminalText(live.value),
                                syntaxStyle: messageSyntax,
                                width: "100%",
                                streaming: true,
                              }),
                            ],
                          )
                        : null,
                    ],
                  )
                : null,
              h(Text, {}, () =>
                busy.value
                  ? `${thinking.value ? "Thinking…" : "Generating"} · Esc cancel`
                  : status.value,
              ),
              pending.value
                ? h(Approval, {
                    title:
                      pending.value.kind === "edit"
                        ? `Edit ${terminalText(pending.value.path)}? y approve · Enter/n deny`
                        : pending.value.kind === "write"
                          ? `${pending.value.created ? "Create" : "Replace"} ${terminalText(pending.value.path)}? y approve · Enter/n deny`
                          : pending.value.kind === "mcp"
                            ? `Call ${terminalText(pending.value.inner ?? pending.value.name)}${pending.value.gateway ? ` via ${terminalText(pending.value.gateway)}` : ""}? y approve · Enter/n deny`
                            : "Run command? y approve · Enter/n deny",
                    preview:
                      mode === "inline" &&
                      (pending.value.kind === "edit" ||
                        pending.value.kind === "write")
                        ? "Diff printed above"
                        : approvalPreview(pending.value, workspace.root),
                    maxLines: mode === "inline" ? 1 : 12,
                    onDecision: (allowed: boolean) => {
                      decide?.(allowed);
                    },
                  })
                : h(Composer, {
                    modelValue: draft.value,
                    busy: busy.value,
                    "onUpdate:modelValue": (value: string) => {
                      draft.value = value;
                    },
                    onSubmit: () => void submit(),
                    onCancel: () => turn?.abort(),
                  }),
            ],
            sidebar: () =>
              h(
                Text,
                {},
                () =>
                  `Model\n${provider.model}\n\nContext\n${provider.context_size}\n\nMode\n${selectedIntent.value}\n${selectedIntent.value === "plan" ? "Read only" : "Read · Edit · Write · Command"}\n\nMCP servers: ${mcp.length}\n${session ? `Session\n${session.state.publicId}` : "Session: temporary"}`,
              ),
          },
        );
    },
  });
}

export async function runTerminal(
  provider: Provider,
  mode: Presentation,
  signal: AbortSignal,
  workspace: Workspace,
  intent: "plan" | "build" | "chat",
  session?: SessionContext,
  mcp: readonly McpConnection[] = [],
) {
  signal.throwIfAborted();
  const Root = createTerminalRoot(
    provider,
    mode,
    signal,
    workspace,
    intent,
    session,
    mcp,
  );
  const app = await createApp(Root, null, terminalOptions(mode));
  const stop = () => app.exit();
  signal.addEventListener("abort", stop, { once: true });
  try {
    signal.throwIfAborted();
    app.mount();
    await app.waitUntilExit();
  } finally {
    signal.removeEventListener("abort", stop);
    app.exit();
  }
}

/** The Reference view's bounds table, from the runtime's own LIMITS. */

import type { RouteName } from "#desktop/renderer/router";
import type { ReferenceView } from "#desktop/shared/protocol";

/**
 * What the Reference view's feature map lists. A feature with a `route` has
 * a workbench view of its own (and is live there); one without is the
 * terminal client's alone, and the table says so instead of linking to the
 * page it is on.
 */
export type Feature = {
  name: string;
  detail: string;
  route?: Exclude<RouteName, "reference">;
};

export const features: Feature[] = [
  {
    name: "Agent chat",
    detail: "streaming turns, thinking, receipts",
    route: "chat",
  },
  {
    name: "Approvals",
    detail: "edit diff, argv, MCP args — approve/deny inline",
    route: "chat",
  },
  {
    name: "Plan / build intents",
    detail: "read-only plan vs gated build",
    route: "chat",
  },
  {
    name: "Sessions",
    detail: "SQLite create/list/resume/inspect",
    route: "sessions",
  },
  {
    name: "Recovery",
    detail: "hazard review tokens, never auto-replay",
    route: "recovery",
  },
  { name: "Workspace", detail: "fd-pinned bounded reads", route: "workspace" },
  {
    name: "MCP",
    detail: "stdio/HTTP trust, status, tool catalog",
    route: "mcp",
  },
  {
    name: "Provider admission",
    detail: "inventory, weight, context evidence",
    route: "providers",
  },
  {
    name: "Chalupa profile",
    detail: "process-scoped remote config",
    route: "providers",
  },
  {
    name: "Project checks",
    detail: "check, test, builds, git",
    route: "project",
  },
  {
    name: "Contracts",
    detail: "Glyphrun loopback specs inventory",
    route: "project",
  },
  { name: "Fullscreen TUI", detail: "terminal-only; command provided" },
  { name: "Inline TUI", detail: "terminal-only; command provided" },
];

/** A byte count in the unit it is set in: MiB or KiB for a power of two,
 * MB for a round million. */
export function bytes(count: number): string {
  for (const [unit, size] of [
    ["MiB", 1024 ** 2],
    ["KiB", 1024],
    ["MB", 1_000_000],
  ] as const)
    if (count >= size && count % size === 0) return `${count / size} ${unit}`;
  return `${count.toLocaleString("en-US")} bytes`;
}

export function duration(ms: number): string {
  if (ms >= 60_000 && ms % 60_000 === 0) return `${ms / 60_000} min`;
  if (ms >= 1_000 && ms % 1_000 === 0) return `${ms / 1_000} s`;
  return `${ms.toLocaleString("en-US")} ms`;
}

const count = (value: number) => value.toLocaleString("en-US");

/** One row per bound: what it bounds, and the bound in words. */
export function limitRows(limits: ReferenceView["limits"]): [string, string][] {
  return [
    [
      "file",
      `${bytes(limits.fileBytes)} read or written; valid UTF-8, no NUL bytes, no hard links`,
    ],
    ["image", `${bytes(limits.imageBytes)}; PNG, JPEG, WebP or GIF`],
    ["fetch", `${bytes(limits.fetchBytes)} of body`],
    [
      "command",
      `${count(limits.commandArgs)} arguments, ${bytes(limits.commandArgBytes)} of argv, ${bytes(limits.commandOutputBytes)} of output kept`,
    ],
    [
      "MCP config",
      `${bytes(limits.mcpConfigBytes)}, at most ${count(limits.mcpServers)} servers`,
    ],
    [
      "MCP tools",
      `at most ${count(limits.mcpToolsPerServer)} per server, no pagination`,
    ],
    [
      "MCP result",
      `${count(limits.mcpTextChars)} characters of text, then cut`,
    ],
    [
      "model response",
      `${count(limits.responseChars)} characters of streamed JSON, at most ${count(limits.toolCallsPerResponse)} tool calls of ${count(limits.toolCallChars)} characters each`,
    ],
    [
      "stream idle",
      `${duration(limits.streamIdleMs)} unless the provider sets its own`,
    ],
  ];
}

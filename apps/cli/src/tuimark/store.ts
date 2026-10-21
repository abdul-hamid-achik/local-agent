// The Tuimark store: every path ui/app.tui reads, with its shape. One place, so
// the controller, the samples the goldens are made from, and the tests cannot
// drift from the markup.

import type { ApprovalRequest, Provider } from "@local-agent/runtime";
import { approvalPreview } from "#cli/preview";
import {
  cleanText,
  clip,
  IDLE_LIVE,
  type LiveView,
  type MessageView,
} from "#cli/tuimark/blocks";
import {
  filterPalette,
  type Intent,
  type PaletteItem,
} from "#cli/tuimark/commands";
import type { TranscriptSnapshot } from "#cli/tuimark/transcript";

export const IDLE_STATUS = "Ready · /help for commands";
export const PLACEHOLDER = "Message the model";
export const PLACEHOLDER_BUSY = "Generating…";
export const APPROVAL_STATUS =
  "Waiting for approval · y allow · n deny · Esc deny";
export const BUSY_STATUS_LINE = "Generating · Esc cancel";

export interface DiffLine {
  n: number;
  t: string;
  add: boolean;
  del: boolean;
}

export interface ApprovalState {
  open: boolean;
  title: string;
  body: string;
  danger: boolean;
  no: string;
  yes: string;
  lines: DiffLine[];
}

export interface Store {
  header: {
    provider: string;
    model: string;
    mode: Intent;
    session: string;
    ctx_label: string;
    ctx_pct: number;
  };
  messages: MessageView[];
  earlier: { n: number };
  live: LiveView;
  approval: ApprovalState;
  palette: { open: boolean; query: string; cur: string };
  found: { any: boolean; items: PaletteItem[] };
  help: { open: boolean };
  draft: string;
  placeholder: string;
  busy: boolean;
  status: string;
}

/** Context size as the Vue sidebar says it: "32k" for 32768 tokens. */
export function tokens(n: number): string {
  if (n < 1000) return String(n);
  const k = n / 1024;
  return `${k >= 10 ? Math.round(k) : k.toFixed(1)}k`;
}

export function contextLabel(used: number, size: number): string {
  return `${tokens(used)}/${tokens(size)}`;
}

export function contextPct(used: number, size: number): number {
  return Math.max(0, Math.min(100, Math.round((used / size) * 100)));
}

export function providerLabel(provider: Pick<Provider, "type">): string {
  return provider.type === "ollama_remote"
    ? "Chalupa / remote"
    : "Ollama / loopback";
}

export function noApproval(): ApprovalState {
  return {
    open: false,
    title: "",
    body: "",
    danger: false,
    no: "Deny",
    yes: "Allow",
    lines: [],
  };
}

export interface StoreInit {
  provider: Pick<Provider, "type" | "model" | "context_size">;
  intent: Intent;
  /** Public session handle, or undefined for a temporary session. */
  sessionId?: string;
  mcpServers: number;
  transcript: TranscriptSnapshot;
}

export function initialStore(init: StoreInit): Store {
  const found = filterPalette("");
  return {
    header: {
      provider: providerLabel(init.provider),
      model: clip(cleanText(init.provider.model), 40),
      mode: init.intent,
      session: init.sessionId ? `session ${init.sessionId}` : "temporary",
      ctx_label: contextLabel(0, init.provider.context_size),
      ctx_pct: 0,
    },
    messages: init.transcript.messages,
    earlier: init.transcript.earlier,
    live: IDLE_LIVE,
    approval: noApproval(),
    palette: { open: false, query: "", cur: found[0]?.id ?? "" },
    found: { any: found.length > 0, items: found },
    help: { open: false },
    draft: "",
    placeholder: PLACEHOLDER,
    busy: false,
    status: init.mcpServers
      ? `${IDLE_STATUS} · MCP ${init.mcpServers}`
      : IDLE_STATUS,
  };
}

// ---------------------------------------------------------------------- approval

const APPROVAL_LINES = 200;

function diffLines(text: string): DiffLine[] {
  return cleanText(text)
    .split("\n")
    .slice(0, APPROVAL_LINES)
    .map((t, n) => ({
      n,
      t,
      add: t.startsWith("+") && !t.startsWith("+++"),
      del: t.startsWith("-") && !t.startsWith("---"),
    }));
}

/** What the approval dialog shows for a runtime proposal. */
export function approvalState(proposal: ApprovalRequest): ApprovalState {
  const base = {
    ...noApproval(),
    open: true,
    lines: diffLines(approvalPreview(proposal)),
  };
  switch (proposal.kind) {
    case "edit":
      return {
        ...base,
        title: clip(`Edit ${cleanText(proposal.path)}?`, 70),
        body: "Apply this change to the workspace.",
      };
    case "mcp":
      return {
        ...base,
        title: clip(`Call ${cleanText(proposal.name)}?`, 70),
        body: "MCP effects are not verified by transport success.",
        danger: true,
      };
    default:
      return {
        ...base,
        title: "Run command?",
        body: clip(cleanText(proposal.argv.join(" ")), 200),
        danger: true,
      };
  }
}

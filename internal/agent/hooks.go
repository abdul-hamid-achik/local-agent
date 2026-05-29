package agent

import (
	"context"
	"fmt"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

// ToolHook is an interceptor that runs around every tool execution. It is the
// in-process equivalent of iii's before/after tool-hook fanout: a single seam
// for audit logging, argument validation/redaction, and policy side effects,
// without the agent loop having to special-case any of them.
//
// Hooks run in registration order. They must be safe for concurrent use:
// MCP and memory tools execute in parallel goroutines.
type ToolHook interface {
	// Name identifies the hook in logs.
	Name() string

	// PreToolUse runs before a tool executes. It may mutate call.Arguments in
	// place (e.g. to redact or normalize). Returning block=true cancels the
	// call; reason becomes the synthetic tool result returned to the model.
	PreToolUse(ctx context.Context, call *llm.ToolCall) (block bool, reason string)

	// PostToolUse runs after a tool executes. It may rewrite the result in
	// place (e.g. to redact secrets or cap size). isErr reflects whether the
	// tool reported an error.
	PostToolUse(ctx context.Context, call llm.ToolCall, result *string, isErr bool)
}

// AddToolHook registers a tool hook. Not safe to call concurrently with Run.
func (a *Agent) AddToolHook(h ToolHook) {
	a.hooks = append(a.hooks, h)
}

// runPreHooks invokes every PreToolUse hook. If any blocks, it returns the
// block reason and the index is short-circuited (later hooks do not run).
func (a *Agent) runPreHooks(ctx context.Context, call *llm.ToolCall) (block bool, reason string) {
	for _, h := range a.hooks {
		if b, r := h.PreToolUse(ctx, call); b {
			if a.logger != nil {
				a.logger.Debug("tool hook block", "hook", h.Name(), "tool", call.Name, "reason", r)
			}
			return true, r
		}
	}
	return false, ""
}

// runPostHooks invokes every PostToolUse hook, letting each rewrite the result.
func (a *Agent) runPostHooks(ctx context.Context, call llm.ToolCall, result *string, isErr bool) {
	for _, h := range a.hooks {
		h.PostToolUse(ctx, call, result, isErr)
	}
}

// SizeCapHook truncates oversized tool results so a single runaway tool (a
// huge file read, a noisy command) cannot blow the context-window budget — a
// real risk on the small local models this agent targets. A no-op PreToolUse.
type SizeCapHook struct {
	MaxBytes int
}

// NewSizeCapHook returns a size-cap hook. A non-positive max disables capping.
func NewSizeCapHook(maxBytes int) *SizeCapHook { return &SizeCapHook{MaxBytes: maxBytes} }

func (h *SizeCapHook) Name() string { return "size-cap" }

func (h *SizeCapHook) PreToolUse(ctx context.Context, call *llm.ToolCall) (bool, string) {
	return false, ""
}

func (h *SizeCapHook) PostToolUse(ctx context.Context, call llm.ToolCall, result *string, isErr bool) {
	if h.MaxBytes <= 0 || result == nil || len(*result) <= h.MaxBytes {
		return
	}
	truncated := (*result)[:h.MaxBytes]
	*result = truncated + fmt.Sprintf("\n\n... [output truncated: %d of %d bytes shown]", h.MaxBytes, len(*result))
}

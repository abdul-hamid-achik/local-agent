package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// TurnReceiptSchema identifies the versioned machine-readable summary of one
// headless turn. Exactly one receipt document is emitted on stdout per --json
// invocation; everything else stays on stderr.
const TurnReceiptSchema = "local-agent.turn-receipt.v1"

// Turn receipt stop reasons form a closed catalog. External supervisors branch
// on these values, so new reasons are additive and existing ones never change
// meaning. Reasons shared with the goal supervisor keep its exact spelling.
const (
	StopReasonCompleted             = "completed"
	StopReasonCanceled              = "canceled"
	StopReasonOutcomeUnknown        = "outcome_unknown"
	StopReasonBudgetExhausted       = "budget_exhausted"
	StopReasonContextBudgetExceeded = "context_budget_exceeded"
	StopReasonEmptyTerminalResponse = "empty_terminal_response"
	StopReasonMalformedToolLoop     = "malformed_tool_loop"
	StopReasonHostRefusalLoop       = "host_refusal_loop"
	StopReasonError                 = "error"
)

// TurnReceiptSession identifies the durable session the turn executed under.
type TurnReceiptSession struct {
	ID        int64  `json:"id"`
	PublicID  string `json:"public_id,omitempty"`
	Workspace string `json:"workspace"`
}

// TurnReceiptModel records the inference identity the host resolved for the
// turn. Digest is present only when the provider inventory verified it.
type TurnReceiptModel struct {
	Name     string `json:"name"`
	Digest   string `json:"digest,omitempty"`
	NumCtx   int    `json:"num_ctx"`
	Provider string `json:"provider,omitempty"`
	Remote   bool   `json:"remote"`
}

// TurnReceiptUsage accumulates provider-reported token accounting across every
// request in the turn, including conservative fail-closed reservations.
type TurnReceiptUsage struct {
	PromptTokens int64 `json:"prompt_tokens"`
	EvalTokens   int64 `json:"eval_tokens"`
}

// TurnReceiptToolCall is one settled tool invocation. Raw arguments and
// results never enter the receipt; the execution ledger owns those hashes.
type TurnReceiptToolCall struct {
	CallID     string `json:"call_id,omitempty"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

// TurnReceipt is the complete document. RunID, TurnID, and Actor may carry
// externally minted identifiers; they are correlation labels and never grant
// authority.
type TurnReceipt struct {
	Schema               string                `json:"schema"`
	RunID                string                `json:"run_id,omitempty"`
	TurnID               string                `json:"turn_id,omitempty"`
	Actor                string                `json:"actor,omitempty"`
	Session              *TurnReceiptSession   `json:"session,omitempty"`
	Model                TurnReceiptModel      `json:"model"`
	Usage                TurnReceiptUsage      `json:"usage"`
	ToolCalls            []TurnReceiptToolCall `json:"tool_calls"`
	ToolCallsOmitted     int                   `json:"tool_calls_omitted,omitempty"`
	Status               string                `json:"status"`
	StopReason           string                `json:"stop_reason"`
	Error                string                `json:"error,omitempty"`
	ExecutionCursor      int64                 `json:"execution_cursor"`
	PendingRecoveryCount int                   `json:"pending_recovery_count"`
	Text                 string                `json:"text"`
}

// TurnOutcome maps a RunTurn error onto the receipt's closed status and
// stop-reason vocabulary. Status is "settled" for a clean turn, "canceled"
// for host/context cancellation, and "failed" otherwise.
func TurnOutcome(err error) (status, stopReason string) {
	var unresolved *UnresolvedExecutionError
	switch {
	case err == nil:
		return "settled", StopReasonCompleted
	case errors.As(err, &unresolved):
		return "failed", StopReasonOutcomeUnknown
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return "canceled", StopReasonCanceled
	case errors.Is(err, ErrTurnEvalBudgetExhausted):
		return "failed", StopReasonBudgetExhausted
	case errors.Is(err, ErrTurnContextBudgetExceeded):
		return "failed", StopReasonContextBudgetExceeded
	case errors.Is(err, ErrEmptyTerminalResponse):
		return "failed", StopReasonEmptyTerminalResponse
	case errors.Is(err, ErrMalformedToolLoop):
		return "failed", StopReasonMalformedToolLoop
	case errors.Is(err, ErrRepeatedHostRefusal):
		return "failed", StopReasonHostRefusalLoop
	default:
		return "failed", StopReasonError
	}
}

// WriteTurnReceipt emits exactly one newline-terminated JSON document. The
// schema field is stamped here so callers cannot ship an unversioned receipt.
func WriteTurnReceipt(w io.Writer, receipt TurnReceipt) error {
	receipt.Schema = TurnReceiptSchema
	if receipt.ToolCalls == nil {
		receipt.ToolCalls = []TurnReceiptToolCall{}
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode turn receipt: %w", err)
	}
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write turn receipt: %w", err)
	}
	return nil
}

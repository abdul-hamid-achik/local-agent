package main

import (
	"context"
	"flag"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
)

type sessionRepairStore interface {
	AcquireExecutionSessionLease(context.Context, int64, string) (*db.ExecutionSessionLease, error)
	RepairSessionProjection(context.Context, *db.ExecutionSessionLease, int64, string) (db.SessionProjectionRepairReceipt, error)
}

type sessionRepairView struct {
	SessionID       int64                     `json:"session_id"`
	WorkspaceID     string                    `json:"workspace_id"`
	SessionRevision int64                     `json:"session_revision"`
	PreviousCursor  int64                     `json:"previous_cursor"`
	NewCursor       int64                     `json:"new_cursor"`
	AnsweredTotal   int                       `json:"answered_total"`
	Repaired        []sessionRepairEffectView `json:"repaired"`
}

type sessionRepairEffectView struct {
	ExecutionID   string `json:"execution_id"`
	ToolName      string `json:"tool_name"`
	EventID       int64  `json:"event_id"`
	EventType     string `json:"event_type"`
	EffectClass   string `json:"effect_class"`
	ReceiptOnFile bool   `json:"receipt_on_file"`
}

func handleSessionCommand(args []string) int {
	return handleSessionCommandIO(args, os.Stdout, os.Stderr)
}

func handleSessionCommandIO(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeSessionUsage(stdout)
		return 0
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) > 1 {
			executionFprintf(stderr, "session: unexpected argument %q after %s\n", args[1], args[0])
			return 2
		}
		writeSessionUsage(stdout)
		return 0
	}
	if args[0] != "repair" {
		executionFprintf(stderr, "session: unknown command %q\n", args[0])
		writeSessionUsage(stderr)
		return 2
	}
	if hasHelpFlag(args[1:]) {
		writeSessionUsage(stdout)
		return 0
	}
	workspace := currentWorkspace()
	if workspace == "" {
		executionFprintln(stderr, "session: workspace identity is unavailable")
		return 1
	}
	store, err := db.Open()
	if err != nil {
		executionFprintf(stderr, "session: open durable store: %v\n", err)
		return 1
	}
	defer func() {
		if err := store.Close(); err != nil {
			executionFprintf(stderr, "session: close durable store: %v\n", err)
		}
	}()
	return handleSessionRepair(store, workspace, args[1:], stdout, stderr)
}

func handleSessionRepair(store sessionRepairStore, workspace string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("session repair", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { writeSessionUsage(stdout) }
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	normalized := make([]string, 0, len(args))
	var positional []string
	for _, argument := range args {
		if strings.HasPrefix(argument, "-") {
			normalized = append(normalized, argument)
			continue
		}
		positional = append(positional, argument)
	}
	if code, done := flagParseExitCode(flags.Parse(append(normalized, positional...))); done {
		return code
	}
	if flags.NArg() != 1 {
		executionFprintln(stderr, "session repair: provide SESSION_ID")
		return 2
	}
	sessionID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || sessionID <= 0 {
		executionFprintf(stderr, "session repair: invalid session ID %q\n", flags.Arg(0))
		return 2
	}
	lease, err := store.AcquireExecutionSessionLease(context.Background(), sessionID, workspace)
	if err != nil {
		executionFprintf(stderr, "session repair: acquire exact session lease: %v\n", err)
		return 1
	}
	defer func() {
		if err := lease.Close(); err != nil {
			executionFprintf(stderr, "session repair: release session lease: %v\n", err)
		}
	}()
	receipt, err := store.RepairSessionProjection(context.Background(), lease, sessionID, workspace)
	if err != nil {
		executionFprintf(stderr, "session repair: %v\n", err)
		return 1
	}
	view := sessionRepairView{
		SessionID: receipt.SessionID, WorkspaceID: receipt.WorkspaceID,
		SessionRevision: receipt.SessionRevision,
		PreviousCursor:  receipt.PreviousCursor, NewCursor: receipt.NewCursor,
		AnsweredTotal: receipt.AnsweredTotal,
		Repaired:      make([]sessionRepairEffectView, 0, len(receipt.Repaired)),
	}
	for _, effect := range receipt.Repaired {
		view.Repaired = append(view.Repaired, sessionRepairEffectView{
			ExecutionID: effect.ExecutionID, ToolName: effect.ToolName,
			EventID: effect.EventID, EventType: string(effect.EventType),
			EffectClass: string(effect.EffectClass), ReceiptOnFile: effect.ResultReceipt != "",
		})
	}
	if *jsonOutput {
		if err := writeExecutionJSON(stdout, view); err != nil {
			executionFprintf(stderr, "session repair: %v\n", err)
			return 1
		}
		return 0
	}
	executionFprintf(stdout, "Repaired session %d projection: cursor %d -> %d @ revision %d.\n",
		view.SessionID, view.PreviousCursor, view.NewCursor, view.SessionRevision)
	if view.AnsweredTotal == 0 {
		executionFprintln(stdout, "No answered effects were missing; the cursor was only re-derived from durable state.")
		return 0
	}
	executionFprintf(stdout, "%d answered effect(s) were newer than the saved transcript:\n", view.AnsweredTotal)
	for _, effect := range view.Repaired {
		executionFprintf(stdout, "  %s · %s · event %d · %s/%s\n",
			terminalSafeGoalText(effect.ExecutionID), terminalSafeGoalText(effect.ToolName),
			effect.EventID, effect.EventType, effect.EffectClass)
	}
	if view.AnsweredTotal > len(view.Repaired) {
		executionFprintf(stdout, "  ...and %d more; every effect remains recorded in the durable execution ledger.\n",
			view.AnsweredTotal-len(view.Repaired))
	}
	executionFprintln(stdout, "These effects happened but are absent from the saved transcript. Inspect them with:")
	executionFprintf(stdout, "  local-agent execution recover %d EXECUTION_ID\n", view.SessionID)
	executionFprintln(stdout, "and tell the agent what changed (or let it recheck workspace state) on the next prompt.")
	return 0
}

func writeSessionUsage(writer io.Writer) {
	executionFprintln(writer, "Usage:")
	executionFprintln(writer, "  local-agent session repair [--json] SESSION_ID")
	executionFprintln(writer)
	executionFprintln(writer, "Repair re-derives the session's execution snapshot cursor from the durable")
	executionFprintln(writer, "ledger after a crash left answered terminal receipts newer than the saved")
	executionFprintln(writer, "transcript. It refuses while any execution still requires reconciliation")
	executionFprintln(writer, "evidence (run `local-agent execution recover SESSION_ID --all` first), never")
	executionFprintln(writer, "retries a tool, and never rewrites the immutable execution ledger.")
	executionFprintln(writer)
	executionFprintln(writer, "Close the interactive session before repairing; the exact session lease is")
	executionFprintln(writer, "exclusive.")
}

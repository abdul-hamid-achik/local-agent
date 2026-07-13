package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/reconciliation"
)

const executionRecoveryActor = "local-user"

type executionRecoveryStore interface {
	InspectStandaloneExecutionReconciliation(context.Context, int64, string, string) (db.StandaloneExecutionReconciliationInspection, error)
	AcquireExecutionSessionLease(context.Context, int64, string) (*db.ExecutionSessionLease, error)
	ResolveStandaloneExecutionReconciliation(context.Context, *db.ExecutionSessionLease, db.ResolveStandaloneExecutionReconciliationRequest) (db.StandaloneExecutionReconciliationReceipt, error)
}

type executionRecoveryView struct {
	SessionID       int64  `json:"session_id"`
	WorkspaceID     string `json:"workspace_id"`
	SessionRevision int64  `json:"session_revision"`
	ExecutionID     string `json:"execution_id"`
	TurnID          string `json:"turn_id"`
	ToolName        string `json:"tool_name"`
	EventID         int64  `json:"event_id"`
	EventType       string `json:"event_type"`
	EffectClass     string `json:"effect_class"`
	ArgumentsSHA256 string `json:"arguments_sha256"`
	ItemID          string `json:"item_id"`
	Resolved        bool   `json:"resolved"`
	ResolutionID    string `json:"resolution_id,omitempty"`
	ApplyTemplate   string `json:"apply_template,omitempty"`
}

type executionRecoveryApplyView struct {
	SessionID       int64  `json:"session_id"`
	SessionRevision int64  `json:"session_revision"`
	ExecutionID     string `json:"execution_id"`
	EventID         int64  `json:"event_id"`
	ItemID          string `json:"item_id"`
	ResolutionID    string `json:"resolution_id"`
	Inserted        bool   `json:"inserted"`
	Notice          string `json:"notice"`
}

func handleExecutionCommand(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		writeExecutionUsage(os.Stdout)
		return 0
	}
	workspace := currentWorkspace()
	if workspace == "" {
		executionFprintln(os.Stderr, "execution: workspace identity is unavailable")
		return 1
	}
	store, err := db.Open()
	if err != nil {
		executionFprintf(os.Stderr, "execution: open durable store: %v\n", err)
		return 1
	}
	defer func() {
		if err := store.Close(); err != nil {
			executionFprintf(os.Stderr, "execution: close durable store: %v\n", err)
		}
	}()
	switch args[0] {
	case "recover":
		return handleExecutionRecover(store, workspace, args[1:], os.Stdout, os.Stderr)
	default:
		executionFprintf(os.Stderr, "execution: unknown command %q\n", args[0])
		writeExecutionUsage(os.Stderr)
		return 2
	}
}

func handleExecutionRecover(store executionRecoveryStore, workspace string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("execution recover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	apply := flags.Bool("apply", false, "append typed reconciliation evidence")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	revision := flags.Int64("revision", -1, "exact session revision shown by inspection")
	eventID := flags.Int64("event-id", -1, "exact latest event ID shown by inspection")
	observation := flags.String("observation", "", "effect_applied, effect_not_applied, or effect_compensated")
	source := flags.String("source", "", "external_receipt, workspace_artifact, verification_check, or operator_observation")
	reference := flags.String("reference", "", "redacted evidence reference")
	summary := flags.String("summary", "", "bounded inspection summary")
	observedAtText := flags.String("observed-at", "", "evidence observation time in RFC3339")
	normalized, err := normalizeExecutionRecoverArgs(args)
	if err != nil {
		executionFprintf(stderr, "execution recover: %v\n", err)
		return 2
	}
	if err := flags.Parse(normalized); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		executionFprintln(stderr, "execution recover: provide SESSION_ID and EXECUTION_ID")
		return 2
	}
	sessionID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || sessionID <= 0 {
		executionFprintf(stderr, "execution recover: invalid session ID %q\n", flags.Arg(0))
		return 2
	}
	executionID := flags.Arg(1)
	provided := make(map[string]bool)
	flags.Visit(func(value *flag.Flag) { provided[value.Name] = true })
	if !*apply {
		for name := range provided {
			if name != "json" && name != "apply" {
				executionFprintf(stderr, "execution recover: --%s requires --apply\n", name)
				return 2
			}
		}
		inspection, err := store.InspectStandaloneExecutionReconciliation(context.Background(), sessionID, workspace, executionID)
		if err != nil {
			executionFprintf(stderr, "execution recover: inspect immutable receipt: %v\n", err)
			return 1
		}
		view := projectExecutionRecovery(inspection)
		if *jsonOutput {
			if err := writeExecutionJSON(stdout, view); err != nil {
				executionFprintf(stderr, "execution recover: %v\n", err)
				return 1
			}
		} else {
			writeExecutionRecovery(stdout, view)
		}
		return 0
	}

	required := []string{"revision", "event-id", "observation", "source", "reference", "summary", "observed-at"}
	for _, name := range required {
		if !provided[name] {
			executionFprintf(stderr, "execution recover: --apply requires --%s from a prior inspection\n", name)
			return 2
		}
	}
	if *revision < 0 || *eventID <= 0 {
		executionFprintln(stderr, "execution recover: --revision must be non-negative and --event-id must be positive")
		return 2
	}
	observedAt, err := time.Parse(time.RFC3339, *observedAtText)
	if err != nil {
		executionFprintf(stderr, "execution recover: invalid --observed-at RFC3339 value: %v\n", err)
		return 2
	}
	evidence := reconciliation.Request{
		Disposition: reconciliation.Disposition(*observation),
		Source: reconciliation.Source{
			Kind: reconciliation.SourceKind(*source), Reference: *reference,
			ObservedAt: observedAt.UTC(),
		},
		Summary: *summary,
	}
	if err := evidence.Validate(); err != nil {
		executionFprintf(stderr, "execution recover: invalid typed evidence: %v\n", err)
		return 2
	}
	lease, err := store.AcquireExecutionSessionLease(context.Background(), sessionID, workspace)
	if err != nil {
		executionFprintf(stderr, "execution recover: acquire exact session lease: %v\n", err)
		return 1
	}
	defer func() {
		if err := lease.Close(); err != nil {
			executionFprintf(stderr, "execution recover: release session lease: %v\n", err)
		}
	}()
	receipt, err := store.ResolveStandaloneExecutionReconciliation(context.Background(), lease, db.ResolveStandaloneExecutionReconciliationRequest{
		SessionID: sessionID, WorkspaceID: workspace, ExecutionID: executionID,
		ExpectedSessionRevision: *revision, ExpectedEventID: *eventID,
		Actor: executionRecoveryActor, Evidence: evidence,
	})
	if err != nil {
		executionFprintf(stderr, "execution recover: append typed evidence: %v\n", err)
		return 1
	}
	view := executionRecoveryApplyView{
		SessionID: receipt.SessionID, SessionRevision: receipt.SessionRevision,
		ExecutionID: receipt.ExecutionID, EventID: receipt.EventID,
		ItemID: receipt.ItemID, ResolutionID: receipt.ResolutionID, Inserted: receipt.Inserted,
		Notice: "Evidence recorded; the immutable outcome-unknown event was not changed and no tool was retried.",
	}
	if *jsonOutput {
		if err := writeExecutionJSON(stdout, view); err != nil {
			executionFprintf(stderr, "execution recover: %v\n", err)
			return 1
		}
	} else {
		executionFprintf(stdout, "Reconciled execution %s at event %d.\nReceipt: %s\n%s\n", terminalSafeGoalText(view.ExecutionID), view.EventID, terminalSafeGoalText(view.ResolutionID), view.Notice)
	}
	return 0
}

func normalizeExecutionRecoverArgs(args []string) ([]string, error) {
	valueFlags := map[string]bool{
		"revision": true, "event-id": true, "observation": true,
		"source": true, "reference": true, "summary": true, "observed-at": true,
	}
	var flags, positional []string
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--apply" || argument == "-apply" || argument == "--json" || argument == "-json" {
			flags = append(flags, argument)
			continue
		}
		if strings.HasPrefix(argument, "--") || strings.HasPrefix(argument, "-") {
			nameValue := strings.TrimLeft(argument, "-")
			name, _, hasValue := strings.Cut(nameValue, "=")
			if valueFlags[name] && !hasValue {
				if index+1 >= len(args) {
					return nil, fmt.Errorf("--%s requires a value", name)
				}
				flags = append(flags, argument, args[index+1])
				index++
				continue
			}
			flags = append(flags, argument)
			continue
		}
		positional = append(positional, argument)
	}
	return append(flags, positional...), nil
}

func projectExecutionRecovery(inspection db.StandaloneExecutionReconciliationInspection) executionRecoveryView {
	view := executionRecoveryView{
		SessionID: inspection.SessionID, WorkspaceID: inspection.WorkspaceID,
		SessionRevision: inspection.SessionRevision, ExecutionID: inspection.ExecutionID,
		TurnID: inspection.TurnID, ToolName: inspection.ToolName,
		EventID: inspection.EventID, EventType: string(inspection.EventType),
		EffectClass: string(inspection.EffectClass), ArgumentsSHA256: inspection.ArgumentsSHA256,
		ItemID: inspection.ItemID, Resolved: inspection.Resolved, ResolutionID: inspection.ResolutionID,
	}
	if !inspection.Resolved {
		view.ApplyTemplate = fmt.Sprintf("local-agent execution recover --apply --revision %d --event-id %d --observation effect_not_applied --source verification_check --reference REF --summary SUMMARY --observed-at RFC3339 %d %s", inspection.SessionRevision, inspection.EventID, inspection.SessionID, inspection.ExecutionID)
	}
	return view
}

func writeExecutionRecovery(writer io.Writer, view executionRecoveryView) {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	executionFprintf(table, "SESSION\t%d @ revision %d\n", view.SessionID, view.SessionRevision)
	executionFprintf(table, "EXECUTION\t%s\n", terminalSafeGoalText(view.ExecutionID))
	executionFprintf(table, "TOOL\t%s\n", terminalSafeGoalText(view.ToolName))
	executionFprintf(table, "EVENT\t%d · %s · %s\n", view.EventID, view.EventType, view.EffectClass)
	executionFprintf(table, "TURN\t%s\n", terminalSafeGoalText(view.TurnID))
	executionFprintf(table, "ARGUMENTS\t%s\n", terminalSafeGoalText(view.ArgumentsSHA256))
	executionFprintf(table, "ITEM\t%s\n", terminalSafeGoalText(view.ItemID))
	_ = table.Flush()
	if view.Resolved {
		executionFprintf(writer, "Already reconciled by immutable receipt %s. No tool was retried.\n", terminalSafeGoalText(view.ResolutionID))
		return
	}
	executionFprintln(writer, "Inspection is read-only. Verify the external effect independently before applying one disposition.")
	executionFprintln(writer, "Apply template:")
	executionFprintln(writer, "  "+view.ApplyTemplate)
}

func writeExecutionUsage(writer io.Writer) {
	executionFprintln(writer, "Usage:")
	executionFprintln(writer, "  local-agent execution recover [--json] SESSION_ID EXECUTION_ID")
	executionFprintln(writer, "  local-agent execution recover --apply --revision N --event-id N --observation VALUE --source VALUE --reference TEXT --summary TEXT --observed-at RFC3339 [--json] SESSION_ID EXECUTION_ID")
	executionFprintln(writer)
	executionFprintln(writer, "Inspection is read-only. Apply requires exact inspection tokens and typed evidence; it never retries a tool or rewrites the immutable execution ledger.")
}

func executionFprintf(writer io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(writer, format, args...)
}

func executionFprintln(writer io.Writer, args ...any) {
	_, _ = fmt.Fprintln(writer, args...)
}

func writeExecutionJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}

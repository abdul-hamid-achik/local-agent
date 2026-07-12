package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/goal"
)

const goalCommandLimit = 100

type goalSessionStore interface {
	ListSessions(context.Context, db.ListSessionsParams) ([]db.Session, error)
	GetSession(context.Context, int64) (db.Session, error)
	GetSessionState(context.Context, int64) (string, error)
}

type goalControlStore interface {
	goalSessionStore
	ListControlStates(context.Context, controlplane.Query) ([]controlplane.State, error)
}

// goalSummary is the stable read model printed by `local-agent goal list`.
// The full validated snapshot remains available to the show command.
type goalSummary struct {
	SessionID       int64         `json:"session_id"`
	GoalID          string        `json:"goal_id"`
	State           goal.State    `json:"state"`
	Objective       string        `json:"objective"`
	CortexTaskID    string        `json:"cortex_task_id,omitempty"`
	CortexRevision  int64         `json:"cortex_revision,omitempty"`
	ContinuationUse int64         `json:"continuation_turns"`
	EvalTokenUse    int64         `json:"eval_tokens"`
	UpdatedAt       time.Time     `json:"goal_updated_at"`
	SessionUpdated  string        `json:"session_updated_at"`
	Snapshot        goal.Snapshot `json:"-"`
}

type goalSessionEnvelope struct {
	Goal *goal.Snapshot `json:"goal"`
}

// pendingControlSummary is the least-privilege CLI projection. Payload and
// evidence envelopes remain in the private durable store; inspection commands
// expose only the identity and explanation needed to choose a next action.
type pendingControlSummary struct {
	SessionID   int64             `json:"session_id"`
	ItemID      string            `json:"item_id"`
	Kind        controlplane.Kind `json:"kind"`
	GoalID      string            `json:"goal_id,omitempty"`
	ExecutionID string            `json:"execution_id,omitempty"`
	TurnID      string            `json:"turn_id,omitempty"`
	Summary     string            `json:"summary"`
	CreatedAt   time.Time         `json:"created_at"`
}

func handleGoalCommand(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		writeGoalUsage(os.Stdout)
		return 0
	}
	workspace := currentWorkspace()
	if workspace == "" {
		goalFprintln(os.Stderr, "goal: workspace identity is unavailable")
		return 1
	}
	store, err := db.Open()
	if err != nil {
		goalFprintf(os.Stderr, "goal: open durable store: %v\n", err)
		return 1
	}
	defer func() {
		if err := store.Close(); err != nil {
			goalFprintf(os.Stderr, "goal: close durable store: %v\n", err)
		}
	}()

	switch args[0] {
	case "list":
		return handleGoalList(store, workspace, args[1:], os.Stdout, os.Stderr)
	case "show":
		return handleGoalShow(store, workspace, args[1:], os.Stdout, os.Stderr)
	case "pending":
		return handleGoalPending(store, workspace, args[1:], os.Stdout, os.Stderr)
	default:
		goalFprintf(os.Stderr, "goal: unknown command %q\n", args[0])
		writeGoalUsage(os.Stderr)
		return 2
	}
}

func handleGoalPending(store goalControlStore, workspace string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("goal pending", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	limit := flags.Int("limit", 20, "maximum pending items to print (1-100)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "goal pending: provide exactly one session ID from `local-agent goal list`")
		return 2
	}
	sessionID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || sessionID <= 0 {
		_, _ = fmt.Fprintf(stderr, "goal pending: invalid session ID %q\n", flags.Arg(0))
		return 2
	}
	if *limit <= 0 || *limit > controlplane.MaxListLimit {
		_, _ = fmt.Fprintf(stderr, "goal pending: limit must be between 1 and %d\n", controlplane.MaxListLimit)
		return 2
	}
	if _, err := getGoalSummary(context.Background(), store, workspace, sessionID); err != nil {
		_, _ = fmt.Fprintf(stderr, "goal pending: %v\n", err)
		return 1
	}
	states, err := store.ListControlStates(context.Background(), controlplane.Query{
		SessionID: sessionID, WorkspaceID: workspace, PendingOnly: true, Limit: *limit,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "goal pending: list durable control items: %v\n", err)
		return 1
	}
	pending, err := projectPendingControlItems(states, sessionID, workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "goal pending: validate durable control items: %v\n", err)
		return 1
	}
	if *jsonOutput {
		if err := writeJSON(stdout, pending); err != nil {
			_, _ = fmt.Fprintf(stderr, "goal pending: %v\n", err)
			return 1
		}
		return 0
	}
	writePendingControlItems(stdout, pending)
	return 0
}

func handleGoalList(store goalSessionStore, workspace string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("goal list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	limit := flags.Int("limit", 20, "maximum durable sessions to inspect (1-100)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "goal list: unexpected positional arguments")
		return 2
	}
	if *limit <= 0 || *limit > goalCommandLimit {
		goalFprintf(stderr, "goal list: limit must be between 1 and %d\n", goalCommandLimit)
		return 2
	}
	summaries, warnings, err := listGoalSummaries(context.Background(), store, workspace, int64(*limit))
	if err != nil {
		goalFprintf(stderr, "goal list: %v\n", err)
		return 1
	}
	for _, warning := range warnings {
		goalFprintf(stderr, "goal list: warning: %v\n", warning)
	}
	if *jsonOutput {
		if err := writeJSON(stdout, summaries); err != nil {
			goalFprintf(stderr, "goal list: %v\n", err)
			return 1
		}
		return 0
	}
	writeGoalList(stdout, summaries)
	return 0
}

func handleGoalShow(store goalSessionStore, workspace string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("goal show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print the complete validated snapshot as JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "goal show: provide exactly one session ID from `local-agent goal list`")
		return 2
	}
	sessionID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || sessionID <= 0 {
		goalFprintf(stderr, "goal show: invalid session ID %q\n", flags.Arg(0))
		return 2
	}
	summary, err := getGoalSummary(context.Background(), store, workspace, sessionID)
	if err != nil {
		goalFprintf(stderr, "goal show: %v\n", err)
		return 1
	}
	if *jsonOutput {
		if err := writeJSON(stdout, summary.Snapshot); err != nil {
			goalFprintf(stderr, "goal show: %v\n", err)
			return 1
		}
		return 0
	}
	writeGoalDetail(stdout, summary)
	return 0
}

func listGoalSummaries(ctx context.Context, store goalSessionStore, workspace string, limit int64) ([]goalSummary, []error, error) {
	if store == nil {
		return nil, nil, errors.New("durable store is unavailable")
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, nil, errors.New("workspace identity is required")
	}
	if limit <= 0 || limit > goalCommandLimit {
		return nil, nil, fmt.Errorf("limit must be between 1 and %d", goalCommandLimit)
	}
	sessions, err := store.ListSessions(ctx, db.ListSessionsParams{WorkspaceID: workspace, Limit: limit})
	if err != nil {
		return nil, nil, fmt.Errorf("list durable sessions: %w", err)
	}
	summaries := make([]goalSummary, 0, len(sessions))
	warnings := make([]error, 0)
	for _, session := range sessions {
		raw, stateErr := store.GetSessionState(ctx, session.ID)
		if errors.Is(stateErr, db.ErrSessionStateNotFound) {
			continue
		}
		if stateErr != nil {
			warnings = append(warnings, fmt.Errorf("session %d: %w", session.ID, stateErr))
			continue
		}
		summary, present, decodeErr := decodeGoalSummary(session, raw)
		if decodeErr != nil {
			warnings = append(warnings, fmt.Errorf("session %d: %w", session.ID, decodeErr))
			continue
		}
		if present {
			summaries = append(summaries, summary)
		}
	}
	return summaries, warnings, nil
}

func getGoalSummary(ctx context.Context, store goalSessionStore, workspace string, sessionID int64) (goalSummary, error) {
	if store == nil {
		return goalSummary{}, errors.New("durable store is unavailable")
	}
	session, err := store.GetSession(ctx, sessionID)
	if err != nil {
		return goalSummary{}, fmt.Errorf("read session %d: %w", sessionID, err)
	}
	if session.WorkspaceID != workspace {
		return goalSummary{}, fmt.Errorf("session %d belongs to a different workspace", sessionID)
	}
	raw, err := store.GetSessionState(ctx, sessionID)
	if err != nil {
		return goalSummary{}, fmt.Errorf("read session %d state: %w", sessionID, err)
	}
	summary, present, err := decodeGoalSummary(session, raw)
	if err != nil {
		return goalSummary{}, err
	}
	if !present {
		return goalSummary{}, fmt.Errorf("session %d has no durable goal", sessionID)
	}
	return summary, nil
}

func decodeGoalSummary(session db.Session, raw string) (goalSummary, bool, error) {
	var envelope goalSessionEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return goalSummary{}, false, fmt.Errorf("decode durable session: %w", err)
	}
	if envelope.Goal == nil {
		return goalSummary{}, false, nil
	}
	if envelope.Goal.SessionID != session.ID {
		return goalSummary{}, false, fmt.Errorf("goal %q belongs to session %d, not %d", envelope.Goal.ID, envelope.Goal.SessionID, session.ID)
	}
	runtime, err := goal.Restore(*envelope.Goal)
	if err != nil {
		return goalSummary{}, false, fmt.Errorf("validate durable goal: %w", err)
	}
	snapshot, err := runtime.Snapshot(context.Background())
	if err != nil {
		return goalSummary{}, false, fmt.Errorf("refresh durable goal: %w", err)
	}
	return goalSummary{
		SessionID:       session.ID,
		GoalID:          snapshot.ID,
		State:           snapshot.State,
		Objective:       snapshot.Objective,
		CortexTaskID:    snapshot.Cortex.TaskID,
		CortexRevision:  snapshot.Cortex.Revision,
		ContinuationUse: snapshot.Usage.ContinuationTurns,
		EvalTokenUse:    snapshot.Usage.EvalTokens,
		UpdatedAt:       snapshot.UpdatedAt,
		SessionUpdated:  session.UpdatedAt,
		Snapshot:        snapshot,
	}, true, nil
}

func writeGoalUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage:")
	_, _ = fmt.Fprintln(writer, "  local-agent goal list [--limit 20] [--json]")
	_, _ = fmt.Fprintln(writer, "  local-agent goal show [--json] <session-id>")
	_, _ = fmt.Fprintln(writer, "  local-agent goal pending [--limit 20] [--json] <session-id>")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "These commands inspect durable goal state and never resume execution.")
}

func projectPendingControlItems(states []controlplane.State, sessionID int64, workspaceID string) ([]pendingControlSummary, error) {
	if sessionID <= 0 || strings.TrimSpace(workspaceID) == "" {
		return nil, errors.New("pending control scope is invalid")
	}
	if len(states) > controlplane.MaxListLimit {
		return nil, fmt.Errorf("pending control projection exceeds %d items", controlplane.MaxListLimit)
	}
	result := make([]pendingControlSummary, 0, len(states))
	seen := make(map[string]struct{}, len(states))
	for index, state := range states {
		if !state.Pending() {
			return nil, fmt.Errorf("item %d is already resolved", index)
		}
		if err := state.Item.Validate(); err != nil {
			return nil, fmt.Errorf("item %d: %w", index, err)
		}
		if state.Item.Identity.SessionID != sessionID || state.Item.Identity.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("item %q is outside the requested session scope", state.Item.ItemID)
		}
		if _, exists := seen[state.Item.ItemID]; exists {
			return nil, fmt.Errorf("duplicate item id %q", state.Item.ItemID)
		}
		seen[state.Item.ItemID] = struct{}{}
		result = append(result, pendingControlSummary{
			SessionID: sessionID, ItemID: state.Item.ItemID, Kind: state.Item.Kind,
			GoalID: state.Item.Identity.GoalID, ExecutionID: state.Item.Identity.ExecutionID,
			TurnID: state.Item.Identity.TurnID, Summary: state.Item.Summary,
			CreatedAt: state.Item.CreatedAt,
		})
	}
	return result, nil
}

func writePendingControlItems(writer io.Writer, items []pendingControlSummary) {
	if len(items) == 0 {
		goalFprintln(writer, "No pending decisions, approvals, or recovery items.")
		return
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	goalFprintln(table, "KIND\tITEM\tGOAL\tSUMMARY")
	for _, item := range items {
		goalFprintf(table, "%s\t%s\t%s\t%s\n",
			item.Kind,
			terminalSafeGoalText(item.ItemID),
			terminalSafeGoalText(fallbackGoalCLIText(item.GoalID, "—")),
			compactGoalObjective(item.Summary, 72),
		)
	}
	_ = table.Flush()
}

func fallbackGoalCLIText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func writeGoalList(writer io.Writer, summaries []goalSummary) {
	if len(summaries) == 0 {
		goalFprintln(writer, "No durable goals found in this workspace.")
		return
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	goalFprintln(table, "SESSION\tSTATE\tUPDATED\tOBJECTIVE")
	for _, summary := range summaries {
		goalFprintf(table, "%d\t%s\t%s\t%s\n",
			summary.SessionID, summary.State, summary.UpdatedAt.Local().Format("2006-01-02 15:04"),
			compactGoalObjective(summary.Objective, 72),
		)
	}
	_ = table.Flush()
}

func writeGoalDetail(writer io.Writer, summary goalSummary) {
	goalFprintf(writer, "Goal: %s\n", terminalSafeGoalText(summary.GoalID))
	goalFprintf(writer, "Session: %d\n", summary.SessionID)
	goalFprintf(writer, "State: %s\n", summary.State)
	goalFprintf(writer, "Objective: %s\n", terminalSafeGoalText(summary.Objective))
	if summary.Snapshot.StateReason != "" {
		goalFprintf(writer, "Reason: %s\n", terminalSafeGoalText(summary.Snapshot.StateReason))
	}
	goalFprintf(writer, "Acceptance: %d criteria\n", len(summary.Snapshot.AcceptanceCriteria))
	goalFprintf(writer, "Usage: %d continuation turns, %d eval tokens\n", summary.ContinuationUse, summary.EvalTokenUse)
	if summary.CortexTaskID != "" {
		goalFprintf(writer, "Cortex: %s @ revision %d\n", terminalSafeGoalText(summary.CortexTaskID), summary.CortexRevision)
	}
	goalFprintf(writer, "Updated: %s\n", summary.UpdatedAt.Local().Format(time.RFC3339))
}

func goalFprintf(writer io.Writer, format string, arguments ...any) {
	_, _ = fmt.Fprintf(writer, format, arguments...)
}

func goalFprintln(writer io.Writer, arguments ...any) {
	_, _ = fmt.Fprintln(writer, arguments...)
}

func compactGoalObjective(value string, limit int) string {
	value = strings.Join(strings.Fields(terminalSafeGoalText(value)), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func terminalSafeGoalText(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		default:
			if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
				return -1
			}
			return r
		}
	}, strings.ToValidUTF8(value, "�"))
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
)

const (
	sessionListLimit   = 100
	sessionExportLimit = 1000
)

type sessionExportStore interface {
	ListSessions(context.Context, db.ListSessionsParams) ([]db.Session, error)
	GetSession(context.Context, int64) (db.Session, error)
	GetSessionState(context.Context, int64) (string, error)
	ListSessionExecutionEvents(context.Context, int64, string, int) ([]execution.Event, error)
	ListExecutionRecoveryHazards(context.Context, int64, string, int64, int) ([]execution.State, error)
	ListControlStates(context.Context, controlplane.Query) ([]controlplane.State, error)
	GetSessionTokenStats(context.Context, int64) ([]db.TokenStat, error)
	GetSessionFileChanges(context.Context, int64) ([]db.FileChange, error)
	ListCheckpoints(context.Context, int64) ([]db.Checkpoint, error)
}

// sessionExportBundle is the machine-readable audit projection of one session.
// Every field is already persisted; export copies nothing new into the world.
type sessionExportBundle struct {
	Schema          string                          `json:"schema"`
	ExportedBy      string                          `json:"exported_by"`
	Session         db.Session                      `json:"session"`
	StateJSON       json.RawMessage                 `json:"state_json,omitempty"`
	ExecutionEvents []sessionExportExecutionEvent   `json:"execution_events"`
	ControlStates   []sessionExportControlState     `json:"control_states"`
	TokenStats      []db.TokenStat                  `json:"token_stats"`
	FileChanges     []db.FileChange                 `json:"file_changes"`
	Checkpoints     []sessionExportCheckpoint       `json:"checkpoints"`
	OpenIssues      []sessionExportOpenIssue        `json:"open_issues"`
	Truncations     map[string]sessionExportBounded `json:"truncations,omitempty"`
}

type sessionExportExecutionEvent struct {
	EventID         int64  `json:"event_id"`
	ExecutionID     string `json:"execution_id"`
	TurnID          string `json:"turn_id"`
	ToolName        string `json:"tool_name"`
	Kind            string `json:"kind"`
	EffectClass     string `json:"effect_class"`
	EventType       string `json:"event_type"`
	Approval        string `json:"approval"`
	ArgumentsSHA256 string `json:"arguments_sha256,omitempty"`
	ResultSHA256    string `json:"result_sha256,omitempty"`
	ResultReceipt   string `json:"result_receipt,omitempty"`
	Detail          string `json:"detail,omitempty"`
	OccurredAt      string `json:"occurred_at"`
}

type sessionExportControlState struct {
	ItemID      string `json:"item_id"`
	Kind        string `json:"kind"`
	ExecutionID string `json:"execution_id,omitempty"`
	TurnID      string `json:"turn_id,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Resolved    bool   `json:"resolved"`
	Outcome     string `json:"outcome,omitempty"`
	ResolvedBy  string `json:"resolved_by,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type sessionExportCheckpoint struct {
	ID       int64  `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	MsgCount int64  `json:"msg_count"`
	// Full checkpoint message bodies are intentionally omitted; they duplicate
	// the transcript and can be large. The metadata is enough for an audit.
	CreatedAt string `json:"created_at"`
}

type sessionExportOpenIssue struct {
	ExecutionID string `json:"execution_id"`
	ToolName    string `json:"tool_name"`
	EventType   string `json:"event_type"`
	EffectClass string `json:"effect_class"`
	Status      string `json:"status"`
	Remedy      string `json:"remedy"`
}

type sessionExportBounded struct {
	Returned int `json:"returned"`
	Limit    int `json:"limit"`
}

func handleSessionList(store sessionExportStore, workspace string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("session list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { writeSessionUsage(stdout) }
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	limit := flags.Int("limit", sessionListLimit, "maximum sessions to list")
	if code, done := flagParseExitCode(flags.Parse(args)); done {
		return code
	}
	if *limit <= 0 || *limit > sessionListLimit {
		executionFprintf(stderr, "session list: --limit must be between 1 and %d\n", sessionListLimit)
		return 2
	}
	sessions, err := store.ListSessions(context.Background(), db.ListSessionsParams{
		WorkspaceID: workspace, Limit: int64(*limit),
	})
	if err != nil {
		executionFprintf(stderr, "session list: %v\n", err)
		return 1
	}
	if *jsonOutput {
		if sessions == nil {
			sessions = []db.Session{}
		}
		if err := writeExecutionJSON(stdout, sessions); err != nil {
			executionFprintf(stderr, "session list: %v\n", err)
			return 1
		}
		return 0
	}
	if len(sessions) == 0 {
		executionFprintf(stdout, "No sessions in workspace %s.\n", relativizeHome(workspace))
		return 0
	}
	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	executionFprintf(table, "ID\tMODEL\tMODE\tUPDATED\tTITLE\n")
	for _, session := range sessions {
		executionFprintf(table, "%d\t%s\t%s\t%s\t%s\n",
			session.ID, terminalSafeGoalText(session.Model), terminalSafeGoalText(session.Mode),
			terminalSafeGoalText(session.UpdatedAt), terminalSafeGoalText(sessionListTitle(session.Title)))
	}
	_ = table.Flush()
	executionFprintln(stdout, "\nExport one with: local-agent session export SESSION_ID")
	return 0
}

func handleSessionExport(store sessionExportStore, workspace string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("session export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { writeSessionUsage(stdout) }
	format := flags.String("format", "both", "output format: jsonl, md, or both")
	outDir := flags.String("out", "", "output directory (default: ./local-agent-audit-<id>)")
	normalized, err := reorderFlagsBeforePositionals(args, map[string]bool{"format": true, "out": true})
	if err != nil {
		executionFprintf(stderr, "session export: %v\n", err)
		return 2
	}
	if code, done := flagParseExitCode(flags.Parse(normalized)); done {
		return code
	}
	if flags.NArg() != 1 {
		executionFprintln(stderr, "session export: provide SESSION_ID")
		return 2
	}
	sessionID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || sessionID <= 0 {
		executionFprintf(stderr, "session export: invalid session ID %q\n", flags.Arg(0))
		return 2
	}
	wantJSONL, wantMD := false, false
	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "both", "":
		wantJSONL, wantMD = true, true
	case "jsonl":
		wantJSONL = true
	case "md", "markdown":
		wantMD = true
	default:
		executionFprintf(stderr, "session export: unknown --format %q (want jsonl, md, or both)\n", *format)
		return 2
	}

	bundle, err := collectSessionExport(context.Background(), store, workspace, sessionID)
	if err != nil {
		executionFprintf(stderr, "session export: %v\n", err)
		return 1
	}

	directory := strings.TrimSpace(*outDir)
	if directory == "" {
		directory = fmt.Sprintf("local-agent-audit-%d", sessionID)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		executionFprintf(stderr, "session export: create output directory: %v\n", err)
		return 1
	}
	written := make([]string, 0, 2)
	if wantJSONL {
		path := filepath.Join(directory, fmt.Sprintf("session-%d.jsonl", sessionID))
		if err := writeSessionExportJSONL(path, bundle); err != nil {
			executionFprintf(stderr, "session export: %v\n", err)
			return 1
		}
		written = append(written, path)
	}
	if wantMD {
		path := filepath.Join(directory, fmt.Sprintf("session-%d-summary.md", sessionID))
		if err := writeSessionExportMarkdown(path, bundle); err != nil {
			executionFprintf(stderr, "session export: %v\n", err)
			return 1
		}
		written = append(written, path)
	}
	executionFprintf(stdout, "Exported session %d (%d execution events, %d open issue(s)).\n",
		sessionID, len(bundle.ExecutionEvents), len(bundle.OpenIssues))
	for _, path := range written {
		executionFprintf(stdout, "  %s\n", path)
	}
	if len(bundle.OpenIssues) > 0 {
		executionFprintln(stdout, "This session has unresolved executions; see the Open Issues section of the summary.")
	}
	return 0
}

func collectSessionExport(ctx context.Context, store sessionExportStore, workspace string, sessionID int64) (sessionExportBundle, error) {
	session, err := store.GetSession(ctx, sessionID)
	if err != nil {
		return sessionExportBundle{}, fmt.Errorf("read session %d: %w", sessionID, err)
	}
	if session.WorkspaceID != workspace {
		return sessionExportBundle{}, fmt.Errorf("session %d belongs to a different workspace", sessionID)
	}
	bundle := sessionExportBundle{
		Schema: "local-agent.session-export.v1", ExportedBy: "local-agent session export",
		Session: session, Truncations: map[string]sessionExportBounded{},
	}

	if raw, stateErr := store.GetSessionState(ctx, sessionID); stateErr == nil {
		if json.Valid([]byte(raw)) {
			bundle.StateJSON = json.RawMessage(raw)
		}
	} else if !errors.Is(stateErr, db.ErrSessionStateNotFound) {
		return sessionExportBundle{}, fmt.Errorf("read session %d state: %w", sessionID, stateErr)
	}

	events, err := store.ListSessionExecutionEvents(ctx, sessionID, workspace, sessionExportLimit)
	if err != nil {
		return sessionExportBundle{}, fmt.Errorf("list execution events: %w", err)
	}
	if len(events) == sessionExportLimit {
		bundle.Truncations["execution_events"] = sessionExportBounded{Returned: len(events), Limit: sessionExportLimit}
	}
	for _, event := range events {
		bundle.ExecutionEvents = append(bundle.ExecutionEvents, sessionExportExecutionEvent{
			EventID: event.ID, ExecutionID: event.Identity.ExecutionID, TurnID: event.Identity.TurnID,
			ToolName: event.Identity.ToolName, Kind: string(event.Identity.Kind),
			EffectClass: string(event.Identity.EffectClass), EventType: string(event.Type),
			Approval: string(event.Approval), ArgumentsSHA256: event.ArgumentsSHA256,
			ResultSHA256: event.ResultSHA256, ResultReceipt: event.ResultReceipt,
			Detail: event.Detail, OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339Nano),
		})
	}

	states, err := store.ListControlStates(ctx, controlplane.Query{
		SessionID: sessionID, WorkspaceID: workspace, Limit: controlplane.MaxListLimit,
	})
	if err != nil {
		return sessionExportBundle{}, fmt.Errorf("list control states: %w", err)
	}
	for _, state := range states {
		view := sessionExportControlState{
			ItemID: state.Item.ItemID, Kind: string(state.Item.Kind),
			ExecutionID: state.Item.Identity.ExecutionID, TurnID: state.Item.Identity.TurnID,
			Summary: state.Item.Summary, Resolved: !state.Pending(),
			CreatedAt: state.Item.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
		if state.Resolution != nil {
			view.Outcome = string(state.Resolution.Outcome)
			view.ResolvedBy = state.Resolution.ResolvedBy
		}
		bundle.ControlStates = append(bundle.ControlStates, view)
	}

	if stats, statsErr := store.GetSessionTokenStats(ctx, sessionID); statsErr == nil {
		bundle.TokenStats = stats
	}
	if changes, changesErr := store.GetSessionFileChanges(ctx, sessionID); changesErr == nil {
		bundle.FileChanges = changes
	}
	if checkpoints, cpErr := store.ListCheckpoints(ctx, sessionID); cpErr == nil {
		for _, cp := range checkpoints {
			bundle.Checkpoints = append(bundle.Checkpoints, sessionExportCheckpoint{
				ID: cp.ID, Label: cp.Label, Kind: cp.Kind, MsgCount: cp.MsgCount, CreatedAt: cp.CreatedAt,
			})
		}
	}

	// Open issues come from the SAME authoritative hazard projection the agent
	// runtime uses to decide whether to block: it applies the snapshot cursor
	// (so a completed/failed effect newer than the saved transcript is flagged),
	// the reconciliation overlay (so only validly-reconciled executions are
	// cleared), and is a bounded latest-state-per-execution view (immune to the
	// timeline's event truncation).
	cursor := decodeExportExecutionCursor(bundle.StateJSON)
	hazards, err := store.ListExecutionRecoveryHazards(ctx, sessionID, workspace, cursor, maxSessionExportHazards)
	if err != nil {
		return sessionExportBundle{}, fmt.Errorf("project execution recovery hazards: %w", err)
	}
	for _, hazard := range hazards {
		status, remedy := openIssueStatus(hazard.Latest.Type, hazard.Identity.EffectClass, sessionID)
		if status == "" {
			continue
		}
		bundle.OpenIssues = append(bundle.OpenIssues, sessionExportOpenIssue{
			ExecutionID: hazard.Identity.ExecutionID, ToolName: hazard.Identity.ToolName,
			EventType: string(hazard.Latest.Type), EffectClass: string(hazard.Identity.EffectClass),
			Status: status, Remedy: remedy,
		})
	}
	return bundle, nil
}

const maxSessionExportHazards = 100

// openIssueStatus classifies a recovery hazard the same way the agent runtime's
// executionRuntime does, and names the exact remedy. Unknown/started hazards
// need reconciliation evidence; an answered terminal newer than the snapshot
// needs projection repair, not reconciliation.
func openIssueStatus(eventType execution.EventType, effect execution.EffectClass, sessionID int64) (status, remedy string) {
	switch {
	case eventType == execution.EventOutcomeUnknown:
		return "UNRESOLVED — outcome unknown, needs reconciliation",
			fmt.Sprintf("local-agent execution recover %d --all", sessionID)
	case eventType == execution.EventStarted && effect != execution.EffectReadOnly:
		return "UNRESOLVED — started without a terminal receipt",
			fmt.Sprintf("local-agent execution recover %d --all", sessionID)
	case (eventType == execution.EventCompleted || eventType == execution.EventFailed) && effect != execution.EffectReadOnly:
		return "UNPROJECTED — answered effect newer than the saved transcript",
			fmt.Sprintf("local-agent session repair %d", sessionID)
	default:
		return "", ""
	}
}

// decodeExportExecutionCursor reads the persisted session snapshot cursor. A
// missing or malformed cursor falls back to 0, which over-reports post-cursor
// answered effects rather than hiding a wedge — the safe direction for an audit.
func decodeExportExecutionCursor(stateJSON json.RawMessage) int64 {
	if len(stateJSON) == 0 {
		return 0
	}
	var envelope struct {
		ExecutionCursor int64 `json:"execution_cursor"`
	}
	if err := json.Unmarshal(stateJSON, &envelope); err != nil || envelope.ExecutionCursor < 0 {
		return 0
	}
	return envelope.ExecutionCursor
}

func writeSessionExportJSONL(path string, bundle sessionExportBundle) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open JSONL export: %w", err)
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)
	emit := func(kind string, value any) error {
		return encoder.Encode(map[string]any{"kind": kind, "value": value})
	}
	if err := emit("session", bundle.Session); err != nil {
		return err
	}
	if len(bundle.StateJSON) > 0 {
		if err := emit("state_json", bundle.StateJSON); err != nil {
			return err
		}
	}
	for _, event := range bundle.ExecutionEvents {
		if err := emit("execution_event", event); err != nil {
			return err
		}
	}
	for _, state := range bundle.ControlStates {
		if err := emit("control_state", state); err != nil {
			return err
		}
	}
	for _, stat := range bundle.TokenStats {
		if err := emit("token_stat", stat); err != nil {
			return err
		}
	}
	for _, change := range bundle.FileChanges {
		if err := emit("file_change", change); err != nil {
			return err
		}
	}
	for _, checkpoint := range bundle.Checkpoints {
		if err := emit("checkpoint", checkpoint); err != nil {
			return err
		}
	}
	for _, issue := range bundle.OpenIssues {
		if err := emit("open_issue", issue); err != nil {
			return err
		}
	}
	return nil
}

func writeSessionExportMarkdown(path string, bundle sessionExportBundle) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session %d audit\n\n", bundle.Session.ID)
	fmt.Fprintf(&b, "- **Title:** %s\n", markdownSafe(bundle.Session.Title))
	fmt.Fprintf(&b, "- **Workspace:** %s\n", markdownSafe(relativizeHome(bundle.Session.WorkspaceID)))
	fmt.Fprintf(&b, "- **Model:** %s\n", markdownSafe(bundle.Session.Model))
	fmt.Fprintf(&b, "- **Mode:** %s\n", markdownSafe(bundle.Session.Mode))
	fmt.Fprintf(&b, "- **Created:** %s\n", markdownSafe(bundle.Session.CreatedAt))
	fmt.Fprintf(&b, "- **Updated:** %s\n", markdownSafe(bundle.Session.UpdatedAt))
	fmt.Fprintf(&b, "- **Execution events:** %d\n", len(bundle.ExecutionEvents))
	fmt.Fprintf(&b, "- **Schema:** `%s`\n\n", bundle.Schema)

	b.WriteString("## Open issues\n\n")
	if len(bundle.OpenIssues) == 0 {
		b.WriteString("None — every execution is projected and cleanly terminal (no unresolved, ")
		b.WriteString("started-without-receipt, or unprojected-answered hazards block continuation).\n\n")
	} else {
		b.WriteString("These executions block continuation. Status and the exact remedy per row:\n\n")
		b.WriteString("| Execution | Tool | Latest event | Status | Remedy |\n|---|---|---|---|---|\n")
		for _, issue := range bundle.OpenIssues {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | `%s` |\n",
				markdownSafe(issue.ExecutionID), markdownSafe(issue.ToolName),
				markdownSafe(issue.EventType), markdownSafe(issue.Status), markdownSafe(issue.Remedy))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Execution timeline\n\n")
	if len(bundle.ExecutionEvents) == 0 {
		b.WriteString("No execution events recorded.\n\n")
	} else {
		b.WriteString("| Event | Tool | Type | Effect | Approval | Occurred |\n|---|---|---|---|---|---|\n")
		for _, event := range bundle.ExecutionEvents {
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s |\n",
				event.EventID, markdownSafe(event.ToolName), markdownSafe(event.EventType),
				markdownSafe(event.EffectClass), markdownSafe(event.Approval), markdownSafe(event.OccurredAt))
		}
		b.WriteString("\n")
	}

	if len(bundle.ControlStates) > 0 {
		b.WriteString("## Control-plane records\n\n")
		b.WriteString("| Item | Kind | Resolved | Outcome |\n|---|---|---|---|\n")
		for _, state := range bundle.ControlStates {
			fmt.Fprintf(&b, "| `%s` | %s | %t | %s |\n",
				markdownSafe(state.ItemID), markdownSafe(state.Kind), state.Resolved, markdownSafe(state.Outcome))
		}
		b.WriteString("\n")
	}

	if len(bundle.FileChanges) > 0 {
		b.WriteString("## File changes\n\n")
		b.WriteString("| Path | Tool | +Added | -Removed |\n|---|---|---|---|\n")
		for _, change := range bundle.FileChanges {
			fmt.Fprintf(&b, "| %s | %s | %d | %d |\n",
				markdownSafe(relativizeHome(change.FilePath)), markdownSafe(change.ToolName), change.Added, change.Removed)
		}
		b.WriteString("\n")
	}

	if len(bundle.Truncations) > 0 {
		b.WriteString("## Truncations\n\nSome sections hit their export bound and may be incomplete:\n\n")
		for section, bound := range bundle.Truncations {
			fmt.Fprintf(&b, "- **%s:** returned %d (limit %d)\n", section, bound.Returned, bound.Limit)
		}
		b.WriteString("\n")
	}

	b.WriteString("---\nThe full machine-readable timeline (including state_json and receipt hashes) is in the JSONL file next to this summary.\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write markdown summary: %w", err)
	}
	return nil
}

func sessionListTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "(untitled)"
	}
	if len([]rune(title)) > 48 {
		return string([]rune(title)[:45]) + "..."
	}
	return title
}

// reorderFlagsBeforePositionals moves flags ahead of positional arguments so
// `SESSION_ID --flag` parses like `--flag SESSION_ID`, while keeping each
// value-taking flag bound to its value. valueFlags names the flags that consume
// the following argument (unless given as --flag=value).
func reorderFlagsBeforePositionals(args []string, valueFlags map[string]bool) ([]string, error) {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		argument := args[i]
		if !strings.HasPrefix(argument, "-") {
			positional = append(positional, argument)
			continue
		}
		name := strings.TrimLeft(argument, "-")
		bare, _, hasValue := strings.Cut(name, "=")
		flags = append(flags, argument)
		if valueFlags[bare] && !hasValue {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--%s requires a value", bare)
			}
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...), nil
}

// relativizeHome rewrites the caller's home directory prefix to ~ so an export
// shared outward does not leak the operator's absolute home path.
func relativizeHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

// markdownSafe strips characters that would break a table cell or inject
// formatting from a value that may contain server-authored text.
func markdownSafe(value string) string {
	replacer := strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "`", "'")
	return replacer.Replace(terminalSafeGoalText(value))
}

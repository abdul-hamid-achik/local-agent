package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestHeadlessSessionTitleUsesFirstPromptLine(t *testing.T) {
	t.Parallel()

	if got := headlessSessionTitle("  inspect the release\nthen summarize  "); got != "inspect the release" {
		t.Fatalf("headlessSessionTitle() = %q", got)
	}
}

func TestHeadlessSessionTitleBoundsUnicodeByRunes(t *testing.T) {
	t.Parallel()

	got := headlessSessionTitle(strings.Repeat("界", 80))
	if !utf8.ValidString(got) {
		t.Fatalf("headlessSessionTitle() returned invalid UTF-8: %q", got)
	}
	if count := len([]rune(got)); count != 72 {
		t.Fatalf("headlessSessionTitle() rune count = %d, want 72", count)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("headlessSessionTitle() = %q, want ellipsis suffix", got)
	}
}

func TestHeadlessSessionTitleNamesBlankPrompt(t *testing.T) {
	t.Parallel()

	if got := headlessSessionTitle(" \n\t"); !strings.HasPrefix(got, "Headless session ") {
		t.Fatalf("headlessSessionTitle() = %q", got)
	}
}

func TestHeadlessSnapshotCursorRequiresProjectedCompletedReceipt(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "headless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspace := t.TempDir()
	session, err := store.CreateSession(context.Background(), db.CreateSessionParams{Title: "headless", WorkspaceID: workspace})
	if err != nil {
		t.Fatal(err)
	}
	identity := execution.Identity{
		SessionID: session.ID, WorkspaceID: workspace, RunID: "run_headless", TurnID: "turn_headless",
		ExecutionID: "exec_headless", IdempotencyKey: "idem_headless", CanonicalCallID: "call_headless",
		ToolName: "write", Iteration: 1, Ordinal: 1, Kind: execution.KindBuiltin, EffectClass: execution.Effectful,
	}
	requested := execution.Event{
		Identity: identity, Type: execution.EventRequested, Approval: execution.ApprovalNotApplicable,
		ArgumentsSHA256: execution.HashText("arguments"),
	}
	approved := requested
	approved.Type = execution.EventApproved
	approved.Approval = execution.ApprovalEmbedding
	started := requested
	started.Type = execution.EventStarted
	completed := requested
	completed.Type = execution.EventCompleted
	for _, event := range []execution.Event{requested, approved, started} {
		if _, _, err := store.AppendExecutionEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	terminal, _, err := store.AppendExecutionEvent(context.Background(), completed)
	if err != nil {
		t.Fatal(err)
	}

	missing := agent.New(nil, nil, 4096)
	if cursor, err := headlessSnapshotExecutionCursor(context.Background(), store, missing, session.ID, workspace, 0); err == nil || cursor != 0 {
		t.Fatalf("missing receipt cursor/error = %d, %v", cursor, err)
	}
	projected := agent.New(nil, nil, 4096)
	projected.AppendMessage(llm.Message{Role: "tool", ToolCallID: identity.CanonicalCallID, ToolName: identity.ToolName, Content: "done"})
	if cursor, err := headlessSnapshotExecutionCursor(context.Background(), store, projected, session.ID, workspace, 0); err != nil || cursor != terminal.ID {
		t.Fatalf("projected receipt cursor/error = %d, %v; want %d", cursor, err, terminal.ID)
	}
}

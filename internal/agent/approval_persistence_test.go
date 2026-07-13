package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
	executionpkg "github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
)

type approvalPersistenceOutput struct {
	outputRecorder
	system []string
}

func (o *approvalPersistenceOutput) SystemMessage(message string) {
	o.system = append(o.system, message)
}

func TestAllowSessionIsExactRequestScopedAndDoesNotPersistGlobally(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	databasePath := filepath.Join(t.TempDir(), "always-approval.db")
	store, err := db.OpenPath(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	scope := New(nil, nil, 4096)
	scope.SetWorkDir(workDir)
	workspaceID, err := scope.checkpointWorkspaceID()
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, db.CreateSessionParams{
		Title: "session approval", Model: "test-model", Mode: "BUILD", WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatal(err)
	}

	var approvalPrompts atomic.Int64
	firstClient := &scriptedClient{responses: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "write-first", Name: "write", Arguments: map[string]any{"path": "same.txt", "content": "same"}}}, Done: true}},
		{{ToolCalls: []llm.ToolCall{{ID: "write-repeat", Name: "write", Arguments: map[string]any{"path": "same.txt", "content": "same"}}}, Done: true}},
		{{Text: "first complete", Done: true}},
	}}
	first := configuredPersistentApprovalAgent(firstClient, store, permission.NewChecker(store, false), workDir, session.ID, 0)
	first.SetApprovalCallback(func(request permission.ApprovalRequest) {
		approvalPrompts.Add(1)
		request.Response <- permission.AllowSession()
	})
	first.AddUserMessage("write the same file twice")
	if err := first.RunTurn(ctx, &approvalPersistenceOutput{}, "turn_always_first"); err != nil {
		t.Fatal(err)
	}
	if approvalPrompts.Load() != 1 {
		t.Fatalf("first turn approval prompts = %d, want 1", approvalPrompts.Load())
	}
	if data, err := os.ReadFile(filepath.Join(workDir, "same.txt")); err != nil || string(data) != "same" {
		t.Fatalf("first approved effect = %q err=%v", data, err)
	}
	if policy := permission.NewChecker(store, false).Check("write"); policy != permission.PolicyAsk {
		t.Fatalf("session approval persisted global write policy = %q", policy)
	}

	firstHazards, err := store.ListExecutionRecoveryHazards(ctx, session.ID, workspaceID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstHazards) != 2 {
		t.Fatalf("first completed execution states = %d, want 2", len(firstHazards))
	}
	for _, state := range firstHazards {
		firstEvents, eventsErr := store.ListExecutionEvents(ctx, session.ID, workspaceID, state.Identity.ExecutionID, 20)
		if eventsErr != nil {
			t.Fatal(eventsErr)
		}
		assertExecutionApproval(t, firstEvents, executionpkg.ApprovalSession)
	}
	firstCursor, err := store.LatestExecutionEventID(ctx, session.ID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}

	secondClient := &scriptedClient{responses: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "write-after-restart", Name: "write", Arguments: map[string]any{"path": "same.txt", "content": "same"}}}, Done: true}},
		{{Text: "second complete", Done: true}},
	}}
	restartedChecker := permission.NewChecker(store, false)
	second := configuredPersistentApprovalAgent(secondClient, store, restartedChecker, workDir, session.ID, firstCursor)
	second.SetApprovalCallback(func(request permission.ApprovalRequest) {
		approvalPrompts.Add(1)
		request.Response <- permission.AllowOnce()
	})
	second.AddUserMessage("write after restart")
	if err := second.RunTurn(ctx, &approvalPersistenceOutput{}, "turn_always_second"); err != nil {
		t.Fatal(err)
	}
	if approvalPrompts.Load() != 2 {
		t.Fatalf("session approval survived agent restart; total prompts=%d", approvalPrompts.Load())
	}
	if data, err := os.ReadFile(filepath.Join(workDir, "same.txt")); err != nil || string(data) != "same" {
		t.Fatalf("second policy-approved effect = %q err=%v", data, err)
	}
	secondHazards, err := store.ListExecutionRecoveryHazards(ctx, session.ID, workspaceID, firstCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondHazards) != 1 {
		t.Fatalf("second completed execution states = %d, want 1", len(secondHazards))
	}
	secondEvents, err := store.ListExecutionEvents(ctx, session.ID, workspaceID, secondHazards[0].Identity.ExecutionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	assertExecutionApproval(t, secondEvents, executionpkg.ApprovalOnce)
}

func TestAllowSessionDoesNotAuthorizeChangedArguments(t *testing.T) {
	client := &scriptedClient{responses: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "write-one", Name: "write", Arguments: map[string]any{"path": "approved.txt", "content": "one"}}}, Done: true}},
		{{ToolCalls: []llm.ToolCall{{ID: "write-two", Name: "write", Arguments: map[string]any{"path": "approved.txt", "content": "two"}}}, Done: true}},
		{{Text: "done", Done: true}},
	}}
	ledger := &fakeExecutionLedger{}
	ag, workDir := newLedgerAgent(t, client, nil, ledger)
	ag.SetPermissionChecker(permission.NewChecker(nil, false))
	var prompts atomic.Int64
	ag.SetApprovalCallback(func(request permission.ApprovalRequest) {
		prompts.Add(1)
		request.Response <- permission.AllowSession()
	})
	if err := ag.RunTurn(context.Background(), &approvalPersistenceOutput{}, "turn_scoped_session"); err != nil {
		t.Fatal(err)
	}
	if prompts.Load() != 2 {
		t.Fatalf("changed canonical arguments reused session grant; prompts=%d", prompts.Load())
	}
	if data, err := os.ReadFile(filepath.Join(workDir, "approved.txt")); err != nil || string(data) != "two" {
		t.Fatalf("final approved effect = %q err=%v", data, err)
	}
	approved := 0
	for _, event := range ledger.snapshot() {
		if event.Type == executionpkg.EventApproved && event.Approval == executionpkg.ApprovalSession {
			approved++
		}
	}
	if approved != 2 {
		t.Fatalf("session-approved events = %d, want 2", approved)
	}
}

func configuredPersistentApprovalAgent(client llm.Client, ledger *db.Store, checker *permission.Checker, workDir string, sessionID, cursor int64) *Agent {
	ag := New(client, nil, 4096)
	ag.SetWorkDir(workDir)
	ag.SetModeContext("test", BuildToolPolicy())
	ag.SetPermissionChecker(checker)
	ag.SetExecutionLedger(ledger)
	ag.SetExecutionSessionID(sessionID)
	ag.SetExecutionSnapshotCursor(cursor)
	ag.RequireExecutionLedger(true)
	return ag
}

func assertExecutionApproval(t *testing.T, events []executionpkg.Event, want executionpkg.Approval) {
	t.Helper()
	for _, event := range events {
		if event.Type == executionpkg.EventApproved {
			if event.Approval != want {
				t.Fatalf("approved event = %q, want %q", event.Approval, want)
			}
			return
		}
	}
	t.Fatalf("execution events contain no approved receipt: %#v", events)
}

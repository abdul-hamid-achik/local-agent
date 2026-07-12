package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestAlwaysApprovalPersistsAcrossCheckerRestartAndSkipsSecondPrompt(t *testing.T) {
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
		Title: "always approval", Model: "test-model", Mode: "BUILD", WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatal(err)
	}

	var approvalPrompts atomic.Int64
	firstClient := &scriptedClient{responses: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "write-first", Name: "write", Arguments: map[string]any{"path": "first.txt", "content": "first"}}}, Done: true}},
		{{Text: "first complete", Done: true}},
	}}
	first := configuredPersistentApprovalAgent(firstClient, store, permission.NewChecker(store, false), workDir, session.ID, 0)
	first.SetApprovalCallback(func(request permission.ApprovalRequest) {
		approvalPrompts.Add(1)
		request.Response <- permission.ApprovalResponse{Allowed: true, Always: true}
	})
	first.AddUserMessage("write the first file")
	if err := first.RunTurn(ctx, &approvalPersistenceOutput{}, "turn_always_first"); err != nil {
		t.Fatal(err)
	}
	if approvalPrompts.Load() != 1 {
		t.Fatalf("first turn approval prompts = %d, want 1", approvalPrompts.Load())
	}
	if data, err := os.ReadFile(filepath.Join(workDir, "first.txt")); err != nil || string(data) != "first" {
		t.Fatalf("first approved effect = %q err=%v", data, err)
	}
	if policy := permission.NewChecker(store, false).Check("write"); policy != permission.PolicyAllow {
		t.Fatalf("persisted write policy = %q, want allow", policy)
	}

	firstHazards, err := store.ListExecutionRecoveryHazards(ctx, session.ID, workspaceID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstHazards) != 1 {
		t.Fatalf("first completed execution states = %d, want 1", len(firstHazards))
	}
	firstEvents, err := store.ListExecutionEvents(ctx, session.ID, workspaceID, firstHazards[0].Identity.ExecutionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	assertExecutionApproval(t, firstEvents, executionpkg.ApprovalAlways)
	firstCursor, err := store.LatestExecutionEventID(ctx, session.ID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}

	secondClient := &scriptedClient{responses: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "write-second", Name: "write", Arguments: map[string]any{"path": "second.txt", "content": "second"}}}, Done: true}},
		{{Text: "second complete", Done: true}},
	}}
	restartedChecker := permission.NewChecker(store, false)
	second := configuredPersistentApprovalAgent(secondClient, store, restartedChecker, workDir, session.ID, firstCursor)
	second.SetApprovalCallback(func(request permission.ApprovalRequest) {
		approvalPrompts.Add(1)
		request.Response <- permission.ApprovalResponse{Allowed: true}
	})
	second.AddUserMessage("write the second file")
	if err := second.RunTurn(ctx, &approvalPersistenceOutput{}, "turn_always_second"); err != nil {
		t.Fatal(err)
	}
	if approvalPrompts.Load() != 1 {
		t.Fatalf("persisted allow prompted again; total prompts=%d", approvalPrompts.Load())
	}
	if data, err := os.ReadFile(filepath.Join(workDir, "second.txt")); err != nil || string(data) != "second" {
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
	assertExecutionApproval(t, secondEvents, executionpkg.ApprovalPolicy)
}

func TestAlwaysApprovalPersistenceFailureWarnsAndDoesNotSurviveCheckerRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "failed-always-approval.db")
	store, err := db.OpenPath(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	checker := permission.NewChecker(store, false)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	client := &scriptedClient{responses: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "write", Name: "write", Arguments: map[string]any{"path": "approved.txt", "content": "approved"}}}, Done: true}},
		{{Text: "done", Done: true}},
	}}
	ledger := &fakeExecutionLedger{}
	ag, workDir := newLedgerAgent(t, client, nil, ledger)
	ag.SetPermissionChecker(checker)
	ag.SetApprovalCallback(func(request permission.ApprovalRequest) {
		request.Response <- permission.ApprovalResponse{Allowed: true, Always: true}
	})
	out := &approvalPersistenceOutput{}
	if err := ag.RunTurn(context.Background(), out, "turn_failed_always_persist"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "approved.txt")); err != nil {
		t.Fatalf("explicitly approved effect did not run: %v", err)
	}
	if !strings.Contains(strings.Join(out.system, "\n"), "approval was granted, but could not be persisted") {
		t.Fatalf("persistence failure warning = %#v", out.system)
	}
	if policy := checker.Check("write"); policy != permission.PolicyAsk {
		t.Fatalf("failed persistence changed the live checker to %q", policy)
	}
	assertExecutionApproval(t, ledger.snapshot(), executionpkg.ApprovalAlways)

	reopened, err := db.OpenPath(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if policy := permission.NewChecker(reopened, false).Check("write"); policy != permission.PolicyAsk {
		t.Fatalf("failed persistence survived checker restart as %q", policy)
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

package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenPath(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

func TestOpenAndMigrate(t *testing.T) {
	s := testStore(t)
	// Verify the store is functional by counting sessions.
	ctx := context.Background()
	count, err := s.CountSessions(ctx)
	if err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 sessions, got %d", count)
	}
}

func TestSessionCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Create.
	sess, err := s.CreateSession(ctx, CreateSessionParams{
		Title: "Test Session",
		Model: "qwen3.5:4b",
		Mode:  "BUILD",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.Title != "Test Session" {
		t.Fatalf("expected title 'Test Session', got %q", sess.Title)
	}

	// Read.
	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.Model != "qwen3.5:4b" {
		t.Fatalf("expected model 'qwen3.5:4b', got %q", got.Model)
	}

	// Create message.
	msg, err := s.CreateSessionMessage(ctx, CreateSessionMessageParams{
		SessionID: sess.ID,
		Role:      "user",
		Content:   "Hello world",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	if msg.Content != "Hello world" {
		t.Fatalf("expected content 'Hello world', got %q", msg.Content)
	}

	// List messages.
	msgs, err := s.GetSessionMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	// Delete.
	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	count, err := s.CountSessions(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 after delete, got %d", count)
	}
}

func TestToolPermissions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Upsert.
	perm, err := s.UpsertToolPermission(ctx, UpsertToolPermissionParams{
		ToolName: "bash",
		Policy:   "allow",
	})
	if err != nil {
		t.Fatalf("upsert permission: %v", err)
	}
	if perm.Policy != "allow" {
		t.Fatalf("expected policy 'allow', got %q", perm.Policy)
	}

	// Update via upsert.
	perm2, err := s.UpsertToolPermission(ctx, UpsertToolPermissionParams{
		ToolName: "bash",
		Policy:   "deny",
	})
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	if perm2.Policy != "deny" {
		t.Fatalf("expected policy 'deny', got %q", perm2.Policy)
	}

	// List.
	perms, err := s.ListToolPermissions(ctx)
	if err != nil {
		t.Fatalf("list permissions: %v", err)
	}
	if len(perms) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(perms))
	}

	// Reset.
	if err := s.ResetToolPermissions(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	perms, err = s.ListToolPermissions(ctx)
	if err != nil {
		t.Fatalf("list after reset: %v", err)
	}
	if len(perms) != 0 {
		t.Fatalf("expected 0 after reset, got %d", len(perms))
	}
}

func TestTokenStats(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	sess, err := s.CreateSession(ctx, CreateSessionParams{
		Title: "Stats Test",
		Model: "qwen3.5:4b",
		Mode:  "ASK",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Record usage.
	_, err = s.RecordTokenUsage(ctx, RecordTokenUsageParams{
		SessionID:    sess.ID,
		Turn:         1,
		EvalCount:    100,
		PromptTokens: 500,
		Model:        "qwen3.5:4b",
	})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}

	_, err = s.RecordTokenUsage(ctx, RecordTokenUsageParams{
		SessionID:    sess.ID,
		Turn:         2,
		EvalCount:    200,
		PromptTokens: 600,
		Model:        "qwen3.5:4b",
	})
	if err != nil {
		t.Fatalf("record usage 2: %v", err)
	}

	// Get totals.
	totals, err := s.GetSessionTotalTokens(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get totals: %v", err)
	}
	if totals.TotalEval != 300 {
		t.Fatalf("expected total_eval 300, got %v", totals.TotalEval)
	}
	if totals.TotalPrompt != 1100 {
		t.Fatalf("expected total_prompt 1100, got %v", totals.TotalPrompt)
	}
	if totals.TurnCount != 2 {
		t.Fatalf("expected turn_count 2, got %v", totals.TurnCount)
	}
}

func TestFileChanges(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	sess, err := s.CreateSession(ctx, CreateSessionParams{
		Title: "Changes Test",
		Model: "qwen3.5:4b",
		Mode:  "BUILD",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err = s.RecordFileChange(ctx, RecordFileChangeParams{
		SessionID: sess.ID,
		FilePath:  "main.go",
		ToolName:  "write_file",
		Added:     10,
		Removed:   3,
	})
	if err != nil {
		t.Fatalf("record change: %v", err)
	}

	_, err = s.RecordFileChange(ctx, RecordFileChangeParams{
		SessionID: sess.ID,
		FilePath:  "main.go",
		ToolName:  "write_file",
		Added:     5,
		Removed:   2,
	})
	if err != nil {
		t.Fatalf("record change 2: %v", err)
	}

	summary, err := s.GetSessionFileChangeSummary(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	if len(summary) != 1 {
		t.Fatalf("expected 1 file, got %d", len(summary))
	}
	if summary[0].TotalAdded != 15 {
		t.Fatalf("expected 15 added, got %d", summary[0].TotalAdded)
	}
}

func TestDoubleOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	s1, err := OpenPath(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	defer s1.Close()

	// Running migrations again should be idempotent (IF NOT EXISTS).
	s2, err := OpenPath(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()
}

func TestOpenDefault(t *testing.T) {
	// Ensure Open() doesn't panic. It writes to ~/.config/local-agent/
	// which should be fine in CI/dev.
	if os.Getenv("CI") != "" {
		t.Skip("skip in CI to avoid side effects")
	}
	s, err := Open()
	if err != nil {
		t.Fatalf("open default: %v", err)
	}
	s.Close()
}

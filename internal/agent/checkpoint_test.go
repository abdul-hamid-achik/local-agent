package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestCheckpointRoundTrip(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	a := New(nil, nil, 8192)
	a.SetCheckpointStore(store, 0)

	// Build an initial conversation and checkpoint it.
	a.AddUserMessage("first question")
	a.AppendMessage(llm.Message{Role: "assistant", Content: "first answer"})
	id, err := a.CreateCheckpoint(context.Background(), "snapshot", db.CheckpointManual)
	if err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a real checkpoint id")
	}

	// Diverge: add more turns.
	a.AddUserMessage("second question")
	a.AppendMessage(llm.Message{Role: "assistant", Content: "second answer"})
	if got := len(a.Messages()); got != 4 {
		t.Fatalf("expected 4 messages before restore, got %d", got)
	}

	// Restore should rewind to the 2-message snapshot.
	n, err := a.RestoreCheckpoint(context.Background(), id)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 messages restored, got %d", n)
	}
	msgs := a.Messages()
	if len(msgs) != 2 || msgs[0].Content != "first question" || msgs[1].Content != "first answer" {
		t.Fatalf("restored history mismatch: %+v", msgs)
	}

	list, err := a.ListCheckpoints(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(list))
	}
}

func TestCheckpointDisabledWithoutStore(t *testing.T) {
	a := New(nil, nil, 8192)
	a.AddUserMessage("hi")

	id, err := a.CreateCheckpoint(context.Background(), "x", db.CheckpointManual)
	if err != nil {
		t.Fatalf("create without store should be a no-op, got: %v", err)
	}
	if id != 0 {
		t.Fatalf("expected id 0 when disabled, got %d", id)
	}
	if _, err := a.RestoreCheckpoint(context.Background(), 1); err == nil {
		t.Fatal("restore without store should error")
	}
}

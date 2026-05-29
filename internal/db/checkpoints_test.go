package db

import (
	"context"
	"errors"
	"testing"
)

func TestCheckpointCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	id, err := s.CreateCheckpoint(ctx, 0, "first", CheckpointManual, `[{"role":"user","content":"hi"}]`, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := s.GetCheckpoint(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Label != "first" || got.Kind != CheckpointManual || got.MsgCount != 1 {
		t.Fatalf("unexpected checkpoint: %+v", got)
	}
	if got.Messages != `[{"role":"user","content":"hi"}]` {
		t.Fatalf("messages payload mismatch: %q", got.Messages)
	}

	list, err := s.ListCheckpoints(ctx, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(list))
	}
	// Listing omits the payload to stay cheap.
	if list[0].Messages != "" {
		t.Fatalf("expected empty payload in listing, got %q", list[0].Messages)
	}

	if err := s.DeleteCheckpoint(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetCheckpoint(ctx, id); !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestCheckpointDefaults(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id, err := s.CreateCheckpoint(ctx, 0, "", "", "", 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetCheckpoint(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Kind != CheckpointManual {
		t.Errorf("empty kind should default to manual, got %q", got.Kind)
	}
	if got.Messages != "[]" {
		t.Errorf("empty messages should default to [], got %q", got.Messages)
	}
}

func TestPruneCheckpoints(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := s.CreateCheckpoint(ctx, 7, "cp", CheckpointPreCompaction, "[]", 0); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	// A different session's checkpoints must be untouched by pruning session 7.
	otherID, _ := s.CreateCheckpoint(ctx, 99, "other", CheckpointManual, "[]", 0)

	if err := s.PruneCheckpoints(ctx, 7, 2); err != nil {
		t.Fatalf("prune: %v", err)
	}

	list, err := s.ListCheckpoints(ctx, 7)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 after prune, got %d", len(list))
	}
	// Most recent kept (DESC order).
	if list[0].ID < list[1].ID {
		t.Fatal("expected newest-first ordering")
	}
	if _, err := s.GetCheckpoint(ctx, otherID); err != nil {
		t.Fatalf("other session's checkpoint should survive: %v", err)
	}

	// keep <= 0 is a no-op.
	if err := s.PruneCheckpoints(ctx, 7, 0); err != nil {
		t.Fatalf("prune 0: %v", err)
	}
	if list, _ := s.ListCheckpoints(ctx, 7); len(list) != 2 {
		t.Fatalf("prune(0) should be a no-op, got %d", len(list))
	}
}

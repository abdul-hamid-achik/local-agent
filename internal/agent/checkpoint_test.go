package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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

func TestCheckpointImageReferenceRoundTripStaysPathFreeAndResolvesLazily(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	raw := []byte("checkpoint image bytes")
	image, err := llm.NewReferencedImageData("capture.png", "image/png", 12, 8, raw)
	if err != nil {
		t.Fatal(err)
	}
	ag := New(nil, nil, 8192)
	ag.SetWorkDir(t.TempDir())
	ag.SetCheckpointStore(store, 0)
	if err := ag.AddUserMessageWithImages("inspect", []llm.ImageData{image}); err != nil {
		t.Fatal(err)
	}
	id, err := ag.CreateCheckpoint(context.Background(), "image", db.CheckpointManual)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := store.GetCheckpoint(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cp.Messages, string(raw)) || strings.Contains(cp.Messages, t.TempDir()) {
		t.Fatalf("checkpoint leaked image bytes or a source path: %s", cp.Messages)
	}
	var persisted []llm.Message
	if err := json.Unmarshal([]byte(cp.Messages), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || len(persisted[0].Images) != 1 || len(persisted[0].Images[0].Data) != 0 || persisted[0].Images[0].SHA256 != image.SHA256 {
		t.Fatalf("checkpoint image projection = %#v", persisted)
	}

	ag.ReplaceMessages(nil)
	if _, err := ag.RestoreCheckpoint(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	ag.SetImageResolver(func(_ context.Context, reference llm.ImageData) ([]byte, error) {
		if reference.SHA256 != image.SHA256 || len(reference.Data) != 0 {
			t.Fatalf("resolver reference = %#v", reference)
		}
		return raw, nil
	})
	resolved, err := ag.resolveProviderImages(context.Background(), ag.Messages())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(resolved[0].Images[0].Data); got != string(raw) {
		t.Fatalf("resolved image = %q", got)
	}
	if len(ag.Messages()[0].Images[0].Data) != 0 {
		t.Fatal("lazy resolution mutated restored checkpoint history")
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

func TestRestoreCheckpointRejectsAnotherSession(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	id, err := store.CreateCheckpoint(context.Background(), 22, "foreign", db.CheckpointManual, `[]`, 0)
	if err != nil {
		t.Fatal(err)
	}
	ag := New(nil, nil, 8192)
	ag.SetCheckpointStore(store, 11)
	if _, err := ag.RestoreCheckpoint(context.Background(), id); err == nil {
		t.Fatal("restored checkpoint from another session")
	}
}

func TestRestoreCheckpointRejectsAnotherWorkspaceWithoutSession(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	workspaceA := t.TempDir()
	creator := New(nil, nil, 8192)
	creator.SetWorkDir(workspaceA)
	creator.SetCheckpointStore(store, 0)
	creator.AddUserMessage("workspace A secret")
	id, err := creator.CreateCheckpoint(context.Background(), "foreign", db.CheckpointManual)
	if err != nil {
		t.Fatal(err)
	}

	consumer := New(nil, nil, 8192)
	consumer.SetWorkDir(t.TempDir())
	consumer.SetCheckpointStore(store, 0)
	consumer.AddUserMessage("workspace B remains")
	if _, err := consumer.RestoreCheckpoint(context.Background(), id); err == nil {
		t.Fatal("fresh workspace restored a foreign checkpoint")
	}
	msgs := consumer.Messages()
	if len(msgs) != 1 || msgs[0].Content != "workspace B remains" {
		t.Fatalf("foreign restore changed transcript: %#v", msgs)
	}
}

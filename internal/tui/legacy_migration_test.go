package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/ice"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
)

func TestLegacyCheckpointMigrationRequiresPreviewAndExactConfirmation(t *testing.T) {
	m := newTestModel(t)
	workspaceDir := t.TempDir()
	m.agent.SetWorkDir(workspaceDir)
	workspaceID, err := canonicalWorkspaceID(workspaceDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	session, err := store.CreateSession(context.Background(), db.CreateSessionParams{
		Title: "active", Model: "qwen3.5:2b", Mode: "BUILD", WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.CreateCheckpoint(context.Background(), 0, "legacy", db.CheckpointManual, "[]", 0); err != nil {
			t.Fatal(err)
		}
	}
	m.SetSessionStore(store)
	m.sessionID = session.ID
	m.agent.SetCheckpointSessionID(session.ID)

	m.handleCommandAction(command.Result{Action: command.ActionClaimLegacyCheckpoints, Data: "2"})
	if count, err := store.CountUnboundLegacyCheckpoints(context.Background()); err != nil || count != 2 {
		t.Fatalf("claim without preview mutated data: %d, %v", count, err)
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Content, "no active preview") {
		t.Fatalf("direct confirmation message = %q", m.entries[len(m.entries)-1].Content)
	}

	m.handleCommandAction(command.Result{Action: command.ActionPreviewLegacyCheckpoints})
	if m.legacyCheckpointPreview == nil || m.legacyCheckpointPreview.Count != 2 {
		t.Fatalf("preview state = %#v", m.legacyCheckpointPreview)
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Content, "/migrate-checkpoints confirm 2") {
		t.Fatalf("preview message = %q", m.entries[len(m.entries)-1].Content)
	}
	if count, _ := store.CountUnboundLegacyCheckpoints(context.Background()); count != 2 {
		t.Fatal("preview mutated legacy checkpoints")
	}

	m.handleCommandAction(command.Result{Action: command.ActionClaimLegacyCheckpoints, Data: "2"})
	if m.legacyCheckpointPreview != nil {
		t.Fatal("successful confirmation did not consume preview")
	}
	if count, err := store.CountUnboundLegacyCheckpoints(context.Background()); err != nil || count != 0 {
		t.Fatalf("unbound count after claim = %d, %v", count, err)
	}
	claimed, err := store.ListCheckpointsForWorkspace(context.Background(), session.ID, workspaceID)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claimed checkpoints = %#v, %v", claimed, err)
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Content, "durable one-time receipt") {
		t.Fatalf("claim receipt message = %q", m.entries[len(m.entries)-1].Content)
	}
}

func TestLegacyMemoryMigrationRequiresPreviewAndReloadsScopedStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	legacy := memory.NewStore(memory.DefaultPathForWorkspace(""))
	if _, err := legacy.Save("legacy one", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Save("legacy two", nil); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t)
	m.agent.SetWorkDir(workspace)
	m.agent.SetMemoryStore(memory.NewStore(memory.DefaultPathForWorkspace(workspace)))

	m.claimLegacyMemory("2")
	if _, err := os.Stat(memory.DefaultPathForWorkspace("") + ".workspace-claim.json"); !os.IsNotExist(err) {
		t.Fatalf("direct confirmation mutated legacy state: %v", err)
	}
	m.previewLegacyMemory()
	if m.legacyMemoryPreview == nil || m.legacyMemoryPreview.Claim.Count != 2 {
		t.Fatalf("preview state = %#v", m.legacyMemoryPreview)
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Content, "/migrate-memory confirm 2") {
		t.Fatalf("preview did not print exact confirmation: %q", m.entries[len(m.entries)-1].Content)
	}
	if _, err := os.Stat(m.legacyMemoryPreview.Claim.MarkerPath); !os.IsNotExist(err) {
		t.Fatalf("preview mutated marker: %v", err)
	}
	m.claimLegacyMemory("2")
	if m.legacyMemoryPreview != nil {
		t.Fatal("successful confirmation retained reusable preview")
	}
	if got := m.agent.MemoryStore().Count(); got != 2 {
		t.Fatalf("active scoped memory count = %d, want 2", got)
	}
	if _, err := os.Stat(memory.DefaultPathForWorkspace("")); !os.IsNotExist(err) {
		t.Fatalf("live global memory survived explicit claim: %v", err)
	}
}

func TestLegacyICEMigrationRequiresPreviewAndExactCount(t *testing.T) {
	workspace := t.TempDir()
	engine, err := ice.NewEngine(commitTestClient{chat: func(context.Context, llm.ChatOptions, func(llm.StreamChunk) error) error {
		return nil
	}}, nil, ice.EngineConfig{StorePath: filepath.Join(t.TempDir(), "conversations.json"), Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	if _, err := engine.Store().Add("legacy", "user", "one", []float32{1}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Store().Add("legacy", "assistant", "two", []float32{1}, 2); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t)
	m.agent.SetWorkDir(workspace)
	m.agent.SetICEEngine(engine)

	m.claimLegacyICE("2")
	preview, err := engine.PreviewLegacyEntries()
	if err != nil || preview.Count != 2 {
		t.Fatalf("direct confirmation changed ICE: preview=%#v err=%v", preview, err)
	}
	m.previewLegacyICE()
	if m.legacyICEPreview == nil || m.legacyICEPreview.Claim.Count != 2 {
		t.Fatalf("preview state = %#v", m.legacyICEPreview)
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Content, "/migrate-ice confirm 2") {
		t.Fatalf("preview did not print exact confirmation: %q", m.entries[len(m.entries)-1].Content)
	}
	m.claimLegacyICE("2")
	if m.legacyICEPreview != nil || engine.ScopedEntryCount() != 2 || m.iceConversations != 2 {
		t.Fatalf("claim state preview=%#v scoped=%d displayed=%d", m.legacyICEPreview, engine.ScopedEntryCount(), m.iceConversations)
	}
}

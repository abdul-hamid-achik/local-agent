package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestSerializeDeserialize_Roundtrip(t *testing.T) {
	entries := []ChatEntry{
		{Kind: "user", Content: "Hello there"},
		{Kind: "assistant", Content: "Hi! How can I help?"},
		{Kind: "system", Content: "Model switched to qwen3"},
	}

	serialized := serializeEntries(entries)
	deserialized := deserializeEntries(serialized)

	if len(deserialized) != len(entries) {
		t.Fatalf("roundtrip length: got %d, want %d", len(deserialized), len(entries))
	}

	for i, e := range deserialized {
		if e.Kind != entries[i].Kind {
			t.Errorf("entry[%d] kind: got %q, want %q", i, e.Kind, entries[i].Kind)
		}
		if e.Content != entries[i].Content {
			t.Errorf("entry[%d] content: got %q, want %q", i, e.Content, entries[i].Content)
		}
	}
}

func TestSerializeEntries_Empty(t *testing.T) {
	result := serializeEntries(nil)
	if result != "" {
		t.Errorf("nil entries should serialize to empty, got %q", result)
	}
}

func TestDeserializeEntries_Empty(t *testing.T) {
	result := deserializeEntries("")
	if result != nil {
		t.Errorf("empty content should deserialize to nil, got %v", result)
	}
}

func TestDeserializeEntries_UnknownHeader(t *testing.T) {
	content := "## Unknown\n\nSome content\n\n## User\n\nValid content"
	result := deserializeEntries(content)
	if len(result) != 1 {
		t.Fatalf("should skip unknown headers, got %d entries", len(result))
	}
	if result[0].Kind != "user" {
		t.Errorf("should parse valid entry, got kind %q", result[0].Kind)
	}
}

func TestSerializeEntries_ErrorKind(t *testing.T) {
	entries := []ChatEntry{
		{Kind: "error", Content: "Something went wrong"},
	}
	serialized := serializeEntries(entries)
	if serialized == "" {
		t.Error("error entries should serialize")
	}

	deserialized := deserializeEntries(serialized)
	if len(deserialized) != 1 || deserialized[0].Kind != "error" {
		t.Errorf("error entry should roundtrip, got %v", deserialized)
	}
}

func TestSerializeEntries_MultilineContent(t *testing.T) {
	entries := []ChatEntry{
		{Kind: "user", Content: "line1\nline2\nline3"},
	}
	serialized := serializeEntries(entries)
	deserialized := deserializeEntries(serialized)
	if len(deserialized) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(deserialized))
	}
	if deserialized[0].Content != "line1\nline2\nline3" {
		t.Errorf("multiline content should roundtrip, got %q", deserialized[0].Content)
	}
}

func TestSessionTitleIsBounded(t *testing.T) {
	got := sessionTitle(strings.Repeat("x", 100))
	if len([]rune(got)) != 72 || !strings.HasSuffix(got, "...") {
		t.Fatalf("session title = %q (%d runes)", got, len([]rune(got)))
	}
}

func TestLosslessSessionStateRestoresAgentHistory(t *testing.T) {
	source := newTestModel(t)
	source.modelPinned = true
	source.entries = []ChatEntry{{Kind: "user", Content: "inspect"}, {Kind: "assistant", Content: "done"}}
	source.agent.ReplaceMessages([]llm.Message{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read"}}},
		{Role: "tool", Content: "contents", ToolName: "read", ToolCallID: "call-1"},
		{Role: "assistant", Content: "done"},
	})

	raw, err := encodeSessionState(source)
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeSessionState(raw)
	if err != nil {
		t.Fatal(err)
	}
	target := newTestModel(t)
	if err := target.restoreSessionState(state); err != nil {
		t.Fatal(err)
	}
	got := target.agent.Messages()
	if len(got) != 4 || got[1].ToolCalls[0].ID != got[2].ToolCallID {
		t.Fatalf("restored agent history is incomplete: %#v", got)
	}
	if !target.modelPinned {
		t.Fatal("saved model pin state was not restored")
	}
}

func TestStaleSessionLoadCannotReplaceCurrentState(t *testing.T) {
	m := newTestModel(t)
	m.entries = []ChatEntry{{Kind: "user", Content: "current"}}
	m.sessionLoading = true
	m.sessionLoadToken = 2

	updated, _ := m.Update(SessionLoadedMsg{
		LoadToken: 1,
		SessionID: 99,
		State:     persistedSessionState{Version: 1, Mode: ModeBuild},
	})
	m = updated.(*Model)
	if len(m.entries) != 1 || m.entries[0].Content != "current" || m.sessionID != 0 {
		t.Fatalf("stale load replaced current state: entries=%#v session=%d", m.entries, m.sessionID)
	}
	if !m.sessionLoading {
		t.Fatal("stale result cancelled the newer in-flight session load")
	}
}

func TestEscapeInvalidatesSessionLoad(t *testing.T) {
	m := newTestModel(t)
	m.sessionLoading = true
	m.sessionLoadToken = 4
	updated, _ := m.Update(escKey())
	m = updated.(*Model)
	if m.sessionLoading || m.sessionLoadToken != 5 {
		t.Fatalf("session load was not invalidated: loading=%v token=%d", m.sessionLoading, m.sessionLoadToken)
	}
}

func TestLateSessionListCannotOpenDuringActiveTurn(t *testing.T) {
	m := newTestModel(t)
	m.sessionListing = true
	m.sessionListToken = 7
	m.state = StateStreaming
	updated, _ := m.Update(SessionListMsg{
		ListToken: 7,
		Sessions:  []SessionListItem{{ID: 1, Title: "foreign"}},
	})
	m = updated.(*Model)
	if m.overlay == OverlaySessionsPicker || m.sessionsPickerState != nil {
		t.Fatal("late session list opened a picker during an active turn")
	}
	if m.sessionListing {
		t.Fatal("late session list left input permanently locked")
	}
}

func TestSessionLoadCannotRestoreDuringActiveTurn(t *testing.T) {
	m := newTestModel(t)
	m.entries = []ChatEntry{{Kind: "user", Content: "active"}}
	m.sessionLoading = true
	m.sessionLoadToken = 3
	m.state = StateWaiting
	updated, _ := m.Update(SessionLoadedMsg{
		LoadToken: 3,
		SessionID: 9,
		State: persistedSessionState{
			Version: 1,
			Mode:    ModeBuild,
			Entries: []persistedChatEntry{{Kind: "user", Content: "stale"}},
		},
	})
	m = updated.(*Model)
	if m.sessionID != 0 || len(m.entries) != 1 || m.entries[0].Content != "active" {
		t.Fatalf("active turn was replaced by late session load: session=%d entries=%#v", m.sessionID, m.entries)
	}
}

func TestSessionToolPersistenceExcludesEphemeralDataAndBoundsCards(t *testing.T) {
	m := newTestModel(t)
	m.toolEntries = []ToolEntry{{
		ID:            "tool-1",
		Name:          "write",
		Args:          strings.Repeat("a", maxPersistedToolArgsBytes*2),
		RawArgs:       map[string]any{"token": "RAW_SECRET_DO_NOT_PERSIST"},
		Result:        strings.Repeat("r", maxPersistedToolResultBytes*2),
		BeforeContent: "BEFORE_SECRET_DO_NOT_PERSIST",
		Status:        ToolStatusDone,
		DiffLines:     make([]DiffLine, maxPersistedDiffLines*2),
	}}
	raw, err := encodeSessionState(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "RAW_SECRET_DO_NOT_PERSIST") || strings.Contains(raw, "BEFORE_SECRET_DO_NOT_PERSIST") {
		t.Fatalf("ephemeral tool data leaked into session JSON: %s", raw)
	}
	state, err := decodeSessionState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ToolEntries) != 1 {
		t.Fatalf("tool entries = %d", len(state.ToolEntries))
	}
	entry := state.ToolEntries[0]
	if len(entry.Args) > maxPersistedToolArgsBytes || len(entry.Result) > maxPersistedToolResultBytes {
		t.Fatalf("persisted tool card exceeded bounds: args=%d result=%d", len(entry.Args), len(entry.Result))
	}
	if len(entry.DiffLines) > maxPersistedDiffLines {
		t.Fatalf("persisted diff lines = %d", len(entry.DiffLines))
	}
	restored := restoreToolEntries(state.ToolEntries)
	if restored[0].RawArgs != nil || restored[0].BeforeContent != "" {
		t.Fatalf("ephemeral fields restored: %#v", restored[0])
	}
}

func TestLoadPersistedSessionRejectsDifferentCanonicalWorkspace(t *testing.T) {
	workspaceA, err := canonicalWorkspaceID(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspaceB, err := canonicalWorkspaceID(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if workspaceA == workspaceB {
		t.Fatalf("test workspaces unexpectedly canonicalized to the same path: %q", workspaceA)
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

	ctx := context.Background()
	session, err := store.CreateSession(ctx, db.CreateSessionParams{
		Title:       "workspace A",
		Model:       "qwen3.5:2b",
		Mode:        "BUILD",
		WorkspaceID: workspaceA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionState(ctx, session.ID, `{"version":1,"messages":[],"entries":[],"mode":2}`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := loadPersistedSession(ctx, store, session.ID, workspaceB); err == nil || !strings.Contains(err.Error(), "different workspace") {
		t.Fatalf("cross-workspace load error = %v, want ownership rejection", err)
	}
	loaded, _, err := loadPersistedSession(ctx, store, session.ID, workspaceA)
	if err != nil {
		t.Fatalf("same-workspace load failed: %v", err)
	}
	if loaded.ID != session.ID {
		t.Fatalf("loaded session id = %d, want %d", loaded.ID, session.ID)
	}
}

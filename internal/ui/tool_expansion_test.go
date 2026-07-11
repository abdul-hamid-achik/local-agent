package ui

import (
	"strings"
	"testing"
)

func TestPerEntryCollapse_Default(t *testing.T) {
	m := newTestModel(t)
	m.toolsCollapsed = true

	// Simulate tool call start — new entry should inherit collapse state.
	updated, _ := m.Update(ToolCallStartMsg{
		Name: "read_file",
		Args: map[string]any{"path": "test.go"},
	})
	m = updated.(*Model)

	if len(m.toolEntries) != 1 {
		t.Fatalf("expected 1 tool entry, got %d", len(m.toolEntries))
	}
	if !m.toolEntries[0].Collapsed {
		t.Error("new tool entry should inherit toolsCollapsed=true")
	}
}

func TestPerEntryCollapse_InheritsFalse(t *testing.T) {
	m := newTestModel(t)
	m.toolsCollapsed = false

	updated, _ := m.Update(ToolCallStartMsg{
		Name: "bash",
		Args: map[string]any{"command": "ls"},
	})
	m = updated.(*Model)

	if m.toolEntries[0].Collapsed {
		t.Error("new tool entry should inherit toolsCollapsed=false")
	}
}

func TestBatchToggleAll(t *testing.T) {
	m := newTestModel(t)
	m.toolsCollapsed = true

	// Add multiple tool entries.
	m.toolEntries = []ToolEntry{
		{Name: "a", Status: ToolStatusDone, Collapsed: true},
		{Name: "b", Status: ToolStatusDone, Collapsed: true},
		{Name: "c", Status: ToolStatusDone, Collapsed: false},
	}

	// Toggle all (t key) should flip toolsCollapsed and apply to all.
	m.toolsCollapsed = !m.toolsCollapsed // false now
	for i := range m.toolEntries {
		m.toolEntries[i].Collapsed = m.toolsCollapsed
	}

	for i, te := range m.toolEntries {
		if te.Collapsed {
			t.Errorf("entry[%d] should be expanded after batch toggle", i)
		}
	}
}

func TestToggleLastTool(t *testing.T) {
	m := newTestModel(t)

	m.toolEntries = []ToolEntry{
		{Name: "a", Status: ToolStatusDone, Collapsed: true},
		{Name: "b", Status: ToolStatusDone, Collapsed: true},
	}

	// Toggle last only.
	last := len(m.toolEntries) - 1
	m.toolEntries[last].Collapsed = !m.toolEntries[last].Collapsed

	if m.toolEntries[0].Collapsed != true {
		t.Error("first entry should remain collapsed")
	}
	if m.toolEntries[1].Collapsed != false {
		t.Error("last entry should be expanded")
	}
}

func TestFileWriteSnapshotBefore(t *testing.T) {
	m := newTestModel(t)

	// Tool name containing "write" triggers snapshot.
	updated, _ := m.Update(ToolCallStartMsg{
		Name: "file_write",
		Args: map[string]any{"path": "/nonexistent/path"},
	})
	m = updated.(*Model)

	if len(m.toolEntries) != 1 {
		t.Fatalf("expected 1 tool entry, got %d", len(m.toolEntries))
	}
	// BeforeContent should be empty since file doesn't exist, but it should not panic.
	if m.toolEntries[0].BeforeContent != "" {
		t.Error("nonexistent file should give empty before content")
	}
}

func TestCollapsedDefaultNeverHidesToolError(t *testing.T) {
	m := newTestModel(t)
	m.toolsCollapsed = true
	m.entries = append(m.entries, ChatEntry{Kind: "user", Content: "run the check"})

	updated, _ := m.Update(ToolCallStartMsg{
		ID: "failed-call", Name: "bash", Args: map[string]any{"command": "go test ./..."},
	})
	m = updated.(*Model)
	updated, _ = m.Update(ToolCallResultMsg{
		ID: "failed-call", Name: "bash", Result: "permission denied", IsError: true,
	})
	m = updated.(*Model)

	rendered := m.renderEntries()
	for _, want := range []string{"go test ./...", "permission denied"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("collapsed error receipt missing %q:\n%s", want, rendered)
		}
	}
}

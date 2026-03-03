package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMouseClick_EmptyEntries(t *testing.T) {
	m := newTestModel(t)
	m.toolEntryRows = make(map[int]int)

	// Should not panic with no entries.
	m.handleMouseClick(5, 10)
}

func TestMouseClick_ToggleTool(t *testing.T) {
	m := newTestModel(t)
	m.toolEntries = []ToolEntry{
		{Name: "test", Status: ToolStatusDone, Collapsed: true},
	}
	m.toolEntryRows = map[int]int{0: 5}

	// Click at Y that maps to row 5 (header height=3, viewport offset=0).
	m.handleMouseClick(5, 8) // 8 - 3 + 0 = 5 → matches entry 0

	if m.toolEntries[0].Collapsed {
		t.Error("clicking tool entry should toggle collapsed state")
	}
}

func TestMouseClick_OutsideToolEntries(t *testing.T) {
	m := newTestModel(t)
	m.toolEntries = []ToolEntry{
		{Name: "test", Status: ToolStatusDone, Collapsed: true},
	}
	m.toolEntryRows = map[int]int{0: 5}

	// Click at a position that doesn't match any tool entry.
	m.handleMouseClick(5, 50)

	if !m.toolEntries[0].Collapsed {
		t.Error("clicking outside should not toggle collapsed state")
	}
}

func TestMouseWheel_SetsScrollFlag(t *testing.T) {
	m := newTestModel(t)
	m.userScrolledUp = false
	// Add enough content so the viewport is scrollable and not at bottom after scroll up.
	var longContent string
	for i := 0; i < 100; i++ {
		longContent += "line\n"
	}
	m.viewport.SetContent(longContent)
	m.viewport.GotoBottom()

	updated, _ := m.Update(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelUp})
	m = updated.(*Model)

	if !m.userScrolledUp {
		t.Error("scroll up should set userScrolledUp flag")
	}
}

func TestMouseWheel_ResetsAtBottom(t *testing.T) {
	m := newTestModel(t)
	m.userScrolledUp = true
	// With no content, viewport is at bottom, so scrolling should reset the flag.
	updated, _ := m.Update(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown})
	m = updated.(*Model)

	if m.userScrolledUp {
		t.Error("scroll to bottom should reset userScrolledUp flag")
	}
}

func TestMouseWheel_NilToolRows(t *testing.T) {
	m := newTestModel(t)
	m.toolEntryRows = nil

	// Should not panic with nil toolEntryRows.
	m.handleMouseClick(5, 10)
}

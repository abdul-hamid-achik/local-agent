package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPasteMsg_SmallPaste(t *testing.T) {
	m := newTestModel(t)
	m.state = StateIdle

	content := "short paste"
	updated, _ := m.Update(tea.PasteMsg{Content: content})
	m = updated.(*Model)

	if m.pendingPaste != "" {
		t.Error("small paste should not trigger pending paste")
	}
}

func TestPasteMsg_LargePaste(t *testing.T) {
	m := newTestModel(t)
	m.state = StateIdle

	// Create paste with >10 lines.
	content := strings.Repeat("line\n", 15)
	updated, _ := m.Update(tea.PasteMsg{Content: content})
	m = updated.(*Model)

	if m.pendingPaste == "" {
		t.Error("large paste should trigger pending paste")
	}
}

func TestPasteMsg_LargePasteNotIdle(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming

	content := strings.Repeat("line\n", 15)
	updated, _ := m.Update(tea.PasteMsg{Content: content})
	m = updated.(*Model)

	if m.pendingPaste != "" {
		t.Error("should not set pending paste during streaming")
	}
}

func TestPendingPaste_AcceptY(t *testing.T) {
	m := newTestModel(t)
	m.pendingPaste = "line1\nline2\nline3"

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = updated.(*Model)

	if m.pendingPaste != "" {
		t.Error("pressing y should clear pending paste")
	}
}

func TestPendingPaste_RejectN(t *testing.T) {
	m := newTestModel(t)
	m.pendingPaste = "line1\nline2\nline3"

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'n'})
	m = updated.(*Model)

	if m.pendingPaste != "" {
		t.Error("pressing n should clear pending paste")
	}
}

func TestPendingPaste_CancelEsc(t *testing.T) {
	m := newTestModel(t)
	m.pendingPaste = "line1\nline2\nline3"

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(*Model)

	if m.pendingPaste != "" {
		t.Error("pressing esc should clear pending paste")
	}
}

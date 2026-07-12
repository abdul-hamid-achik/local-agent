package ui

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
	if got := strings.Count(m.input.Value(), content); got != 1 {
		t.Fatalf("small paste inserted %d times, want exactly once: %q", got, m.input.Value())
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
	if m.input.Value() != "" {
		t.Fatalf("large paste reached the composer before consent: %q", m.input.Value())
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

func TestPendingPasteAcceptanceKeepsClosingFenceVisible(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 32})
	m = updated.(*Model)
	base := "Review the first receipt.\nKeep the second line while I check Settings.\n"
	m.input.SetValue(base)
	m.syncInputHeight()
	paste := strings.Join([]string{
		"fixture line 01", "fixture line 02", "fixture line 03", "fixture line 04",
		"fixture line 05", "fixture line 06", "fixture line 07", "fixture line 08",
		"fixture line 09", "fixture line 10", "fixture line 11",
	}, "\n")
	updated, _ = m.Update(tea.PasteMsg{Content: paste})
	m = updated.(*Model)
	if m.pendingPaste != paste {
		t.Fatal("large paste did not remain pending for an explicit decision")
	}
	if got := m.input.Value(); got != base {
		t.Fatalf("large paste mutated the draft before consent: %q", got)
	}

	updated, _ = m.Update(charKey('y'))
	m = updated.(*Model)
	if got := strings.Count(m.input.Value(), "fixture line 01"); got != 1 {
		t.Fatalf("accepted paste inserted %d copies, want exactly one", got)
	}
	view := m.View().Content
	for _, want := range []string{"fixture line 11", "```"} {
		if !strings.Contains(view, want) {
			t.Fatalf("accepted paste clipped %q:\n%s", want, view)
		}
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
	if got := strings.Count(m.input.Value(), "line1"); got != 1 {
		t.Fatalf("plain paste inserted %d copies, want exactly one", got)
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

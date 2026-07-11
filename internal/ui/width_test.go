package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSinglePaneWidthTracksTerminal(t *testing.T) {
	for _, width := range []int{30, 40, 80, 120, 200} {
		t.Run(strings.Repeat("w", width/10), func(t *testing.T) {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = updated.(*Model)
			if got, want := m.chatPaneWidth(), width-1; got != want {
				t.Fatalf("chat width = %d, want %d", got, want)
			}
			if got, want := m.viewport.Width(), width-1; got != want {
				t.Fatalf("viewport width = %d, want %d", got, want)
			}
			m.entries = []ChatEntry{{Kind: "assistant", Content: strings.Repeat("longword ", 80)}}
			m.invalidateEntryCache()
			m.viewport.SetContent(m.renderEntries())
			assertRenderedLinesFit(t, m.View().Content, width)
		})
	}
}

func TestTrueMinimumWidthShowsResizeState(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: minTerminalWidth - 1, Height: 20})
	m = updated.(*Model)
	view := m.View().Content
	if !strings.Contains(view, "TERMINAL TOO NARROW") || !strings.Contains(view, "30 columns") {
		t.Fatalf("minimum-width guidance missing:\n%s", view)
	}
	assertRenderedLinesFit(t, view, minTerminalWidth-1)
}

package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

func TestWideTranscriptUsesAvailableTerminalWidth(t *testing.T) {
	const terminalWidth = 200
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: terminalWidth, Height: 30})
	m = updated.(*Model)

	if got, want := m.chatContentWidth(), terminalWidth-7; got != want {
		t.Fatalf("wide chat content width = %d, want %d", got, want)
	}
	if m.markdownWidth != m.chatContentWidth() || m.md == nil || m.md.width != m.chatContentWidth() {
		t.Fatalf("wide markdown renderer is stale: model=%d renderer=%v content=%d", m.markdownWidth, m.md, m.chatContentWidth())
	}
	m.entries = []ChatEntry{{
		Kind:    "assistant",
		Content: strings.Repeat("use the available transcript width ", 24),
	}}
	m.invalidateRenderedCache()
	rendered := ansi.Strip(m.renderEntries())

	usedWidth := 0
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "available transcript width") {
			usedWidth = max(usedWidth, lipgloss.Width(line))
		}
	}
	if usedWidth <= 120 {
		t.Fatalf("wide assistant prose used only %d columns; transcript still appears capped:\n%s", usedWidth, rendered)
	}
	if usedWidth > m.chatPaneWidth() {
		t.Fatalf("wide assistant prose overflowed pane: used=%d pane=%d", usedWidth, m.chatPaneWidth())
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

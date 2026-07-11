package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionTitleTruncationPreservesUnicode(t *testing.T) {
	title := strings.Repeat("会話🙂", 20)
	got := (sessionItem{title: title}).Title()
	if !utf8.ValidString(got) {
		t.Fatalf("session title is invalid UTF-8: %q", got)
	}
	if width := lipgloss.Width(got); width > 40 {
		t.Fatalf("session title width = %d, want <= 40", width)
	}
}

func TestSessionsPickerFitsMinimumAndKeepsFooter(t *testing.T) {
	m := newTestModel(t)
	m.width = minTerminalWidth
	m.height = minTerminalHeight
	m.sessionsPickerState = newSessionsPickerState([]SessionListItem{
		{ID: 1, Title: strings.Repeat("会話🙂", 12), CreatedAt: "just now"},
		{ID: 2, Title: "Second session", CreatedAt: "yesterday"},
	}, m.width, m.height, m.isDark)
	m.overlay = OverlaySessionsPicker

	rendered := m.renderSessionsPicker()
	assertRenderedLinesFit(t, rendered, minTerminalWidth)
	assertRenderedHeightFits(t, rendered, minTerminalHeight)
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "esc close") {
		t.Fatalf("sessions footer missing close affordance:\n%s", rendered)
	}
}

func TestSessionsPickerFooterNamesSlashFilterBinding(t *testing.T) {
	m := newTestModel(t)
	m.sessionsPickerState = newSessionsPickerState([]SessionListItem{
		{ID: 1, Title: "First session", CreatedAt: "just now"},
		{ID: 2, Title: "Second session", CreatedAt: "yesterday"},
	}, m.width, m.height, m.isDark)
	m.overlay = OverlaySessionsPicker

	rendered := ansi.Strip(m.renderSessionsPicker())
	if !strings.Contains(rendered, "/ filter") {
		t.Fatalf("sessions footer should expose the Bubbles filter binding:\n%s", rendered)
	}
	if strings.Contains(rendered, "Type to filter") {
		t.Fatalf("sessions footer should not imply filtering starts without /:\n%s", rendered)
	}
}

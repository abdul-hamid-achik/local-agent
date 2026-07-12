package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderKeyHintsDegradesByPriority(t *testing.T) {
	m := newTestModel(t)
	hints := []keyHint{
		{Key: "Esc", Action: "Close"},
		{Key: "Enter", Action: "Select"},
		{Key: "↑/↓", Action: "Move"},
		{Key: "/", Action: "Filter"},
	}

	wide := ansi.Strip(m.renderKeyHints(80, hints...))
	if wide != "esc close · enter select · ↑/↓ move · / filter" {
		t.Fatalf("wide hints = %q", wide)
	}

	progressive := ansi.Strip(m.renderKeyHints(30, hints[:3]...))
	if progressive != "esc close · enter select · ↑/↓" {
		t.Fatalf("progressive hints hid an action that fits = %q", progressive)
	}

	compact := ansi.Strip(m.renderKeyHints(24, hints...))
	if compact != "esc close · enter · ↑/↓" {
		t.Fatalf("compact hints = %q", compact)
	}

	tiny := ansi.Strip(m.renderKeyHints(11, hints...))
	if !strings.HasPrefix(tiny, "esc") || strings.Contains(tiny, "filter") {
		t.Fatalf("tiny hints lost priority: %q", tiny)
	}
	if got := lipgloss.Width(tiny); got > 11 {
		t.Fatalf("tiny hint width = %d, want <= 11", got)
	}
}

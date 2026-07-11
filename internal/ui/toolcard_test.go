package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

func TestToolCardViewWithActivityIsPureAndShowsSummary(t *testing.T) {
	card := NewToolCard("write_file", ToolCardFile, true)
	card.SetSummary("write src/部署🙂/main.go")
	before := card

	first := card.ViewWithActivity(56, "◐", 1500*time.Millisecond)
	second := card.ViewWithActivity(56, "◐", 1500*time.Millisecond)

	if first != second {
		t.Fatalf("pure rendering changed between calls:\nfirst: %q\nsecond: %q", first, second)
	}
	if !reflect.DeepEqual(card, before) {
		t.Fatalf("rendering mutated the card:\nbefore: %#v\nafter:  %#v", before, card)
	}
	for _, want := range []string{"◐", "write_file", "write src/", "1.5s"} {
		if !strings.Contains(first, want) {
			t.Fatalf("running card missing %q:\n%s", want, first)
		}
	}
	assertToolCardLinesFit(t, first, 56)
}

func TestToolCardSummaryIsBoundedAndUnicodeSafe(t *testing.T) {
	card := NewToolCard("read_file", ToolCardFile, false)
	card.SetSummary(strings.Repeat("部署🙂\n", 300))

	if !utf8.ValidString(card.Summary) {
		t.Fatalf("summary is invalid UTF-8: %q", card.Summary)
	}
	if strings.Contains(card.Summary, "\n") {
		t.Fatalf("summary was not normalized to one line: %q", card.Summary)
	}
	if got := lipgloss.Width(card.Summary); got > maxToolCardSummaryWidth {
		t.Fatalf("summary width = %d, want <= %d", got, maxToolCardSummaryWidth)
	}

	card.State = ToolCardSuccess
	card.Duration = 42 * time.Millisecond
	view := card.View(32)
	if !strings.Contains(view, "部署") {
		t.Fatalf("collapsed card omitted its semantic summary:\n%s", view)
	}
	assertToolCardLinesFit(t, view, 32)
}

func TestToolCardCollapsedErrorAlwaysShowsResult(t *testing.T) {
	card := NewToolCard("write_file", ToolCardFile, true)
	card.State = ToolCardError
	card.Expanded = false
	card.Duration = 250 * time.Millisecond
	card.SetSummary("write 配置.yaml")
	card.Result = "permission denied for 配置.yaml"

	view := card.View(38)
	if !strings.Contains(view, "permission denied") {
		t.Fatalf("collapsed error hid its result:\n%s", view)
	}
	assertToolCardLinesFit(t, view, 38)

	card.Result = ""
	if fallback := card.View(38); !strings.Contains(fallback, "no error details") {
		t.Fatalf("empty error did not expose a diagnostic fallback:\n%s", fallback)
	}
}

func assertToolCardLinesFit(t *testing.T, rendered string, width int) {
	t.Helper()
	for lineNumber, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", lineNumber, got, width, rendered)
		}
	}
}

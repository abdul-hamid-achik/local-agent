package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
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
	for _, want := range []string{"◐", "Writing", "write src/", "1.5s"} {
		if !strings.Contains(first, want) {
			t.Fatalf("running card missing %q:\n%s", want, first)
		}
	}
	if card.Name != "write_file" {
		t.Fatalf("presentation changed raw correlation name: %q", card.Name)
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

func TestToolCardStripsTerminalControlsFromUntrustedFields(t *testing.T) {
	card := NewToolCard("remote__read", ToolCardGeneric, true)
	card.State = ToolCardSuccess
	card.Expanded = true
	card.SetSummary("summary\x1b]8;;https://example.invalid\x07link\x1b]8;;\x07\u202espoof")
	card.Args = "{\"path\":\"safe\x1b]0;owned\x07\"}"
	card.Result = "line\x1b]52;c;payload\x07\nnext\u202espoof"
	if strings.Contains(card.Summary, "\x1b]") || strings.Contains(card.Summary, "\x07") || strings.Contains(card.Summary, "\u202e") ||
		!strings.Contains(card.Summary, "summary") {
		t.Fatalf("tool card stored unsafe summary: %q", card.Summary)
	}

	rendered := card.View(72)
	for _, forbidden := range []string{"\x1b]", "\x07", "\u202e", "https://example.invalid"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("tool card retained terminal control payload %q: %q", forbidden, rendered)
		}
	}
	for _, want := range []string{"line", "next", "spoof"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("tool card dropped visible content %q: %q", want, rendered)
		}
	}
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

func TestToolCardCompletedReceiptShowsDisclosureState(t *testing.T) {
	card := NewToolCard("read_file", ToolCardFile, true)
	card.State = ToolCardSuccess
	card.Duration = 42 * time.Millisecond
	card.Result = "package ui"

	collapsed := ansi.Strip(card.View(48))
	if !strings.Contains(collapsed, "▸ ✓ Read") || strings.Contains(collapsed, "▾") {
		t.Fatalf("collapsed receipt did not expose its disclosure state:\n%s", collapsed)
	}

	card.Expanded = true
	expanded := ansi.Strip(card.View(48))
	if !strings.Contains(expanded, "▾ ✓ Read") || strings.Contains(expanded, "▸") {
		t.Fatalf("expanded receipt did not expose its disclosure state:\n%s", expanded)
	}

	card.State = ToolCardRunning
	running := ansi.Strip(card.View(48))
	if strings.Contains(running, "▸") || strings.Contains(running, "▾") {
		t.Fatalf("non-expandable running receipt showed a disclosure mark:\n%s", running)
	}

	card = NewToolCard("bash", ToolCardBash, true)
	card.State = ToolCardError
	card.Duration = 310 * time.Millisecond
	card.Result = "exit status 1"
	failed := ansi.Strip(card.View(48))
	if !strings.Contains(failed, "▸ ✗ Run failed") || !strings.Contains(failed, "exit status 1") {
		t.Fatalf("failed receipt lost its disclosure or error state:\n%s", failed)
	}

	card.State = ToolCardSuccess
	card.Expanded = false
	for _, width := range []int{4, 5, 12} {
		assertToolCardLinesFit(t, card.View(width), width)
	}
	if tiny := ansi.Strip(card.View(4)); strings.Contains(tiny, "▸") {
		t.Fatalf("extreme-width receipt kept a disclosure mark that cannot fit: %q", tiny)
	}
}

func TestToolCardProjectionStateFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		projection ecosystem.ToolProjection
		want       ToolCardState
	}{
		{
			name: "transport success without domain interpretation",
			projection: ecosystem.ToolProjection{
				Transport: ecosystem.TransportSucceeded,
			},
			want: ToolCardAttention,
		},
		{
			name: "successful domain with stale evidence",
			projection: ecosystem.ToolProjection{
				Transport: ecosystem.TransportSucceeded,
				Domain:    ecosystem.DomainSucceeded,
				Evidence:  ecosystem.EvidenceStale,
			},
			want: ToolCardAttention,
		},
		{
			name: "successful domain with contradicted evidence",
			projection: ecosystem.ToolProjection{
				Transport: ecosystem.TransportSucceeded,
				Domain:    ecosystem.DomainSucceeded,
				Evidence:  ecosystem.EvidenceContradicted,
			},
			want: ToolCardAttention,
		},
		{
			name: "domain failure wins over running transport",
			projection: ecosystem.ToolProjection{
				Transport: ecosystem.TransportRunning,
				Domain:    ecosystem.DomainFailed,
			},
			want: ToolCardError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolCardStateFromProjection(tt.projection); got != tt.want {
				t.Fatalf("state = %v, want %v for %#v", got, tt.want, tt.projection)
			}
		})
	}
}

func TestToolCardAttentionHonorsNoColor(t *testing.T) {
	previous := noColor
	noColor = true
	t.Cleanup(func() { noColor = previous })

	card := NewToolCard("mcphub__mcphub_call_tool", ToolCardGeneric, true)
	card.State = ToolCardAttention
	card.Projection = ecosystem.ToolProjection{
		Operation: "cortex_start_task", Role: ecosystem.RoleCoordination,
		Transport: ecosystem.TransportSucceeded, Domain: ecosystem.DomainAttention,
		Route: ecosystem.ToolRoute{Gateway: "mcphub", Server: "cortex", CallID: "call-7", Lazy: true},
	}
	rendered := card.View(48)
	if hasANSIColor(rendered) {
		t.Fatalf("NO_COLOR attention receipt emitted ANSI color: %q", rendered)
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

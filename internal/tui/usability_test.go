package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

func TestNarrowTerminalViewExplainsSidebarRecovery(t *testing.T) {
	m := newTestModel(t)
	m.width = 40
	m.height = 20
	m.sidePanel.SetWidth(25)
	m.sidePanel.Show()

	view := m.View()
	if !strings.Contains(view.Content, "Ctrl+B") || !strings.Contains(view.Content, "hide sidebar") {
		t.Fatalf("narrow view should explain immediate recovery, got:\n%s", view.Content)
	}
	assertRenderedLinesFit(t, view.Content, 40)
	m.state = StateStreaming
	if busy := m.View().Content; !strings.Contains(busy, "Esc, then Ctrl+B") {
		t.Fatalf("busy narrow view should explain cancellation before sidebar toggle, got:\n%s", busy)
	}

	m.state = StateIdle
	m.sidePanel.Hide()
	if hint := m.narrowTerminalHint(); hint != "" {
		t.Fatalf("40-column terminal should be usable after hiding sidebar, got hint %q", hint)
	}
}

func TestCompactWelcomeFitsActualChatPane(t *testing.T) {
	m := newTestModel(t)
	m.width = 60
	m.height = 20
	m.sidePanel.SetWidth(25)
	m.sidePanel.Show()

	var b strings.Builder
	m.renderWelcome(&b)
	got := b.String()
	if strings.Contains(got, "╦") {
		t.Fatal("compact welcome should omit the wide ASCII logo")
	}
	for _, want := range []string{"LOCAL AGENT", "Local-first", "? help", "/ commands"} {
		if !strings.Contains(got, want) {
			t.Errorf("compact welcome missing %q:\n%s", want, got)
		}
	}
	assertRenderedLinesFit(t, got, m.chatPaneWidth())
}

func TestHelpOverlayFitsNarrowTerminal(t *testing.T) {
	m := newTestModel(t)
	m.sidePanel.Hide()
	m.width = 40
	m.height = 20
	m.initHelpViewport()

	overlay := m.renderHelpOverlay(m.width)
	assertRenderedLinesFit(t, overlay, m.width)
	if !strings.Contains(overlay, "Keyboard Shortcuts") {
		t.Fatalf("help content missing from overlay:\n%s", overlay)
	}
}

func TestToolCardFitsNarrowWidthAndUnicode(t *testing.T) {
	card := NewToolCard("write_文件_with_a_very_long_name", ToolCardFile, true)
	card.State = ToolCardSuccess
	card.Duration = 1250 * time.Millisecond
	card.Expanded = true
	card.Args = `{"path":"a/very/long/path"}`
	card.Result = "first very long result line\nsecond very long result line"

	for _, width := range []int{4, 12, 20} {
		t.Run(string(rune('a'+width)), func(t *testing.T) {
			assertRenderedLinesFit(t, card.View(width), width)
		})
	}
}

func TestRenderDiffAtWidthFitsPane(t *testing.T) {
	lines := []DiffLine{
		{Kind: DiffAdded, Content: "a very long added line that cannot fit"},
		{Kind: DiffRemoved, Content: "a very long removed line that cannot fit"},
		{Kind: DiffContext, Content: "context that cannot fit either"},
	}
	got := renderDiffAtWidth(lines, NewStyles(true), 10, 20)
	assertRenderedLinesFit(t, got, 20)
}

func TestAdaptiveStatusTextUsesReadableMutedColor(t *testing.T) {
	tests := []struct {
		name   string
		isDark bool
		want   string
	}{
		{name: "light", isDark: false, want: "#5B6779"},
		{name: "dark", isDark: true, want: "#8B97AD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adaptiveStyles(tt.isDark).StatusText.GetForeground()
			want := lipgloss.Color(tt.want)
			gr, gg, gb, ga := got.RGBA()
			wr, wg, wb, wa := want.RGBA()
			if gr != wr || gg != wg || gb != wb || ga != wa {
				t.Fatalf("status text color = %#v, want %s", got, tt.want)
			}
		})
	}
}

func TestTruncateDisplayPreservesUnicodeAndCellWidth(t *testing.T) {
	got := truncateDisplay("模型🙂abcdef", 7)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated value should use an ellipsis, got %q", got)
	}
	if lipgloss.Width(got) > 7 {
		t.Fatalf("display width = %d, want <= 7 (%q)", lipgloss.Width(got), got)
	}
}

func TestWrapTextHasNoBlankContinuationAndPreservesUnicode(t *testing.T) {
	got := wrapText("Toggle this help when input is empty", 18)
	if strings.Contains(got, "\n\n") {
		t.Fatalf("wrapped prose contains an empty continuation line: %q", got)
	}
	assertRenderedLinesFit(t, got, 18)

	unicode := wrapText("模型模型模型🙂abcdef", 6)
	if !utf8.ValidString(unicode) {
		t.Fatalf("wrapped Unicode is invalid UTF-8: %q", unicode)
	}
	assertRenderedLinesFit(t, unicode, 6)
}

func assertRenderedLinesFit(t *testing.T, rendered string, width int) {
	t.Helper()
	for i, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("line %d width = %d, want <= %d: %q", i+1, got, width, line)
		}
	}
}

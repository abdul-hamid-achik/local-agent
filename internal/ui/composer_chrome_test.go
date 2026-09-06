package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestComposerIsFramedAtSharedColumn(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{"standard", 80, 24},
		{"wide", 120, 36},
	} {
		m := newTestModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m = updated.(*Model)

		view, _ := m.renderComposerChrome()
		plain := ansi.Strip(view)
		lines := strings.Split(plain, "\n")
		if len(lines) < 3 {
			t.Fatalf("%s: framed composer should be at least 3 rows:\n%s", size.name, plain)
		}
		grid := m.contentGrid()
		if got := lipgloss.Width(lines[0]); got != grid.LineWidth() {
			t.Fatalf("%s: composer top width = %d, want LineWidth %d\n%s", size.name, got, grid.LineWidth(), plain)
		}
		if !strings.HasPrefix(strings.TrimRight(lines[0], " "), strings.Repeat(" ", grid.OriginX())+"╭") {
			t.Fatalf("%s: composer is not inset by OriginX:\n%s", size.name, plain)
		}
		if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "╮") ||
			!strings.Contains(lines[len(lines)-1], "╰") || !strings.Contains(lines[len(lines)-1], "╯") {
			t.Fatalf("%s: composer is missing a complete rounded box:\n%s", size.name, plain)
		}
		if !strings.Contains(plain, "❯") {
			t.Fatalf("%s: composer missing prompt:\n%s", size.name, plain)
		}
	}
}

func TestComposerStaysUnframedOnMinimumTerminal(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: minTerminalWidth, Height: minTerminalHeight})
	m = updated.(*Model)
	if m.composerFrameActive() {
		t.Fatal("minimum terminal should keep the draft unframed")
	}
	view, _ := m.renderComposerChrome()
	plain := ansi.Strip(view)
	for _, border := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(plain, border) {
			t.Fatalf("minimum composer is framed:\n%s", plain)
		}
	}
	if !strings.Contains(plain, "❯") {
		t.Fatalf("minimum composer missing prompt:\n%s", plain)
	}
}

func TestComposerPromptSitsInsideTheFrame(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)

	view, _ := m.renderComposerChrome()
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) < 2 {
		t.Fatalf("composer has no inner row:\n%q", view)
	}
	inner := lines[1]
	at := strings.Index(inner, "❯")
	if at < 0 {
		t.Fatalf("composer has no prompt:\n%q", inner)
	}
	// OriginX gutter + border + pad put ❯ in display column 6 (1-indexed).
	if col := lipgloss.Width(inner[:at]) + 1; col != contentLeftColumns+3 {
		t.Fatalf("composer prompt in column %d, want %d:\n%q", col, contentLeftColumns+3, inner)
	}
	if col := lipgloss.Width(inner[:at]) + lipgloss.Width("❯ ") + 1; col != contentLeftColumns+5 {
		t.Fatalf("composer text starts in column %d, want %d:\n%q", col, contentLeftColumns+5, inner)
	}
}

func TestComposerInnerWidthIsLineWidthMinusFrame(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)

	box := m.contentGrid().ContentWidth()
	inner := m.composerInnerWidth()
	frame := composerFrameStyle(box, m.glyphProfile, lipgloss.NoColor{})
	if got := frame.GetHorizontalFrameSize(); got != 4 {
		t.Fatalf("composer frame size = %d, want 4 (border 2 + pad 2)", got)
	}
	if inner != box-4 {
		t.Fatalf("composer inner = %d, want ContentWidth %d - 4", inner, box)
	}
}

func TestFooterIdentityLivesOnShortcutsRow(t *testing.T) {
	// Authority sits on the bottom shortcuts row, beside the key that changes
	// it, and never under the composer box as a second meta line. Identity
	// (model · context) lives in the top bar — see planStatus.
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(*Model)
	m.model = "ornith:latest"
	m.setMode(ModePlan)
	bar := ansi.Strip(m.renderShortcutsBar(m.chatPaneWidth()))
	if !strings.Contains(bar, "PLAN") {
		t.Fatalf("shortcuts bar missing mode:\n%s", bar)
	}
	if strings.Contains(bar, "ornith") {
		t.Fatalf("shortcuts bar re-printed model owned by the top bar:\n%s", bar)
	}
	if top := ansi.Strip(m.renderSessionTopBar(m.chatPaneWidth())); !strings.Contains(top, "ornith") {
		t.Fatalf("top bar missing the model it owns:\n%s", top)
	}
	// The composer carries the draft and nothing else — no identity meta row.
	chrome, _ := m.renderComposerChrome()
	if plain := ansi.Strip(chrome); strings.Contains(plain, "ornith") {
		t.Fatalf("model leaked into the composer surface:\n%s", plain)
	}
}

func TestActivityOmitsConversationTitle(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(*Model)
	m.entries = []ChatEntry{{Kind: "user", Content: "hey chat unique prompt"}}
	m.activeSessionTitle = "hey chat unique prompt"
	m.sessionPublicID = "abcdef1"
	m.state = StateStreaming
	m.reducedMotion = true
	m.turnStartedAt = m.nowTime()
	line := ansi.Strip(m.renderWorkingLine())
	if strings.Contains(line, "hey chat unique prompt") {
		t.Fatalf("activity re-echoed the conversation title:\n%s", line)
	}
}

func TestComposerAndIdentityShareLineWidth(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.entries = []ChatEntry{{Kind: "user", Content: "align the last prompt"}}
	m.settleChromeSpringForTest()

	outer := m.contentGrid().LineWidth()
	paneW := m.chatPaneWidth()
	composer, _ := m.renderComposerChrome()
	composerTop := strings.Split(ansi.Strip(composer), "\n")[0]
	identity := ansi.Strip(m.renderSessionTopBar(paneW))
	if lipgloss.Width(composerTop) != outer {
		t.Fatalf("composer width = %d, want LineWidth %d", lipgloss.Width(composerTop), outer)
	}
	if lipgloss.Width(identity) != outer {
		t.Fatalf("top bar width = %d, want LineWidth %d so the meter matches the composer", lipgloss.Width(identity), outer)
	}
	if outer >= paneW {
		t.Fatalf("shared column %d should be inset from pane %d", outer, paneW)
	}
	grid := m.contentGrid()
	if grid.RightChrome() != grid.OriginX() {
		t.Fatalf("gutters are uneven: left OriginX=%d right chrome=%d (pane %d LineWidth %d)",
			grid.OriginX(), grid.RightChrome(), paneW, outer)
	}
	composerPad := strings.Repeat(" ", grid.OriginX())
	if !strings.HasPrefix(strings.TrimRight(composerTop, " "), composerPad+"╭") {
		t.Fatalf("composer left gutter missing:\n%s", composerTop)
	}
}

func TestModalHidesComposerChromeWithoutChangingItsHeight(t *testing.T) {
	m := newTestModel(t)
	for _, size := range [][2]int{{80, 24}, {30, 12}} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(*Model)
		m.input.SetValue("private draft")
		live, _ := m.renderComposerChrome()
		inert := m.renderInertComposerChrome()
		if strings.TrimSpace(ansi.Strip(inert)) != "" || lipgloss.Height(inert) != lipgloss.Height(live) {
			t.Fatalf("modal should hide the draft and frame, preserving height: live=%q inert=%q", live, inert)
		}
	}
}

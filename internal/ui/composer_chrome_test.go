package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestComposerFrameOnRoomyFrame(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	if !m.composerFrameEnabled() {
		t.Fatal("expected framed composer on roomy terminal")
	}
	view, _ := m.renderComposerChrome()
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "╭") && !strings.Contains(plain, "+") {
		t.Fatalf("framed composer missing border:\n%s", plain)
	}
}

func TestComposerFrameOffOnMinimumTerminal(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: minTerminalWidth, Height: minTerminalHeight})
	m = updated.(*Model)
	if m.composerFrameEnabled() {
		t.Fatal("minimum terminal must not use framed composer")
	}
	view, _ := m.renderComposerChrome()
	plain := ansi.Strip(view)
	if strings.Contains(plain, "╭") || strings.Contains(plain, "╮") {
		t.Fatalf("minimum composer still framed:\n%s", plain)
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
	// Composer chrome is border-only (no trailing model line).
	chrome, _ := m.renderComposerChrome()
	plain := ansi.Strip(chrome)
	if strings.Contains(plain, "ornith:latest") && !strings.Contains(plain, "╭") && !strings.Contains(plain, "+") {
		t.Fatalf("model leaked into composer body unexpectedly:\n%s", plain)
	}
	// Framed composer must not append a separate identity row under the box.
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if len(lines) > 0 {
		last := lines[len(lines)-1]
		if strings.Contains(last, "ornith") && !strings.Contains(last, "│") && !strings.Contains(last, "|") {
			t.Fatalf("composer still has under-box meta line: %q", last)
		}
	}
}

func TestActivityOmitsStickyEcho(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(*Model)
	m.entries = []ChatEntry{{Kind: "user", Content: "hey chat unique prompt"}}
	m.activeSessionTitle = "hey chat unique prompt"
	m.sessionPublicID = "abcdef1"
	m.state = StateStreaming
	m.reducedMotion = true
	m.turnStartedAt = m.nowTime()
	if !m.stickyUserActive() {
		t.Fatal("expected sticky user for activity de-dupe")
	}
	line := ansi.Strip(m.renderWorkingLine())
	if strings.Contains(line, "hey chat unique prompt") {
		t.Fatalf("activity re-echoed sticky/title prompt:\n%s", line)
	}
}

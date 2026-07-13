package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestHelpUsesSharedFrameAtSupportedSizes(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{name: "minimum", width: 30, height: 12},
		{name: "narrow", width: 40, height: 20},
		{name: "normal", width: 80, height: 24},
	} {
		t.Run(size.name, func(t *testing.T) {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = updated.(*Model)
			m.overlay = OverlayHelp
			m.initHelpViewport()

			rendered := m.renderHelpOverlay(m.width)
			assertRenderedLinesFit(t, rendered, size.width)
			assertRenderedHeightFits(t, rendered, size.height)
		})
	}
}

func TestHelpKeepsKeysAndContextTruthfulAtNarrowWidth(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m = updated.(*Model)
	plain := ansi.Strip(m.buildHelpContent(m.helpContentWidth()))
	if !strings.Contains(plain, "shift+enter") || strings.Contains(plain, "shift+ent…") {
		t.Fatalf("narrow help truncated a key label:\n%s", plain)
	}

	foundContext := false
	for _, row := range m.keyHelpRows() {
		if row.key == "?" && strings.Contains(strings.ToLower(row.desc), "empty input") {
			foundContext = true
			break
		}
	}
	if !foundContext {
		t.Fatal("help does not disclose the empty-input requirement")
	}
}

func TestHelpExplainsOneSlotFollowUpLifecycle(t *testing.T) {
	m := newTestModel(t)
	content := strings.Join(strings.Fields(strings.ToLower(ansi.Strip(m.buildHelpContent(m.helpContentWidth())))), " ")
	for _, want := range []string{
		"send / queue one follow-up",
		"enter (running)",
		"after the current turn settles successfully",
		"esc (running)",
		"returns to the composer",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("help omitted %q:\n%s", want, content)
		}
	}
}

func TestHelpPreservesScrollAcrossResize(t *testing.T) {
	m := newTestModel(t)
	m.overlay = OverlayHelp
	m.initHelpViewport()
	m.helpViewport.GotoBottom()
	if m.helpViewport.YOffset() == 0 {
		t.Fatal("help fixture is not scrollable")
	}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	m = updated.(*Model)
	if m.helpViewport.YOffset() == 0 {
		t.Fatal("help resize discarded the scroll position")
	}
	assertRenderedLinesFit(t, m.renderHelpOverlay(m.width), 60)
	assertRenderedHeightFits(t, m.renderHelpOverlay(m.width), 18)
}

func TestHelpGoalActionsShareStateAwareRegistryMetadata(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 80, 24
	content := ansi.Strip(m.buildHelpContent(m.helpContentWidth()))
	for _, action := range []string{"/goal new", "/goal show", "/goal pause", "/goal resume", "/goal budget", "/goal drop"} {
		if !strings.Contains(content, action) {
			t.Fatalf("help omitted %q:\n%s", action, content)
		}
	}
	if !strings.Contains(content, "Unavailable") || !strings.Contains(content, "No goal is configured") {
		t.Fatalf("help omitted goal action availability:\n%s", content)
	}
}

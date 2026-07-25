package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// This is the guard the codebase was missing. Duplicated ambient state was
// found by eye, one pair at a time, and fixed with a local `if !headerActive`
// that only covered the pair someone had noticed. These tests fail on any
// repetition across every supported width, so the next one cannot ship quietly.

// ambientFrames are the frame sizes and session states worth sweeping: the
// supported minimum, the widths where the top bar and the shortcuts identity
// switch on, and a roomy desktop terminal.
type ambientFrame struct {
	name          string
	width, height int
}

var ambientFrames = []ambientFrame{
	{"minimum", 30, 12},
	{"narrow", 46, 18},
	{"identity threshold", 52, 20},
	{"standard", 80, 24},
	{"wide", 120, 36},
}

func TestAmbientStateIsNeverPrintedTwiceInAFrame(t *testing.T) {
	for _, mode := range []Mode{ModeNormal, ModePlan, ModeAuto} {
		for _, started := range []bool{false, true} {
			for _, frame := range ambientFrames {
				m := newTestModel(t)
				updated, _ := m.Update(tea.WindowSizeMsg{Width: frame.width, Height: frame.height})
				m = updated.(*Model)
				m.model = "ornith:latest"
				m.setMode(mode)
				m.numCtx = 98_304
				m.promptTokens = 1024
				if started {
					m.entries = []ChatEntry{
						{Kind: "user", Content: "hey"},
						{Kind: "assistant", Content: "sure"},
					}
				}
				m.recalcViewportHeight()
				m.refreshTranscript()

				view := ansi.Strip(m.View().Content)
				label := frame.name + "/" + m.modeConfigs[mode].Label
				if started {
					label += "/started"
				}

				if got := strings.Count(view, "ornith"); got > 1 {
					t.Errorf("%s: model printed %d times:\n%s", label, got, view)
				}
				// NORMAL is never badged, so only PLAN and AUTO can double up.
				if mode != ModeNormal {
					if got := strings.Count(view, m.modeConfigs[mode].Label); got > 1 {
						t.Errorf("%s: mode printed %d times:\n%s", label, got, view)
					}
				}
			}
		}
	}
}

// Ownership must also be total: a fact assigned to a surface that will not
// paint it is a fact the reader never sees. This is the failure mode that the
// first cut of planStatus shipped, by assigning identity to the shortcuts row
// at widths where that row carries keys only.
func TestAmbientStateAlwaysHasExactlyOneOwner(t *testing.T) {
	for _, frame := range ambientFrames {
		for _, started := range []bool{false, true} {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: frame.width, Height: frame.height})
			m = updated.(*Model)
			m.model = "ornith:latest"
			m.setMode(ModePlan)
			if started {
				m.entries = []ChatEntry{{Kind: "user", Content: "hey"}}
			}
			m.recalcViewportHeight()

			plan := m.planStatus()
			for fact, name := range map[statusFact]string{
				factModel:          "model",
				factRemoteBoundary: "remote boundary",
				factContext:        "context",
				factMode:           "mode",
			} {
				if plan.ownedBy(fact) == surfaceNone {
					t.Errorf("%s (started=%v): %s has no owner", frame.name, started, name)
				}
			}
		}
	}
}

// The model and the authority badge must each be reachable on every supported
// frame. A no-duplication rule is trivially satisfiable by printing nothing.
func TestAmbientStateRemainsVisibleOnEverySupportedFrame(t *testing.T) {
	for _, frame := range ambientFrames {
		m := newTestModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: frame.width, Height: frame.height})
		m = updated.(*Model)
		m.model = "ornith:latest"
		m.setMode(ModePlan)
		m.entries = []ChatEntry{{Kind: "user", Content: "hey"}}
		m.recalcViewportHeight()
		m.refreshTranscript()

		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "ornith") {
			t.Errorf("%s: model is not visible anywhere:\n%s", frame.name, view)
		}
		if !strings.Contains(view, "PLAN") {
			t.Errorf("%s: PLAN authority is not visible anywhere:\n%s", frame.name, view)
		}
	}
}

// A theme switch must reach every cached surface, not just the styles the
// Model holds directly. Child components, Bubbles delegates, and the Glamour
// renderer each keep their own copy, which is what rebuildThemedSurfaces
// exists to repaint.
func TestThemeSwitchRepaintsTheWholeFrame(t *testing.T) {
	m := newTestModel(t)
	m.entries = []ChatEntry{
		{Kind: "user", Content: "hey"},
		{Kind: "assistant", Content: "an answer", RenderedContent: "an answer"},
	}
	m.refreshTranscript()
	before := m.View().Content

	if !m.SetTheme("dracula") {
		t.Fatal("SetTheme rejected a registered theme")
	}
	m.refreshTranscript()
	after := m.View().Content

	if before == after {
		t.Fatal("theme switch did not change any painted cell")
	}
	// The Dracula dark accent must actually appear somewhere in the frame.
	if !strings.Contains(after, "139;233;253") { // #8BE9FD
		t.Fatalf("selected theme's accent is absent from the frame:\n%q", after[:min(600, len(after))])
	}
	if m.md == nil || m.md.themeID != "dracula" {
		t.Fatal("markdown renderer kept the previous theme")
	}
}

func TestThemeSwitchRejectsUnknownAndKeepsCurrent(t *testing.T) {
	m := newTestModel(t)
	if !m.SetTheme("gruvbox") {
		t.Fatal("SetTheme rejected gruvbox")
	}
	if m.SetTheme("not-a-theme") {
		t.Fatal("SetTheme accepted an unregistered id")
	}
	if got := m.ThemeID(); got != "gruvbox" {
		t.Fatalf("rejected switch changed the active theme to %q", got)
	}
}

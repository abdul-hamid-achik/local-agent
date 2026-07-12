package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestMultilineComposerCostsOneViewportRowPerLine(t *testing.T) {
	for _, width := range []int{80, 60, 40} {
		m := newTestModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		m = updated.(*Model)
		initial := m.viewport.Height()

		m.input.SetValue("one\ntwo\nthree")
		m.syncInputHeight()
		if got, want := m.viewport.Height(), initial-2; got != want {
			t.Fatalf("width %d: three-line viewport height = %d, want %d", width, got, want)
		}

		m.input.SetValue("one")
		m.syncInputHeight()
		if got := m.viewport.Height(); got != initial {
			t.Fatalf("width %d: collapsed composer height = %d, want %d", width, got, initial)
		}
	}
}

func TestFiveRowComposerFitsTerminalAndKeepsTailVisible(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 32})
	m = updated.(*Model)
	lines := make([]string, 14)
	for i := range lines {
		lines[i] = "fixture line " + string(rune('A'+i))
	}
	m.input.SetValue(strings.Join(lines, "\n"))
	m.syncInputHeight()
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	m = updated.(*Model)

	view := m.View().Content
	if got := lipgloss.Height(view); got > m.height {
		t.Fatalf("five-row composer view height = %d, want <= %d", got, m.height)
	}
	if !strings.Contains(view, lines[len(lines)-1]) {
		t.Fatalf("five-row composer clipped its tail:\n%s", view)
	}
}

func TestMultilineComposerUsesOneSendMarkerAndContinuationRails(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("first\nsecond\nthird")
	m.syncInputHeight()

	view := ansi.Strip(m.input.View())
	if got := strings.Count(view, "❯ "); got != 1 {
		t.Fatalf("multiline composer rendered %d send markers, want one:\n%s", got, view)
	}
	if got := strings.Count(view, "│ "); got != 2 {
		t.Fatalf("multiline composer rendered %d continuation rails, want two:\n%s", got, view)
	}
}

func TestHeightOnlyResizePreservesRenderedCaches(t *testing.T) {
	m := newTestModel(t)
	m.entries = []ChatEntry{{
		Kind: "assistant", Content: "cached answer", RenderedContent: "cached answer",
	}}
	m.viewport.SetContent(m.renderEntries())
	if !m.entryCacheValid {
		t.Fatal("precondition: entry cache should be valid")
	}
	markdown := m.md
	rendered := m.entries[0].RenderedContent

	updated, _ := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height + 5})
	m = updated.(*Model)

	if m.md != markdown {
		t.Fatal("height-only resize recreated the markdown renderer")
	}
	if m.entries[0].RenderedContent != rendered {
		t.Fatal("height-only resize rebuilt completed assistant markdown")
	}
	if !m.entryCacheValid {
		t.Fatal("height-only resize invalidated the transcript entry cache")
	}
}

func TestMouseHitTestingStartsAtViewportRowZero(t *testing.T) {
	m := newTestModel(t)
	m.toolEntries = []ToolEntry{{Collapsed: true}}
	m.toolHitRegions = []toolHitRegion{{ToolIndex: 0, Row: 0, EndCol: 12}}
	m.handleMouseClick(0, 0)
	if m.toolEntries[0].Collapsed {
		t.Fatal("row-zero tool card was not toggled")
	}

	m.viewport.SetContent(strings.Repeat("line\n", 100))
	m.viewport.SetYOffset(3)
	m.toolEntries[0].Collapsed = true
	m.toolHitRegions[0].Row = 3
	m.handleMouseClick(0, 0)
	if m.toolEntries[0].Collapsed {
		t.Fatal("scrolled row-zero tool card was not toggled")
	}
}

func TestDenseToolCardHitRegionsNeverOverlap(t *testing.T) {
	m := newTestModel(t)
	m.toolEntries = []ToolEntry{{Collapsed: true}, {Collapsed: true}}
	m.toolHitRegions = []toolHitRegion{
		{ToolIndex: 0, Row: 4, EndCol: 12},
		{ToolIndex: 1, Row: 5, EndCol: 12},
	}

	m.handleMouseClick(0, 5)
	if !m.toolEntries[0].Collapsed || m.toolEntries[1].Collapsed {
		t.Fatalf("second dense receipt toggled the wrong card: %#v", m.toolEntries)
	}

	m.toolEntries[0].Collapsed = true
	m.toolEntries[1].Collapsed = true
	m.handleMouseClick(0, 6)
	if !m.toolEntries[0].Collapsed || !m.toolEntries[1].Collapsed {
		t.Fatal("clicking below an exact ToolCard header toggled a receipt")
	}
}

func TestToolCardMouseTargetsExcludeFooterAndRightPadding(t *testing.T) {
	m := newTestModel(t)
	m.toolEntries = []ToolEntry{{Collapsed: true}}
	m.viewport.SetHeight(5)
	m.toolHitRegions = []toolHitRegion{{ToolIndex: 0, Row: 5, EndCol: 8}}

	// Terminal row viewport.Height() is the divider immediately below the
	// transcript. It must never alias a scrolled transcript row.
	m.handleMouseClick(0, m.viewport.Height())
	if !m.toolEntries[0].Collapsed {
		t.Fatal("clicking the divider toggled an offscreen ToolCard")
	}

	m.toolHitRegions[0].Row = 0
	m.handleMouseClick(8, 0)
	if !m.toolEntries[0].Collapsed {
		t.Fatal("clicking right-side padding toggled a ToolCard")
	}
	m.handleMouseClick(7, 0)
	if m.toolEntries[0].Collapsed {
		t.Fatal("clicking inside the rendered ToolCard header did not toggle it")
	}
}

func TestModeReceiptDoesNotGrowViewBeyondTerminal(t *testing.T) {
	m := newTestModel(t)
	m.setMode(ModePlan)
	view := m.View().Content
	assertRenderedHeightFits(t, view, m.height)
}

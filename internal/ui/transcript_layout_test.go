package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestTranscriptEntrySeparatorOwnsVerticalRhythm(t *testing.T) {
	tests := []struct {
		name              string
		previous, current string
		want              string
	}{
		{name: "turn boundary", previous: "user", current: "assistant", want: "\n\n"},
		{name: "tool sequence", previous: "tool_group", current: "tool_group", want: "\n"},
		{name: "tool to answer", previous: "tool_group", current: "assistant", want: "\n\n"},
		{name: "notice stack", previous: "system", current: "system", want: "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := transcriptEntrySeparator(tt.previous, tt.current); got != tt.want {
				t.Fatalf("separator %q -> %q = %q, want %q", tt.previous, tt.current, got, tt.want)
			}
		})
	}
}

func TestSystemNoticesAreExplicitAndBounded(t *testing.T) {
	m := newTestModel(t)
	for _, width := range []int{30, 80} {
		m.width = width
		plain := ansi.Strip(m.renderSystemNotice("Model changed to local", width-2))
		if !strings.Contains(plain, "notice · Model changed") {
			t.Fatalf("%d-column host notice is indistinguishable:\n%s", width, plain)
		}
		for _, line := range strings.Split(plain, "\n") {
			if len([]rune(line)) > width {
				t.Fatalf("%d-column host notice overflowed: %q", width, line)
			}
		}
	}
}

func TestRenderEntriesNestsReasoningAndDenselyStacksTools(t *testing.T) {
	m := newTestModel(t)
	m.entries = []ChatEntry{
		{Kind: "user", Content: "check it"},
		{Kind: "assistant", Content: "starting", RenderedContent: "starting", ThinkingContent: "inspect first", ThinkingCollapsed: true},
		{Kind: "tool_group", ToolIndex: 0},
		{Kind: "tool_group", ToolIndex: 1},
		{Kind: "assistant", Content: "finished", RenderedContent: "finished"},
	}
	m.toolEntries = []ToolEntry{
		{ID: "one", Name: "read_file", Status: ToolStatusDone, Duration: 10 * time.Millisecond, Collapsed: true},
		{ID: "two", Name: "bash", Status: ToolStatusDone, Duration: 20 * time.Millisecond, Collapsed: true},
	}

	plain := ansi.Strip(m.renderEntries())
	assistantAt := strings.Index(plain, "assistant\n")
	reasoningAt := strings.Index(plain, "Thought")
	startingAt := strings.Index(plain, "starting")
	if assistantAt < 0 || reasoningAt < assistantAt || startingAt < reasoningAt {
		t.Fatalf("assistant turn ownership is unclear:\n%s", plain)
	}
	if got := strings.Count(plain, "assistant\n"); got != 1 {
		t.Fatalf("tool boundaries split one assistant turn into %d role headers:\n%s", got, plain)
	}
	if !strings.Contains(plain, "Read (10ms)\n  │") {
		t.Fatalf("consecutive tool receipts are not densely stacked:\n%s", plain)
	}
	if strings.Contains(plain, "starting\n\n\n") || strings.Contains(plain, "(20ms)\n\n\n") {
		t.Fatalf("semantic boundary contains duplicate blank rows:\n%s", plain)
	}
	if len(m.toolHitRegions) != 2 || m.toolHitRegions[1].Row != m.toolHitRegions[0].Row+1 {
		t.Fatalf("dense ToolCard headers do not have exact adjacent hit rows: %#v", m.toolHitRegions)
	}
	if m.toolHitRegions[1].EndCol <= 0 {
		t.Fatalf("ToolCard header has no horizontal hit bound: %#v", m.toolHitRegions[1])
	}
	secondRegion := m.toolHitRegions[1]
	if secondRegion.StartCol <= 0 || secondRegion.StartCol >= secondRegion.EndCol {
		t.Fatalf("ToolCard header does not exclude its left margin: %#v", secondRegion)
	}
	m.handleMouseClick(secondRegion.EndCol, secondRegion.Row)
	if !m.toolEntries[0].Collapsed || !m.toolEntries[1].Collapsed {
		t.Fatal("clicking immediately beyond a rendered header toggled a receipt")
	}
	m.handleMouseClick(secondRegion.StartCol-1, secondRegion.Row)
	if !m.toolEntries[0].Collapsed || !m.toolEntries[1].Collapsed {
		t.Fatal("clicking immediately before a rendered header toggled a receipt")
	}
	m.handleMouseClick(secondRegion.StartCol, secondRegion.Row)
	if !m.toolEntries[0].Collapsed || m.toolEntries[1].Collapsed {
		t.Fatalf("clicking the first rendered header cell toggled the wrong receipt: %#v", m.toolEntries)
	}
	m.toolEntries[1].Collapsed = true
	m.invalidateEntryCache()
	m.refreshTranscript()
	secondRegion = m.toolHitRegions[1]
	m.handleMouseClick(secondRegion.EndCol-1, secondRegion.Row)
	if !m.toolEntries[0].Collapsed || m.toolEntries[1].Collapsed {
		t.Fatalf("clicking the last rendered header cell toggled the wrong receipt: %#v", m.toolEntries)
	}
	m.toolEntries[1].Collapsed = true
	m.invalidateEntryCache()
	m.refreshTranscript()
	secondRegion = m.toolHitRegions[1]
	m.handleMouseClick(secondRegion.StartCol, secondRegion.Row-1)
	if m.toolEntries[0].Collapsed || !m.toolEntries[1].Collapsed {
		t.Fatal("clicking the preceding dense header did not target only that ToolCard")
	}
	m.toolEntries[0].Collapsed = true
	m.invalidateEntryCache()
	m.refreshTranscript()
	secondRegion = m.toolHitRegions[1]
	m.handleMouseClick(secondRegion.StartCol, secondRegion.Row+1)
	if !m.toolEntries[0].Collapsed || !m.toolEntries[1].Collapsed {
		t.Fatal("clicking below a rendered ToolCard header toggled a receipt")
	}
}

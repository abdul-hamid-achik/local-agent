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
	m.toolCardMgr = NewToolCardManager(m.isDark)
	for i, entry := range m.toolEntries {
		m.toolCardMgr.AddCardWithID(entry.ID, entry.Name, ToolCardGeneric, time.Time{})
		card := &m.toolCardMgr.Cards[i]
		card.State = ToolCardSuccess
		card.Duration = entry.Duration
	}

	plain := ansi.Strip(m.renderEntries())
	assistantAt := strings.Index(plain, "assistant ")
	reasoningAt := strings.Index(plain, "reasoning")
	startingAt := strings.Index(plain, "starting")
	if assistantAt < 0 || reasoningAt < assistantAt || startingAt < reasoningAt {
		t.Fatalf("assistant turn ownership is unclear:\n%s", plain)
	}
	if !strings.Contains(plain, "Read (10ms)\n  │") {
		t.Fatalf("consecutive tool receipts are not densely stacked:\n%s", plain)
	}
	if strings.Contains(plain, "starting\n\n\n") || strings.Contains(plain, "(20ms)\n\n\n") {
		t.Fatalf("semantic boundary contains duplicate blank rows:\n%s", plain)
	}
}

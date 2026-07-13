package ui

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestProcessStreamChunk_PlainText(t *testing.T) {
	main, think, inThinking, buf := processStreamChunk("hello world", false, "")
	if main != "hello world" {
		t.Errorf("main text = %q, want %q", main, "hello world")
	}
	if think != "" {
		t.Errorf("think text = %q, want empty", think)
	}
	if inThinking {
		t.Error("should not be in thinking mode")
	}
	if buf != "" {
		t.Errorf("search buf = %q, want empty", buf)
	}
}

func TestProcessStreamChunk_OpenTag(t *testing.T) {
	main, think, inThinking, _ := processStreamChunk("<think>reasoning here", false, "")
	if main != "" {
		t.Errorf("main text = %q, want empty", main)
	}
	if think != "reasoning here" {
		t.Errorf("think text = %q, want %q", think, "reasoning here")
	}
	if !inThinking {
		t.Error("should be in thinking mode")
	}
}

func TestProcessStreamChunk_CloseTag(t *testing.T) {
	main, think, inThinking, _ := processStreamChunk("end of thought</think>visible text", true, "")
	if think != "end of thought" {
		t.Errorf("think text = %q, want %q", think, "end of thought")
	}
	if main != "visible text" {
		t.Errorf("main text = %q, want %q", main, "visible text")
	}
	if inThinking {
		t.Error("should not be in thinking mode after close tag")
	}
}

func TestProcessStreamChunk_FullCycle(t *testing.T) {
	main, think, inThinking, _ := processStreamChunk("<think>thought</think>response", false, "")
	if think != "thought" {
		t.Errorf("think text = %q, want %q", think, "thought")
	}
	if main != "response" {
		t.Errorf("main text = %q, want %q", main, "response")
	}
	if inThinking {
		t.Error("should not be in thinking mode")
	}
}

func TestProcessStreamChunk_SplitAcrossChunks(t *testing.T) {
	// First chunk ends with partial tag "<thi"
	main1, think1, inThinking1, buf1 := processStreamChunk("text<thi", false, "")
	if main1 != "text" {
		t.Errorf("chunk1 main = %q, want %q", main1, "text")
	}
	if think1 != "" {
		t.Errorf("chunk1 think = %q, want empty", think1)
	}
	if inThinking1 {
		t.Error("chunk1 should not be in thinking")
	}
	if buf1 != "<thi" {
		t.Errorf("chunk1 buf = %q, want %q", buf1, "<thi")
	}

	// Second chunk completes the tag
	main2, think2, inThinking2, _ := processStreamChunk("nk>reasoning", false, buf1)
	if main2 != "" {
		t.Errorf("chunk2 main = %q, want empty", main2)
	}
	if think2 != "reasoning" {
		t.Errorf("chunk2 think = %q, want %q", think2, "reasoning")
	}
	if !inThinking2 {
		t.Error("chunk2 should be in thinking mode")
	}
}

func TestProcessStreamChunk_NestedTags(t *testing.T) {
	// Nested <think> should be treated as text inside thinking.
	main, think, _, _ := processStreamChunk("<think>outer<think>inner</think>after", false, "")
	// The inner <think> should be literal text inside thinking.
	// When we encounter the first </think>, thinking ends.
	if main != "after" {
		t.Errorf("main = %q, want %q", main, "after")
	}
	// The think content should include "outer<think>inner"
	if think != "outer<think>inner" {
		t.Errorf("think = %q, want %q", think, "outer<think>inner")
	}
}

func TestHasPartialTagSuffix(t *testing.T) {
	tests := []struct {
		s, tag string
		want   int
	}{
		{"hello<", "<think>", 1},
		{"hello<t", "<think>", 2},
		{"hello<th", "<think>", 3},
		{"hello<thi", "<think>", 4},
		{"hello<thin", "<think>", 5},
		{"hello<think", "<think>", 6},
		{"hello<think>", "<think>", 0}, // full match, not partial
		{"hello", "<think>", 0},
		{"</thi", "</think>", 5},
		{"<", "</think>", 1},
	}
	for _, tt := range tests {
		got := hasPartialTagSuffix(tt.s, tt.tag)
		if got != tt.want {
			t.Errorf("hasPartialTagSuffix(%q, %q) = %d, want %d", tt.s, tt.tag, got, tt.want)
		}
	}
}

func TestRenderThinkingBoxUsesCompactRail(t *testing.T) {
	m := newTestModel(t)
	m.width = 80

	collapsed := ansi.Strip(m.renderThinkingBox("inspect files\ncompare behavior\nreport result", true))
	if !strings.Contains(collapsed, "│ ▸ reasoning · 3 lines · ctrl+t expand") {
		t.Fatalf("collapsed reasoning receipt is unclear:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "inspect files") || strings.ContainsAny(collapsed, "╭╮╰╯") {
		t.Fatalf("collapsed reasoning retained heavy chrome or hidden content:\n%s", collapsed)
	}

	expanded := ansi.Strip(m.renderThinkingBox("inspect files\ncompare behavior\nreport result", false))
	for _, want := range []string{"│ ▾ reasoning", "ctrl+t collapse", "│ inspect files", "│ report result"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded reasoning missing %q:\n%s", want, expanded)
		}
	}
}

func TestThinkingHeaderUsesSingularLineLabel(t *testing.T) {
	got := thinkingHeader("▸", "expand", 1, 80)
	if got != "▸ reasoning · 1 line · ctrl+t expand" {
		t.Fatalf("singular reasoning header = %q", got)
	}
}

func TestRenderThinkingBoxStaysInsideReadableTranscript(t *testing.T) {
	for _, width := range []int{30, 40, 80, 160} {
		m := newTestModel(t)
		m.width = width
		rendered := m.renderThinkingBox(strings.Repeat("long reasoning text ", 10), false)
		maximum := max(4, m.chatContentWidth()-2)
		for lineNumber, line := range strings.Split(rendered, "\n") {
			if got := lipgloss.Width(line); got > maximum {
				t.Fatalf("width %d line %d = %d cells, want <= %d: %q", width, lineNumber+1, got, maximum, line)
			}
		}
	}
}

func TestLiveReasoningUsesStableStandaloneRailUntilAnswerStarts(t *testing.T) {
	m := newTestModel(t)
	m.reducedMotion = true
	m.thinkBuf.WriteString("inspect files\ncompare behavior")

	plain := ansi.Strip(m.renderEntries())
	if strings.Contains(plain, "assistant") {
		t.Fatalf("thinking-only stream rendered an empty assistant block:\n%s", plain)
	}
	for _, want := range []string{"│ reasoning · live", "compare behavior"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("live reasoning omitted %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "chars") || strings.Contains(plain, "ctrl+t") {
		t.Fatalf("live reasoning exposed unstable metrics or an unavailable action:\n%s", plain)
	}
}

func TestLiveReasoningSummarySanitizesTerminalAndBidiControls(t *testing.T) {
	unsafe := "earlier\n\x1b]52;c;REASONING_SECRET\x07inspect\x1b[2J\tfiles\u202e\u2066"
	summary := liveThinkingSummary(unsafe)
	if strings.Contains(summary, "REASONING_SECRET") || !strings.Contains(summary, "inspect files") {
		t.Fatalf("sanitized live reasoning summary = %q", summary)
	}
	for _, character := range summary {
		if unicode.IsControl(character) || isBidiControl(character) {
			t.Fatalf("unsafe rune %U survived live reasoning summary: %q", character, summary)
		}
	}

	m := newTestModel(t)
	rendered := ansi.Strip(m.renderLiveThinkingBox(unsafe))
	if strings.Contains(rendered, "REASONING_SECRET") || !strings.Contains(rendered, "reasoning · live · inspect files") {
		t.Fatalf("unsafe live reasoning header = %q", rendered)
	}
}

func TestExpandedReasoningStripsTerminalControlSequences(t *testing.T) {
	m := newTestModel(t)
	rendered := m.renderThinkingBox("inspect\x1b]0;owned\x07\nthen \u202espoof", false)
	for _, forbidden := range []string{"\x1b]", "\x07", "\u202e", "owned"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("expanded reasoning retained terminal control payload %q: %q", forbidden, rendered)
		}
	}
	for _, want := range []string{"inspect", "then", "spoof"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expanded reasoning dropped visible text %q: %q", want, rendered)
		}
	}
}

func TestReasoningOnlyCompletionAvoidsEmptyAssistantBlock(t *testing.T) {
	m := newTestModel(t)
	m.entries = []ChatEntry{{
		Kind: "assistant", ThinkingContent: "inspect files", ThinkingCollapsed: true,
	}}

	plain := ansi.Strip(m.renderEntries())
	if strings.Contains(plain, "assistant") {
		t.Fatalf("completed reasoning-only segment rendered empty assistant chrome:\n%s", plain)
	}
	if !strings.Contains(plain, "reasoning · 1 line") {
		t.Fatalf("completed reasoning receipt missing:\n%s", plain)
	}
}

func TestWhitespaceOnlyThinkingDoesNotCreateLiveOrCompletedBlock(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	updated, _ := m.Update(StreamThinkingMsg{Text: " \n\t "})
	m = updated.(*Model)
	if m.hasLiveTurn() {
		t.Fatal("whitespace-only reasoning created a live turn")
	}
	m.flushStream()
	if len(m.entries) != 0 {
		t.Fatalf("whitespace-only reasoning created entries: %#v", m.entries)
	}
}

func TestToolCallSettlesReasoningBeforeReceipt(t *testing.T) {
	m := newTestModel(t)
	m.reducedMotion = true
	m.state = StateStreaming
	m.thinkBuf.WriteString("inspect the target")

	updated, _ := m.Update(ToolCallStartMsg{
		ID: "call-1", Name: "read", Args: map[string]any{"path": "README.md"}, StartTime: time.Now(),
	})
	m = updated.(*Model)
	if len(m.entries) != 2 || m.entries[0].Kind != "assistant" || m.entries[1].Kind != "tool_group" {
		t.Fatalf("reasoning/tool entry order = %#v", m.entries)
	}
	plain := ansi.Strip(m.renderEntries())
	if strings.Contains(plain, "assistant") {
		t.Fatalf("tool reasoning rendered empty assistant role:\n%s", plain)
	}
	reasoningAt := strings.Index(plain, "reasoning")
	toolAt := strings.Index(strings.ToLower(plain), "read")
	if reasoningAt < 0 || toolAt < 0 || reasoningAt > toolAt {
		t.Fatalf("reasoning did not precede tool receipt:\n%s", plain)
	}
}

func TestToggleThinkingAppliesToEveryVisibleDisclosure(t *testing.T) {
	m := newTestModel(t)
	m.entries = []ChatEntry{
		{Kind: "assistant", Content: "first", ThinkingContent: "first reasoning", ThinkingCollapsed: true},
		{Kind: "assistant", Content: "second", ThinkingContent: "second reasoning", ThinkingCollapsed: true},
	}

	updated, _ := m.Update(ctrlKey('t'))
	m = updated.(*Model)
	for i, entry := range m.entries {
		if entry.ThinkingCollapsed {
			t.Fatalf("entry %d remained collapsed after shared disclosure toggle", i)
		}
	}

	updated, _ = m.Update(ctrlKey('t'))
	m = updated.(*Model)
	for i, entry := range m.entries {
		if !entry.ThinkingCollapsed {
			t.Fatalf("entry %d remained expanded after shared disclosure toggle", i)
		}
	}
}

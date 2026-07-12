package ui

import (
	"strings"
	"testing"

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

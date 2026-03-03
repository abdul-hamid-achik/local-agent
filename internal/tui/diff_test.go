package tui

import (
	"strings"
	"testing"
)

func TestComputeDiff_Identical(t *testing.T) {
	result := computeDiff("hello\nworld\n", "hello\nworld\n")
	if result != nil {
		t.Errorf("identical texts should return nil, got %d lines", len(result))
	}
}

func TestComputeDiff_EmptyBefore(t *testing.T) {
	result := computeDiff("", "line1\nline2\n")
	if len(result) == 0 {
		t.Fatal("expected diff lines for new file")
	}
	for _, line := range result {
		if line.Kind != DiffAdded {
			t.Errorf("new file should have only added lines, got kind %d", line.Kind)
		}
	}
}

func TestComputeDiff_EmptyAfter(t *testing.T) {
	result := computeDiff("line1\nline2\n", "")
	if len(result) == 0 {
		t.Fatal("expected diff lines for deleted file")
	}
	for _, line := range result {
		if line.Kind != DiffRemoved {
			t.Errorf("deleted file should have only removed lines, got kind %d", line.Kind)
		}
	}
}

func TestComputeDiff_Modification(t *testing.T) {
	before := "line1\nline2\nline3\n"
	after := "line1\nline2-modified\nline3\n"
	result := computeDiff(before, after)

	if len(result) == 0 {
		t.Fatal("expected diff lines for modification")
	}

	// Should contain removed and added lines.
	var hasAdded, hasRemoved, hasContext bool
	for _, line := range result {
		switch line.Kind {
		case DiffAdded:
			hasAdded = true
			if line.Content != "line2-modified" {
				t.Errorf("added line should be 'line2-modified', got %q", line.Content)
			}
		case DiffRemoved:
			hasRemoved = true
			if line.Content != "line2" {
				t.Errorf("removed line should be 'line2', got %q", line.Content)
			}
		case DiffContext:
			hasContext = true
		}
	}
	if !hasAdded || !hasRemoved {
		t.Error("modification should produce both added and removed lines")
	}
	if !hasContext {
		t.Error("modification should have context lines")
	}
}

func TestComputeDiff_ContextLimiting(t *testing.T) {
	// Create a file with many lines and a change in the middle.
	var before, after strings.Builder
	for i := 0; i < 50; i++ {
		before.WriteString("line" + strings.Repeat("x", i) + "\n")
		after.WriteString("line" + strings.Repeat("x", i) + "\n")
	}
	// Change line 25
	beforeStr := strings.Replace(before.String(), "line"+strings.Repeat("x", 25), "CHANGED", 1)
	afterStr := strings.Replace(after.String(), "line"+strings.Repeat("x", 25), "MODIFIED", 1)

	result := computeDiff(beforeStr, afterStr)
	if len(result) == 0 {
		t.Fatal("expected diff lines")
	}

	// Should not include all 50 lines — context filtering should limit output.
	if len(result) > 20 {
		t.Errorf("context limiting should reduce output, got %d lines", len(result))
	}
}

func TestFilterContext_EmptyInput(t *testing.T) {
	result := filterContext(nil, 3)
	if result != nil {
		t.Errorf("empty input should return nil, got %d lines", len(result))
	}
}

func TestFilterContext_AllChanges(t *testing.T) {
	lines := []DiffLine{
		{DiffAdded, "a"},
		{DiffAdded, "b"},
		{DiffRemoved, "c"},
	}
	result := filterContext(lines, 3)
	if len(result) != 3 {
		t.Errorf("all changes should be kept, got %d lines", len(result))
	}
}

func TestSplitLines_Empty(t *testing.T) {
	result := splitLines("")
	if result != nil {
		t.Errorf("empty string should return nil, got %v", result)
	}
}

func TestSplitLines_TrailingNewline(t *testing.T) {
	result := splitLines("a\nb\n")
	if len(result) != 2 {
		t.Errorf("should have 2 lines, got %d: %v", len(result), result)
	}
}

func TestLcsLines_Empty(t *testing.T) {
	result := lcsLines(nil, []string{"a"})
	if result != nil {
		t.Errorf("LCS with empty input should be nil, got %v", result)
	}
}

func TestLcsLines_Basic(t *testing.T) {
	a := []string{"a", "b", "c", "d"}
	b := []string{"a", "c", "d", "e"}
	lcs := lcsLines(a, b)
	expected := []string{"a", "c", "d"}
	if len(lcs) != len(expected) {
		t.Fatalf("LCS length mismatch: got %v, want %v", lcs, expected)
	}
	for i, v := range lcs {
		if v != expected[i] {
			t.Errorf("LCS[%d] = %q, want %q", i, v, expected[i])
		}
	}
}

func TestRenderDiff_Empty(t *testing.T) {
	s := NewStyles(true)
	result := renderDiff(nil, s, 10)
	if result != "" {
		t.Errorf("empty diff should render empty, got %q", result)
	}
}

func TestRenderDiff_MaxLines(t *testing.T) {
	lines := []DiffLine{
		{DiffAdded, "a"},
		{DiffAdded, "b"},
		{DiffAdded, "c"},
		{DiffAdded, "d"},
		{DiffAdded, "e"},
	}
	s := NewStyles(true)
	result := renderDiff(lines, s, 3)
	// Should contain "more lines" indicator.
	if !strings.Contains(result, "more lines") {
		t.Error("should show 'more lines' when truncating")
	}
}

func TestReadFileForDiff_NoArgs(t *testing.T) {
	result := readFileForDiff(nil)
	if result != "" {
		t.Errorf("nil args should return empty, got %q", result)
	}
}

func TestReadFileForDiff_NonexistentFile(t *testing.T) {
	result := readFileForDiff(map[string]any{"path": "/nonexistent/file/path"})
	if result != "" {
		t.Errorf("nonexistent file should return empty, got %q", result)
	}
}

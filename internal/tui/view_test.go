package tui

import (
	"strings"
	"testing"
	"time"
)

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{999, "999"},
		{1000, "1.0k"},
		{1234, "1.2k"},
		{8192, "8.2k"},
		{0, "0"},
		{500, "500"},
		{10000, "10.0k"},
	}

	for _, tt := range tests {
		got := formatTokens(tt.input)
		if got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{42 * time.Millisecond, "42ms"},
		{1300 * time.Millisecond, "1.3s"},
		{0, "0ms"},
		{999 * time.Millisecond, "999ms"},
		{time.Second, "1.0s"},
		{2500 * time.Millisecond, "2.5s"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.input)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"within_limit", "hello", 10, "hello"},
		{"exact_limit", "hello", 5, "hello"},
		{"over_limit", "hello world", 8, "hello..."},
		{"much_over", "this is a long string", 10, "this is..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

func TestWrapText(t *testing.T) {
	t.Run("no_wrap_needed", func(t *testing.T) {
		got := wrapText("short", 20)
		if got != "short" {
			t.Errorf("expected 'short', got %q", got)
		}
	})

	t.Run("word_wrap", func(t *testing.T) {
		got := wrapText("hello world foo bar", 11)
		lines := strings.Split(got, "\n")
		for _, line := range lines {
			if len(line) > 11 {
				t.Errorf("line %q exceeds width 11", line)
			}
		}
	})

	t.Run("preserves_newlines", func(t *testing.T) {
		got := wrapText("line1\nline2", 20)
		if !strings.Contains(got, "\n") {
			t.Error("should preserve existing newlines")
		}
		lines := strings.Split(got, "\n")
		if len(lines) < 2 {
			t.Errorf("expected at least 2 lines, got %d", len(lines))
		}
	})

	t.Run("width_zero_guard", func(t *testing.T) {
		got := wrapText("hello", 0)
		if got != "hello" {
			t.Errorf("width<=0 should return original, got %q", got)
		}
	})

	t.Run("width_negative", func(t *testing.T) {
		got := wrapText("hello", -1)
		if got != "hello" {
			t.Errorf("negative width should return original, got %q", got)
		}
	})
}

func TestIndentBlock(t *testing.T) {
	t.Run("prefix_added", func(t *testing.T) {
		got := indentBlock("hello\nworld", "  ")
		lines := strings.Split(got, "\n")
		if lines[0] != "  hello" {
			t.Errorf("first line should be '  hello', got %q", lines[0])
		}
		if lines[1] != "  world" {
			t.Errorf("second line should be '  world', got %q", lines[1])
		}
	})

	t.Run("empty_lines_preserved", func(t *testing.T) {
		got := indentBlock("hello\n\nworld", ">> ")
		lines := strings.Split(got, "\n")
		if len(lines) != 3 {
			t.Fatalf("expected 3 lines, got %d", len(lines))
		}
		if lines[0] != ">> hello" {
			t.Errorf("first line should be '>> hello', got %q", lines[0])
		}
		if lines[1] != "" {
			t.Errorf("empty line should stay empty, got %q", lines[1])
		}
		if lines[2] != ">> world" {
			t.Errorf("third line should be '>> world', got %q", lines[2])
		}
	})

	t.Run("single_line", func(t *testing.T) {
		got := indentBlock("hello", "* ")
		if got != "* hello" {
			t.Errorf("expected '* hello', got %q", got)
		}
	})
}

func TestContextPctInHeader(t *testing.T) {
	t.Run("no_pct_when_zero_tokens", func(t *testing.T) {
		m := newTestModel(t)
		m.model = "test-model"
		m.promptTokens = 0
		m.numCtx = 8192
		header := m.renderHeader()
		if strings.Contains(header, "%") {
			t.Error("should not show percentage when promptTokens is 0")
		}
	})

	t.Run("no_pct_when_zero_numCtx", func(t *testing.T) {
		m := newTestModel(t)
		m.model = "test-model"
		m.promptTokens = 1000
		m.numCtx = 0
		header := m.renderHeader()
		if strings.Contains(header, "%") {
			t.Error("should not show percentage when numCtx is 0")
		}
	})

	t.Run("correct_percentage", func(t *testing.T) {
		m := newTestModel(t)
		m.model = "test-model"
		m.promptTokens = 4096
		m.numCtx = 8192
		header := m.renderHeader()
		if !strings.Contains(header, "50%") {
			t.Errorf("expected header to contain '50%%', got %q", header)
		}
	})

	t.Run("high_percentage", func(t *testing.T) {
		m := newTestModel(t)
		m.model = "test-model"
		m.promptTokens = 7500
		m.numCtx = 8192
		header := m.renderHeader()
		if !strings.Contains(header, "91%") {
			t.Errorf("expected header to contain '91%%', got %q", header)
		}
	})
}

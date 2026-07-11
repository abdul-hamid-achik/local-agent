package ui

import (
	"strings"
	"testing"
)

func TestFindSafeMarkdownBoundary(t *testing.T) {
	tests := []struct {
		name    string
		content string
		// wantPrefix is the substring expected before the boundary; "" means no boundary (0).
		wantPrefix string
	}{
		{
			name:       "no blank line yet",
			content:    "just a single line still streaming",
			wantPrefix: "",
		},
		{
			name:       "one complete paragraph then partial",
			content:    "First paragraph done.\n\nSecond para still goi",
			wantPrefix: "First paragraph done.",
		},
		{
			name:       "blank line inside open code fence is not a boundary",
			content:    "Intro.\n\n```go\nfunc main() {\n\n\tprintln(1)",
			wantPrefix: "Intro.",
		},
		{
			name:       "closed code fence then blank line is safe",
			content:    "```go\nx := 1\n```\n\nNext partial",
			wantPrefix: "```go\nx := 1\n```",
		},
		{
			name:       "blank line inside open tilde fence is not a boundary",
			content:    "Intro.\n\n~~~\nblock\n\nstill in block",
			wantPrefix: "Intro.",
		},
		{
			name:       "inline code backticks are not a fence",
			content:    "Use `go test` to run.\n\nNext partial paragraph",
			wantPrefix: "Use `go test` to run.",
		},
		{
			name:       "consecutive blank lines pick the latest safe boundary",
			content:    "One.\n\nTwo.\n\nThree partial",
			wantPrefix: "One.\n\nTwo.",
		},
		{
			name:       "second closed fence then blank is safe",
			content:    "```\na\n```\n\ntext\n\n```\nb\n```\n\ntail",
			wantPrefix: "```\na\n```\n\ntext\n\n```\nb\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := findSafeMarkdownBoundary(tt.content)
			if tt.wantPrefix == "" {
				if b != 0 {
					t.Fatalf("expected no boundary (0), got %d (prefix=%q)", b, tt.content[:b])
				}
				return
			}
			if b <= 0 {
				t.Fatalf("expected a boundary, got %d", b)
			}
			got := tt.content[:b]
			if got != tt.wantPrefix {
				t.Fatalf("prefix mismatch:\n got: %q\nwant: %q", got, tt.wantPrefix)
			}
			// Critical invariant: the prefix must have balanced code fences.
			if strings.Count(got, "```")%2 != 0 {
				t.Fatalf("boundary split inside an open code fence: %q", got)
			}
		})
	}
}

// referenceSafeBoundary is a deliberately simple (re-scan per candidate)
// implementation used to cross-check the optimized single-pass version.
func referenceSafeBoundary(content string) int {
	insideFence := func(s string) bool {
		open := false
		var fc byte
		for _, line := range strings.Split(s, "\n") {
			t := strings.TrimSpace(line)
			if len(t) < 3 || (t[0] != '`' && t[0] != '~') {
				continue
			}
			c := t[0]
			n := 0
			for n < len(t) && t[n] == c {
				n++
			}
			if n < 3 {
				continue
			}
			if !open {
				open, fc = true, c
			} else if c == fc {
				open = false
			}
		}
		return open
	}
	idx := strings.LastIndex(content, "\n\n")
	for idx > 0 {
		if !insideFence(content[:idx]) {
			return idx
		}
		idx = strings.LastIndex(content[:idx], "\n\n")
	}
	return 0
}

func TestFindSafeMarkdownBoundaryMatchesReference(t *testing.T) {
	inputs := []string{
		"",
		"single line",
		"a\n\nb",
		"a\n\nb\n\nc partial",
		"```\nopen fence\n\nstill open",
		"```\nx\n```\n\nafter",
		"~~~\ny\n\nstill in tilde",
		"text `inline` more\n\nnext",
		"p1\n\n```\ncode\n\nmore code\n```\n\np2\n\np3 partial",
		"\n\n\n\n",
		"no blanks at all just words and words",
	}
	for _, in := range inputs {
		if got, want := findSafeMarkdownBoundary(in), referenceSafeBoundary(in); got != want {
			t.Errorf("findSafeMarkdownBoundary(%q) = %d, reference = %d", in, got, want)
		}
	}
}

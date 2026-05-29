package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// MarkdownRenderer handles markdown rendering with caching support.
type MarkdownRenderer struct {
	renderer *glamour.TermRenderer
	width    int
	isDark   bool

	// Stable-prefix streaming cache: the rendered output of the last stable
	// markdown prefix, reused across paints so we only re-render when a new
	// safe boundary is crossed.
	cachedStreamPrefix string
	cachedStreamRender string
}

func glamourStyle(isDark bool) string {
	if noColor {
		return "notty"
	}
	if isDark {
		return "dark"
	}
	return "light"
}

// NewMarkdownRenderer creates a renderer for the given terminal width and theme.
func NewMarkdownRenderer(width int, isDark bool) *MarkdownRenderer {
	// Use standard glamour style with word wrapping
	// Glamour automatically handles syntax highlighting via Chroma
	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle(isDark)),
		glamour.WithWordWrap(width-4),
	)

	return &MarkdownRenderer{
		renderer: r,
		width:    width,
		isDark:   isDark,
	}
}

// RenderFull renders a complete markdown document (for finished messages).
// This is the "format-on-complete" path used when streaming ends.
func (mr *MarkdownRenderer) RenderFull(content string) string {
	if content == "" || mr.renderer == nil {
		return content
	}

	rendered, err := mr.renderer.Render(content)
	if err != nil {
		return content
	}

	return strings.TrimRight(rendered, "\n")
}

// RenderStreaming renders content during streaming (plain text, no Glamour).
// This avoids jitter from re-rendering incomplete markdown.
func (mr *MarkdownRenderer) RenderStreaming(content string) string {
	return content
}

// RenderStreamingFormatted renders in-progress content using the stable-prefix
// technique: the portion of the document up to the last safe markdown boundary
// (a blank line not inside an open code fence) is rendered with Glamour and
// cached; the trailing partial paragraph is returned separately to be shown as
// plain text. This makes streaming look formatted instead of "popping" into
// shape on completion, without the jitter of re-rendering incomplete markdown.
func (mr *MarkdownRenderer) RenderStreamingFormatted(content string) (formatted, tail string) {
	if mr == nil || mr.renderer == nil || content == "" {
		return "", content
	}

	b := findSafeMarkdownBoundary(content)
	if b <= 0 {
		// No complete block yet — stream the whole thing as plain text.
		return "", content
	}

	prefix := content[:b]
	tail = strings.TrimLeft(content[b:], "\n")

	if prefix == mr.cachedStreamPrefix {
		return mr.cachedStreamRender, tail
	}
	rendered := strings.TrimRight(mr.RenderFull(prefix), "\n")
	mr.cachedStreamPrefix = prefix
	mr.cachedStreamRender = rendered
	return rendered, tail
}

// findSafeMarkdownBoundary returns the byte offset of the latest blank-line
// paragraph break in content that is not inside an open fenced code block, so
// we never split a fence in half. Returns 0 if there is no safe boundary yet.
//
// It runs in a single linear pass: fence state is tracked line-by-line as we
// scan, and each "\n\n" is recorded as the latest safe boundary whenever the
// fence is currently closed. Both ``` and ~~~ fences are tracked (a fence only
// closes with the character that opened it), and fence markers must be at the
// start of a line, so inline code like `foo` mid-sentence never counts.
func findSafeMarkdownBoundary(content string) int {
	fenceOpen := false
	var fenceChar byte
	lineStart := 0
	lastSafe := 0

	for i := 0; i < len(content); i++ {
		if content[i] != '\n' {
			continue
		}
		// The line content[lineStart:i] is now complete; fold it into fence state.
		applyFenceLine(content[lineStart:i], &fenceOpen, &fenceChar)
		// A blank line (a second '\n') marks a paragraph boundary at i. It is
		// safe to split there only if no code fence is currently open.
		if i+1 < len(content) && content[i+1] == '\n' && !fenceOpen {
			lastSafe = i
		}
		lineStart = i + 1
	}
	return lastSafe
}

// applyFenceLine toggles fence state if line is a fence marker (a leading run of
// >=3 backticks or tildes). A fence only closes with the char that opened it.
func applyFenceLine(line string, open *bool, fenceChar *byte) {
	t := strings.TrimSpace(line)
	if len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return
	}
	c := t[0]
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n < 3 {
		return
	}
	switch {
	case !*open:
		*open, *fenceChar = true, c
	case c == *fenceChar:
		*open = false
	}
}

// SetWidth updates the renderer for a new terminal width.
func (mr *MarkdownRenderer) SetWidth(width int) {
	mr.width = width
	mr.cachedStreamPrefix = ""
	mr.cachedStreamRender = ""
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle(mr.isDark)),
		glamour.WithWordWrap(width-4),
	)
	if err == nil {
		mr.renderer = r
	}
}

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

// SetWidth updates the renderer for a new terminal width.
func (mr *MarkdownRenderer) SetWidth(width int) {
	mr.width = width
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle(mr.isDark)),
		glamour.WithWordWrap(width-4),
	)
	if err == nil {
		mr.renderer = r
	}
}

package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// MarkdownRenderer handles markdown rendering with caching support.
type MarkdownRenderer struct {
	renderer *glamour.TermRenderer
	width    int
}

func glamourStyle() string {
	if noColor {
		return "notty"
	}
	return "dark"
}

// NewMarkdownRenderer creates a renderer for the given terminal width.
func NewMarkdownRenderer(width int) *MarkdownRenderer {
	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle()),
		glamour.WithWordWrap(width-4),
	)

	return &MarkdownRenderer{
		renderer: r,
		width:    width,
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
		glamour.WithStandardStyle(glamourStyle()),
		glamour.WithWordWrap(width-4),
	)
	if err == nil {
		mr.renderer = r
	}
}

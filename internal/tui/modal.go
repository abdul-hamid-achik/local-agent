package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ModalConfig holds configuration for rendering a modal dialog.
type ModalConfig struct {
	Title         string          // Modal title
	Content       string          // Main content (can be multi-line)
	Footer        string          // Footer hints
	Width         int             // Content width (excluding padding/border)
	MaxWidth      int             // Maximum width cap
	BorderStyle   lipgloss.Border // Border style
	PaddingTop    int             // Top padding
	PaddingBottom int             // Bottom padding
	PaddingLeft   int             // Left padding
	PaddingRight  int             // Right padding
}

// DefaultModalConfig returns a ModalConfig with sensible defaults.
func DefaultModalConfig() ModalConfig {
	return ModalConfig{
		MaxWidth:      60,
		BorderStyle:   lipgloss.RoundedBorder(),
		PaddingTop:    1,
		PaddingBottom: 1,
		PaddingLeft:   2,
		PaddingRight:  2,
	}
}

// RenderModal renders a centered modal dialog with the given configuration.
// The modal is centered both horizontally and vertically over the base content.
func RenderModal(baseContent string, config ModalConfig, styles Styles, viewportWidth, viewportHeight int) string {
	cfg := DefaultModalConfig()
	
	// Apply overrides
	if config.Title != "" {
		cfg.Title = config.Title
	}
	if config.Content != "" {
		cfg.Content = config.Content
	}
	if config.Footer != "" {
		cfg.Footer = config.Footer
	}
	if config.Width > 0 {
		cfg.Width = config.Width
	}
	if config.MaxWidth > 0 {
		cfg.MaxWidth = config.MaxWidth
	}
	// Use provided border style if different from default
	if config.BorderStyle != (lipgloss.Border{}) {
		cfg.BorderStyle = config.BorderStyle
	}
	cfg.PaddingTop = config.PaddingTop
	cfg.PaddingBottom = config.PaddingBottom
	cfg.PaddingLeft = config.PaddingLeft
	cfg.PaddingRight = config.PaddingRight

	// Build content
	var b strings.Builder
	
	if cfg.Title != "" {
		b.WriteString(styles.OverlayTitle.Render(cfg.Title))
		b.WriteString("\n")
	}
	
	if cfg.Content != "" {
		b.WriteString(cfg.Content)
		if cfg.Footer != "" {
			b.WriteString("\n\n")
		}
	}
	
	if cfg.Footer != "" {
		b.WriteString(styles.OverlayDim.Render(cfg.Footer))
	}

	// Calculate final width
	contentW := cfg.Width
	if contentW == 0 {
		// Auto-width based on content
		lines := strings.Split(b.String(), "\n")
		for _, line := range lines {
			w := lipgloss.Width(line)
			if w+cfg.PaddingLeft+cfg.PaddingRight+2 > contentW {
				contentW = w + cfg.PaddingLeft + cfg.PaddingRight + 2
			}
		}
	}
	
	// Apply max width
	if contentW > cfg.MaxWidth {
		contentW = cfg.MaxWidth
	}
	
	// Ensure minimum width
	if contentW < 30 {
		contentW = 30
	}
	
	// Don't exceed viewport width
	if contentW >= viewportWidth-4 {
		contentW = viewportWidth - 4
	}

	// Create box style
	box := lipgloss.NewStyle().
		Border(cfg.BorderStyle).
		BorderForeground(lipgloss.Color(styles.OverlayBorder)).
		Padding(cfg.PaddingTop, cfg.PaddingLeft, cfg.PaddingBottom, cfg.PaddingRight).
		Width(contentW)

	return box.Render(b.String())
}

// CenterOverlay centers an overlay string over base content.
// This is a helper for the common pattern of rendering modals on top of viewport content.
func CenterOverlay(baseContent, overlay string, viewportWidth, viewportHeight int) string {
	baseLines := strings.Split(baseContent, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// Center vertically
	startY := (len(baseLines) - len(overlayLines)) / 2
	if startY < 0 {
		startY = 0
	}

	// Overlay each line
	for i, ol := range overlayLines {
		row := startY + i
		if row >= len(baseLines) {
			break
		}
		// Center horizontally
		olW := lipgloss.Width(ol)
		padLeft := (viewportWidth - olW) / 2
		if padLeft < 0 {
			padLeft = 0
		}
		baseLines[row] = strings.Repeat(" ", padLeft) + ol
	}

	return strings.Join(baseLines, "\n")
}

// ModalBuilder provides a fluent API for building modals.
type ModalBuilder struct {
	config ModalConfig
}

// NewModal creates a new ModalBuilder.
func NewModal() *ModalBuilder {
	return &ModalBuilder{
		config: DefaultModalConfig(),
	}
}

// Title sets the modal title.
func (mb *ModalBuilder) Title(title string) *ModalBuilder {
	mb.config.Title = title
	return mb
}

// Content sets the modal content.
func (mb *ModalBuilder) Content(content string) *ModalBuilder {
	mb.config.Content = content
	return mb
}

// Footer sets the footer hints.
func (mb *ModalBuilder) Footer(footer string) *ModalBuilder {
	mb.config.Footer = footer
	return mb
}

// Width sets the modal width.
func (mb *ModalBuilder) Width(width int) *ModalBuilder {
	mb.config.Width = width
	return mb
}

// MaxWidth sets the maximum modal width.
func (mb *ModalBuilder) MaxWidth(maxWidth int) *ModalBuilder {
	mb.config.MaxWidth = maxWidth
	return mb
}

// Build renders the modal.
func (mb *ModalBuilder) Build(styles Styles, viewportWidth, viewportHeight int) string {
	return RenderModal("", mb.config, styles, viewportWidth, viewportHeight)
}

// BuildOnContent renders the modal centered over the given base content.
func (mb *ModalBuilder) BuildOnContent(baseContent string, styles Styles, viewportWidth, viewportHeight int) string {
	modal := RenderModal("", mb.config, styles, viewportWidth, viewportHeight)
	return CenterOverlay(baseContent, modal, viewportWidth, viewportHeight)
}

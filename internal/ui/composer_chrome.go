package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// composerFrameEnabled is true when the frame has room for a bordered draft
// without starving the minimum welcome + notice contract (30×12).
func (m *Model) composerFrameEnabled() bool {
	return m != nil && m.ready && m.height >= 16 && m.width >= 40
}

// renderComposerChrome paints the draft surface. On roomy frames it uses a
// single Grok-style rounded border around the textarea only — model/mode live
// on the bottom shortcuts row (right side), not as a second meta line under
// the box. On minimum terminals it falls back to the plain textarea.
//
// Important: never mutate the live textarea width/height here — reflow during
// paint desyncs inputLines from the projected footer and overflows the frame.
func (m *Model) renderComposerChrome() (string, *tea.Cursor) {
	if m == nil {
		return "", nil
	}
	paneW := m.chatPaneWidth()
	input := m.input
	if m.state != StateIdle {
		input.Placeholder = "Write a follow-up · enter queue"
	}
	input.SetVirtualCursor(false)

	if !m.composerFrameEnabled() {
		return input.View(), input.Cursor()
	}

	body := strings.TrimRight(input.View(), "\n")
	innerW := max(1, paneW-2)
	palette := newSemanticPalette(m.isDark)
	border := lipgloss.NewStyle().Foreground(palette.Border)
	// ASCII profile keeps box-drawing off so glyph contracts stay pure.
	topLeft, topRight, botLeft, botRight, horiz, vert := "╭", "╮", "╰", "╯", "─", "│"
	if m.glyphProfile == GlyphASCII {
		topLeft, topRight, botLeft, botRight, horiz, vert = "+", "+", "+", "+", "-", "|"
	}
	top := border.Render(topLeft + strings.Repeat(horiz, innerW) + topRight)
	bot := border.Render(botLeft + strings.Repeat(horiz, innerW) + botRight)
	side := border.Render(vert)

	var framed strings.Builder
	framed.WriteString(top)
	framed.WriteByte('\n')
	if body == "" {
		framed.WriteString(side)
		framed.WriteString(strings.Repeat(" ", innerW))
		framed.WriteString(side)
		framed.WriteByte('\n')
	} else {
		for _, line := range strings.Split(body, "\n") {
			plain := ansi.Strip(line)
			pad := innerW - lipgloss.Width(plain)
			if pad < 0 {
				line = truncateDisplayWithGlyphProfile(line, innerW, m.glyphProfile)
				pad = innerW - lipgloss.Width(ansi.Strip(line))
			}
			if pad < 0 {
				pad = 0
			}
			framed.WriteString(side)
			framed.WriteString(line)
			if pad > 0 {
				framed.WriteString(strings.Repeat(" ", pad))
			}
			framed.WriteString(side)
			framed.WriteByte('\n')
		}
	}
	framed.WriteString(bot)

	// Border insets: +1 column (left edge), +1 row (top edge).
	cursor := offsetCursor(input.Cursor(), 1, 1)
	return strings.TrimRight(framed.String(), "\n"), cursor
}

// renderFooterIdentityRight is the Grok bottom-right identity: model · mode.
// Painted on the same row as shortcuts (not under the composer box).
func (m *Model) renderFooterIdentityRight(budget int) string {
	if m == nil || budget < 8 {
		return ""
	}
	parts := make([]string, 0, 2)
	if model := m.currentModelSurfaceLabel(budget < 28); model != "" {
		parts = append(parts, m.styles.Dimmed.Render(
			truncateDisplayWithGlyphProfile(model, min(22, budget), m.glyphProfile),
		))
	}
	presented := m.presentedMode()
	// PLAN/AUTO labels are short; keep them once there is room after the model.
	if presented != ModeNormal && budget >= 12 {
		cfg := m.modeConfigs[presented]
		var style lipgloss.Style
		switch presented {
		case ModePlan:
			style = m.styles.ModePlan
		case ModeAuto:
			style = m.styles.ModeBuild
		default:
			style = m.styles.ModeAsk
		}
		parts = append(parts, style.Render(cfg.Label))
	}
	if len(parts) == 0 {
		return ""
	}
	sep := m.styles.Dimmed.Render(glyphSeparator(m.glyphProfile))
	line := strings.Join(parts, sep)
	if lipgloss.Width(line) > budget {
		// Drop mode first, then truncate model.
		if len(parts) > 1 {
			line = parts[0]
		}
		line = truncateDisplayWithGlyphProfile(ansi.Strip(line), budget, m.glyphProfile)
		line = m.styles.Dimmed.Render(line)
	}
	return line
}

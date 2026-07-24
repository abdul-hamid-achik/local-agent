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

// blankRowsLike returns whitespace with the same row count as value, so a
// surface can be emptied without changing the height the layout already
// reserved for it.
func blankRowsLike(value string) string {
	return strings.Repeat("\n", strings.Count(strings.TrimRight(value, "\n"), "\n"))
}

// renderComposerChrome paints the draft surface. On roomy frames it uses a
// single Grok-style rounded border around the textarea only — model/mode live
// on the bottom shortcuts row (right side), not as a second meta line under
// the box. On minimum terminals it falls back to the plain textarea.
//
// Important: never mutate the live textarea width/height here — reflow during
// paint desyncs inputLines from the projected footer and overflows the frame.
func (m *Model) renderComposerChrome() (string, *tea.Cursor) {
	return m.renderComposerChromeBody(false)
}

// renderInertComposerChrome paints the composer's frame at its exact live
// height with an empty interior. Overlays use it so the draft surface keeps its
// allocation — reflowing the transcript when a modal opens is jarring — without
// leaking the draft under the panel or showing a prompt that looks focused
// while a modal owns input. Painting nothing there was the previous behavior
// and it stranded the divider above an empty band on every overlay screen.
func (m *Model) renderInertComposerChrome() string {
	view, _ := m.renderComposerChromeBody(true)
	return view
}

func (m *Model) renderComposerChromeBody(inert bool) (string, *tea.Cursor) {
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
		if inert {
			return blankRowsLike(input.View()), nil
		}
		return input.View(), input.Cursor()
	}

	body := strings.TrimRight(input.View(), "\n")
	if inert {
		// Same row count, no content: geometry is identical to the live frame.
		body = blankRowsLike(body)
	}
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

// renderFooterIdentityRight is the bottom-right identity on the shortcuts row.
//
// It carries only what this frame assigned to surfaceShortcuts. On a roomy
// frame that is the authority badge alone: the model name lives in the top bar,
// beside the context meter it belongs with. On frames without a top bar the
// plan falls the model through to here instead.
func (m *Model) renderFooterIdentityRight(budget int) string {
	if m == nil || budget < 8 {
		return ""
	}
	plan := m.planStatus()
	parts := make([]string, 0, 2)

	if m.currentModelIsNonLocal() {
		if plan.owns(factRemoteBoundary, surfaceShortcuts) {
			parts = append(parts, m.styles.StatusWarning.Render(
				truncateDisplayWithGlyphProfile(
					m.currentModelReachabilityLabel(budget < 28), budget, m.glyphProfile,
				),
			))
		}
	} else if plan.owns(factModel, surfaceShortcuts) {
		if model := m.currentModelReachabilityLabel(budget < 28); model != "" {
			parts = append(parts, m.styles.Dimmed.Render(
				truncateDisplayWithGlyphProfile(model, min(22, budget), m.glyphProfile),
			))
		}
	}

	presented := m.presentedMode()
	// NORMAL is the default authority and needs no badge; PLAN and AUTO change
	// what the host will do and always earn their label here.
	if plan.owns(factMode, surfaceShortcuts) && presented != ModeNormal && budget >= 6 {
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
		// Authority is last in and first kept: it changes what the host will do.
		if len(parts) > 1 {
			line = parts[len(parts)-1]
		}
		line = truncateDisplayWithGlyphProfile(ansi.Strip(line), budget, m.glyphProfile)
		line = m.styles.Dimmed.Render(line)
	}
	return line
}

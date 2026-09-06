package ui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The draft lives in a rounded box whose Lip Gloss Width is ContentWidth.
// OriginX spaces indent every row so the left gutter matches the right chrome
// (Grok: the box is inset on both sides, not flush-left with slack only on
// the right). Border and padding sit inside that Width.
const (
	composerPromptColumns = 2
	composerFrameCursorX  = 2
	composerFrameCursorY  = 1
)

func composerFrameStyle(outerWidth int, profile GlyphProfile, borderFg color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(borderForGlyphProfile(profile)).
		BorderForeground(borderFg).
		Padding(0, 1).
		Width(max(4, outerWidth))
}

func composerInnerWidth(outerWidth int, profile GlyphProfile) int {
	frame := composerFrameStyle(outerWidth, profile, lipgloss.NoColor{})
	return max(1, frame.GetWidth()-frame.GetHorizontalFrameSize())
}

func (m *Model) composerInnerWidth() int {
	if m == nil {
		return 1
	}
	return composerInnerWidth(m.contentGrid().ContentWidth(), m.glyphProfile)
}

// composerFrameActive is the Grok-style rounded draft box. Two border rows
// only pay for themselves once the transcript floor and shortcuts still fit;
// the 30×12 recovery frame keeps an unframed draft on the same LineWidth.
func (m *Model) composerFrameActive() bool {
	if m == nil || !m.ready {
		return false
	}
	return m.height >= 16 && m.chatPaneWidth() >= 36
}

// composerTextWidth is the textarea wrap budget. Framed drafts use the inner
// column of the ContentWidth box; compact drafts use ContentWidth so a
// Prefix indent keeps them on the same LineWidth as the framed box.
func (m *Model) composerTextWidth() int {
	if m.composerFrameActive() {
		return m.composerInnerWidth()
	}
	return max(1, m.contentGrid().ContentWidth())
}

func indentDisplay(prefix, block string) string {
	if prefix == "" {
		return block
	}
	if block == "" {
		return prefix
	}
	lines := strings.Split(block, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// blankRowsLike returns whitespace with the same row count as value, so a
// surface can be emptied without changing the height the layout already
// reserved for it.
func blankRowsLike(value string) string {
	return strings.Repeat("\n", strings.Count(strings.TrimRight(value, "\n"), "\n"))
}

func composerPrompt(profile GlyphProfile, lineNumber int) string {
	glyphs := glyphSet(profile)
	if lineNumber == 0 {
		if profile == GlyphASCII {
			return "> "
		}
		return "❯ "
	}
	return glyphs.Vertical + " "
}

// blankComposerInner keeps the framed draft's inner row count without leaking
// the live prompt. A single space holds the content row so Lip Gloss does not
// collapse a one-line box to borders only.
func blankComposerInner(view string) string {
	view = strings.TrimRight(view, "\n")
	rows := 1
	if view != "" {
		rows = strings.Count(view, "\n") + 1
	}
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = " "
	}
	return strings.Join(lines, "\n")
}

// renderComposerChrome paints the draft surface.
//
// Important: never mutate the live textarea width/height here — reflow during
// paint desyncs inputLines from the projected footer and overflows the frame.
func (m *Model) renderComposerChrome() (string, *tea.Cursor) {
	return m.renderComposerChromeBody(false)
}

// renderInertComposerChrome paints the composer at its exact live height with
// no content. Overlays use it so the draft surface keeps its allocation —
// reflowing the transcript when a modal opens is jarring — without leaking the
// draft under the panel or showing a prompt that looks focused while a modal
// owns input.
func (m *Model) renderInertComposerChrome() string {
	view, _ := m.renderComposerChromeBody(true)
	return view
}

func (m *Model) renderComposerChromeBody(inert bool) (string, *tea.Cursor) {
	if m == nil {
		return "", nil
	}
	input := m.input
	if m.state != StateIdle {
		input.Placeholder = "Write a follow-up · enter queue"
	}
	input.SetVirtualCursor(false)

	grid := m.contentGrid()
	lead := grid.Prefix(" ")
	leadW := lipgloss.Width(lead)
	inner := strings.TrimRight(input.View(), "\n")
	if !m.composerFrameActive() {
		if inert {
			return indentDisplay(lead, blankRowsLike(input.View())), nil
		}
		return indentDisplay(lead, inner), offsetCursor(input.Cursor(), leadW, 0)
	}
	if inert {
		inner = blankComposerInner(inner)
	}

	frame := composerFrameStyle(grid.ContentWidth(), m.glyphProfile, m.styles.OverlayBorder)
	framed := indentDisplay(lead, frame.Render(inner))
	if inert {
		return blankRowsLike(framed), nil
	}
	return framed, offsetCursor(input.Cursor(), leadW+composerFrameCursorX, composerFrameCursorY)
}

// footerPaintsComposerFrame reports whether this footer owns the rounded draft
// box. When it does, the box top edge is the transcript boundary and the
// full-pane divider is omitted so the shared LineWidth column stays honest.
func (m *Model) footerPaintsComposerFrame() bool {
	if m == nil || !m.composerFrameActive() {
		return false
	}
	if m.readScopePrompt != nil || m.pendingPaste != nil || m.pendingSessionSwitch != nil {
		return false
	}
	if m.overlay == OverlayTranscriptSearch && m.transcriptSearch != nil {
		return false
	}
	if m.overlay == OverlayPlanForm && m.planFormState != nil {
		return false
	}
	if m.overlay == OverlayGoalForm && m.goalFormState != nil {
		return false
	}
	if m.pendingApproval != nil || m.pendingQuestion != nil {
		return true
	}
	if m.overlay == OverlayCompletion && m.isCompletionActive() {
		return true
	}
	if m.overlay != OverlayNone {
		return true
	}
	if m.queuedFollowUp != nil && (!m.queuedFollowUpHeld() || !m.composerEditable()) {
		return false
	}
	return m.composerEditable()
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

package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// FrameSurfaceProjection binds painted content to the exact half-open cell
// rectangle owned by that surface.
type FrameSurfaceProjection struct {
	Rect    CellRect
	Content string
	Visible bool
}

const minTranscriptRows = 4

// FrameVerticalFit records which vertical policy produced the frame. It keeps
// cramped layouts observable without making View infer intent from rectangle
// sizes.
type FrameVerticalFit uint8

const (
	// FrameVerticalComfortable means the complete footer, including ordinary
	// separation chrome, fit alongside the transcript floor.
	FrameVerticalComfortable FrameVerticalFit = iota
	// FrameVerticalCondensed means only redundant divider/spacing rows were
	// removed to preserve the transcript floor. No control or authored footer
	// content was hidden.
	FrameVerticalCondensed
	// FrameVerticalOwnerPriority means the footer's critical controls alone
	// exceeded the remaining rows. The controls remain complete and the
	// transcript receives the physical remainder, which can be below its floor.
	FrameVerticalOwnerPriority
	// FrameVerticalRecovery means the terminal-size recovery surface replaces
	// the base transcript/footer entirely. Its geometry remains canonical, but
	// neither base surface is painted.
	FrameVerticalRecovery
)

// FrameProjection is the single geometry snapshot consumed by View and by
// viewport sizing. The current migration projects the transcript and footer;
// overlays remain a z-layer over these stable base rectangles.
type FrameProjection struct {
	Screen              CellRect
	SafeScreen          CellRect
	WidthClass          WidthClass
	HeightClass         HeightClass
	TranscriptFloorRows int
	TranscriptLayout    LayoutCapabilities
	VerticalFit         FrameVerticalFit
	Transcript          FrameSurfaceProjection
	Footer              FrameSurfaceProjection
	Cursor              *tea.Cursor
}

type footerProjection struct {
	content         string
	cursor          *tea.Cursor
	reservedHeight  int
	verticalFit     FrameVerticalFit
	dividerRows     int
	activityGapRows int
}

type footerProjectionOptions struct {
	showDivider     bool
	showActivityGap bool
}

// projectFrame computes base geometry exactly once from the current model
// state. Right and bottom compatibility insets preserve the established Charm
// renderer contract while making those cells explicit instead of scattering
// "-1" arithmetic through children.
func (m *Model) projectFrame() FrameProjection {
	screen := NewCellRect(0, 0, max(0, m.width), max(0, m.height))
	safe := Inset(screen, Insets{Right: 1, Bottom: 1})
	transcriptFloor := min(minTranscriptRows, safe.Height())
	if m.narrowTerminalHint() != "" {
		emptyWorkRect := NewCellRect(safe.MinX, safe.MinY, safe.MinX, safe.MinY)
		return FrameProjection{
			Screen:              screen,
			SafeScreen:          safe,
			WidthClass:          ClassifyWidth(m.width),
			HeightClass:         ClassifyHeight(m.height),
			TranscriptFloorRows: transcriptFloor,
			TranscriptLayout: DeriveLayoutCapabilities(
				emptyWorkRect,
				LayoutCapabilityOptions{ForceCompact: m.forceCompact},
			),
			VerticalFit: FrameVerticalRecovery,
			Transcript: FrameSurfaceProjection{
				Rect: safe,
			},
			Footer: FrameSurfaceProjection{
				Rect: NewCellRect(safe.MinX, safe.MaxY, safe.MaxX, safe.MaxY),
			},
		}
	}
	footer := m.projectFooterWithin(max(0, safe.Height()-transcriptFloor))
	footerRect, transcriptRect := TakeBottom(safe, footer.reservedHeight)
	transcriptLayout := DeriveLayoutCapabilities(
		transcriptWorkRect(transcriptRect),
		LayoutCapabilityOptions{ForceCompact: m.forceCompact},
	)

	var cursor *tea.Cursor
	if footer.cursor != nil {
		cursor = offsetCursor(footer.cursor, footerRect.MinX, footerRect.MinY)
	}

	return FrameProjection{
		Screen:              screen,
		SafeScreen:          safe,
		WidthClass:          ClassifyWidth(m.width),
		HeightClass:         ClassifyHeight(m.height),
		TranscriptFloorRows: transcriptFloor,
		TranscriptLayout:    transcriptLayout,
		VerticalFit:         footer.verticalFit,
		Transcript: FrameSurfaceProjection{
			Rect:    transcriptRect,
			Visible: !transcriptRect.Empty(),
		},
		Footer: FrameSurfaceProjection{
			Rect:    footerRect,
			Content: footer.content,
			Visible: footer.reservedHeight > 0,
		},
		Cursor: cursor,
	}
}

// projectFooterWithin first attempts the complete footer, then removes only
// redundant separation chrome if that is enough to preserve the transcript
// floor. A critical owner (approval, form, completion, or composer) is never
// clipped after rendering merely to satisfy geometry.
func (m *Model) projectFooterWithin(maxHeight int) footerProjection {
	preferred := m.projectFooterWithOptions(footerProjectionOptions{
		showDivider:     true,
		showActivityGap: true,
	})
	if preferred.reservedHeight <= maxHeight {
		return preferred
	}

	if preferred.activityGapRows > 0 &&
		preferred.reservedHeight-preferred.activityGapRows <= maxHeight {
		withoutGap := m.projectFooterWithOptions(footerProjectionOptions{
			showDivider:     true,
			showActivityGap: false,
		})
		if withoutGap.reservedHeight <= maxHeight {
			withoutGap.verticalFit = FrameVerticalCondensed
			return withoutGap
		}
	}

	if preferred.activityGapRows == 0 && preferred.dividerRows == 0 {
		preferred.verticalFit = FrameVerticalOwnerPriority
		return preferred
	}
	critical := m.projectFooterWithOptions(footerProjectionOptions{
		showDivider:     false,
		showActivityGap: false,
	})
	if critical.reservedHeight <= maxHeight {
		critical.verticalFit = FrameVerticalCondensed
		return critical
	}
	critical.verticalFit = FrameVerticalOwnerPriority
	return critical
}

// projectFooterWithOptions is the only footer renderer. It produces both the
// exact bytes painted by View and the row budget consumed by projectFrame, so
// adding a new owner cannot update paint without also updating layout.
func (m *Model) projectFooterWithOptions(options footerProjectionOptions) footerProjection {
	p := footerProjection{}
	var content strings.Builder

	if options.showDivider && !m.compactCompletionOwnsDivider() {
		content.WriteString(m.styles.Divider.Render(
			ruleWithGlyphProfile(m.chatPaneWidth(), m.glyphProfile),
		))
		content.WriteString("\n")
		p.dividerRows++
	}
	if plan := m.renderGoalPlan(); plan != "" {
		content.WriteString(plan)
		content.WriteString("\n")
	}
	if bob := m.renderBobWorkspaceContext(); bob != "" {
		content.WriteString(bob)
		content.WriteString("\n")
	}
	if action := m.renderContinuationAction(); action != "" && !m.goalPlanOwnsContinuation() {
		content.WriteString(action)
		content.WriteString("\n")
	}
	if status := m.renderStatusLine(); status != "" {
		content.WriteString(status)
		// The trailing newline owns the existing safety row for multi-row
		// minimum-height decision surfaces.
		content.WriteString("\n")
		if options.showActivityGap && m.activityComposerGap() {
			content.WriteString("\n")
			p.activityGapRows++
		}
	}

	switch {
	case m.pendingApproval != nil:
		approval := m.renderApproval()
		content.WriteString(approval)
	case m.readScopePrompt != nil:
		prompt := m.renderReadScopePrompt()
		content.WriteString(prompt)
	case m.pendingPaste != nil || m.pendingSessionSwitch != nil:
		// The status projection above owns the footer until the user answers.
	case m.overlay == OverlayCortexDecision && m.cortexDecision != nil:
		decision := m.cortexDecision.View(m.cortexDecisionBusyMarker())
		content.WriteString(decision)
	case m.overlay == OverlayCompletion && m.isCompletionActive():
		popupY := strings.Count(content.String(), "\n")
		popup, popupCursor := m.renderCompletionModalView()
		content.WriteString(popup)
		content.WriteString("\n")
		input := m.input
		input.SetVirtualCursor(false)
		content.WriteString(input.View())
		p.cursor = offsetCursor(popupCursor, 0, popupY)
	case m.overlay == OverlayPlanForm && m.planFormState != nil:
		formY := strings.Count(content.String(), "\n")
		form, formCursor := m.renderPlanFormView()
		content.WriteString(form)
		p.cursor = offsetCursor(formCursor, 0, formY)
	case m.overlay == OverlayGoalForm && m.goalFormState != nil:
		formY := strings.Count(content.String(), "\n")
		form, formCursor := m.goalFormState.ViewWithCursor()
		content.WriteString(form)
		p.cursor = offsetCursor(formCursor, 0, formY)
	case m.overlay != OverlayNone:
		// Centered overlays keep the draft's allocation in the base frame so
		// opening navigation cannot reflow the transcript behind the scrim.
		input := m.input
		input.SetVirtualCursor(false)
		content.WriteString(strings.Repeat("\n", max(0, lipgloss.Height(input.View())-1)))
	case m.queuedFollowUp != nil && (!m.queuedFollowUpHeld() || !m.composerEditable()):
		queue := m.renderQueuedFollowUp()
		content.WriteString(queue)
	case m.composerEditable():
		if m.queuedFollowUpHeld() {
			queue := m.renderQueuedFollowUp()
			content.WriteString(queue)
			content.WriteString("\n")
		}
		if cue := m.renderComposerOverflowCue(); cue != "" {
			content.WriteString(cue)
			content.WriteString("\n")
		}
		input := m.input
		if m.state != StateIdle {
			input.Placeholder = "Write a follow-up · enter queue"
		}
		input.SetVirtualCursor(false)
		composerY := strings.Count(content.String(), "\n")
		content.WriteString(input.View())
		p.cursor = offsetCursor(input.Cursor(), 0, composerY)
	default:
		// The empty projection below still owns one safe bottom row.
	}

	p.content = content.String()
	p.reservedHeight = 1
	if p.content != "" {
		p.reservedHeight = lipgloss.Height(p.content)
	}
	return p
}

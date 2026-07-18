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

// FrameProjection is the single geometry snapshot consumed by View and by
// viewport sizing. The current migration projects the transcript and footer;
// overlays remain a z-layer over these stable base rectangles.
type FrameProjection struct {
	Screen      CellRect
	SafeScreen  CellRect
	WidthClass  WidthClass
	HeightClass HeightClass
	Transcript  FrameSurfaceProjection
	Footer      FrameSurfaceProjection
	Cursor      *tea.Cursor
}

type footerProjection struct {
	content        string
	cursor         *tea.Cursor
	reservedHeight int
}

// projectFrame computes base geometry exactly once from the current model
// state. Right and bottom compatibility insets preserve the established Charm
// renderer contract while making those cells explicit instead of scattering
// "-1" arithmetic through children.
func (m *Model) projectFrame() FrameProjection {
	screen := NewCellRect(0, 0, max(0, m.width), max(0, m.height))
	safe := Inset(screen, Insets{Right: 1, Bottom: 1})
	footer := m.projectFooter()
	footerRect, transcriptRect := TakeBottom(safe, footer.reservedHeight)

	var cursor *tea.Cursor
	if footer.cursor != nil {
		cursor = offsetCursor(footer.cursor, footerRect.MinX, footerRect.MinY)
	}

	return FrameProjection{
		Screen:      screen,
		SafeScreen:  safe,
		WidthClass:  ClassifyWidth(m.width),
		HeightClass: ClassifyHeight(m.height),
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

// projectFooter is the only footer authority. It produces both the exact
// bytes painted by View and the row budget consumed by projectFrame, so adding
// a new owner cannot update paint without also updating layout.
func (m *Model) projectFooter() footerProjection {
	p := footerProjection{}
	var content strings.Builder

	if !m.compactCompletionOwnsDivider() {
		content.WriteString(m.styles.Divider.Render(rule(m.chatPaneWidth())))
		content.WriteString("\n")
		p.reservedHeight++
	}
	if plan := m.renderGoalPlan(); plan != "" {
		content.WriteString(plan)
		content.WriteString("\n")
		p.reservedHeight += lipgloss.Height(plan)
	}
	if bob := m.renderBobWorkspaceContext(); bob != "" {
		content.WriteString(bob)
		content.WriteString("\n")
		p.reservedHeight += lipgloss.Height(bob)
	}
	if action := m.renderContinuationAction(); action != "" && !m.goalPlanOwnsContinuation() {
		content.WriteString(action)
		content.WriteString("\n")
		p.reservedHeight += lipgloss.Height(action)
	}
	if status := m.renderStatusLine(); status != "" {
		statusRows := lipgloss.Height(status)
		content.WriteString(status)
		content.WriteString("\n")
		p.reservedHeight += statusRows
		if m.activityComposerGap() {
			content.WriteString("\n")
			p.reservedHeight++
		}
		if statusRows > 1 {
			// Preserve the final terminal safety row used by the existing
			// minimum-height decision surfaces.
			p.reservedHeight++
		}
	}

	switch {
	case m.pendingApproval != nil:
		approval := m.renderApproval()
		content.WriteString(approval)
		p.reservedHeight += lipgloss.Height(approval)
	case m.readScopePrompt != nil:
		prompt := m.renderReadScopePrompt()
		content.WriteString(prompt)
		p.reservedHeight += lipgloss.Height(prompt)
	case m.pendingPaste != nil || m.pendingSessionSwitch != nil:
		// The status projection above owns the footer until the user answers.
	case m.overlay == OverlayCortexDecision && m.cortexDecision != nil:
		decision := m.cortexDecision.View(m.cortexDecisionBusyMarker())
		content.WriteString(decision)
		p.reservedHeight += lipgloss.Height(decision)
	case m.overlay == OverlayCompletion && m.isCompletionActive():
		popupY := strings.Count(content.String(), "\n")
		popup, popupCursor := m.renderCompletionModalView()
		content.WriteString(popup)
		content.WriteString("\n")
		input := m.input
		input.SetVirtualCursor(false)
		content.WriteString(input.View())
		p.cursor = offsetCursor(popupCursor, 0, popupY)
		p.reservedHeight += lipgloss.Height(popup) + m.inputLines
	case m.overlay == OverlayPlanForm && m.planFormState != nil:
		formY := strings.Count(content.String(), "\n")
		form, formCursor := m.renderPlanFormView()
		content.WriteString(form)
		p.cursor = offsetCursor(formCursor, 0, formY)
		p.reservedHeight += lipgloss.Height(form)
	case m.overlay == OverlayGoalForm && m.goalFormState != nil:
		formY := strings.Count(content.String(), "\n")
		form, formCursor := m.goalFormState.ViewWithCursor()
		content.WriteString(form)
		p.cursor = offsetCursor(formCursor, 0, formY)
		p.reservedHeight += lipgloss.Height(form)
	case m.overlay != OverlayNone:
		// Centered overlays keep the draft's allocation in the base frame so
		// opening navigation cannot reflow the transcript behind the scrim.
		input := m.input
		input.SetVirtualCursor(false)
		content.WriteString(strings.Repeat("\n", max(0, lipgloss.Height(input.View())-1)))
		p.reservedHeight += m.inputLines
	case m.queuedFollowUp != nil && (!m.queuedFollowUpHeld() || !m.composerEditable()):
		queue := m.renderQueuedFollowUp()
		content.WriteString(queue)
		p.reservedHeight += max(1, lipgloss.Height(queue))
	case m.composerEditable():
		if m.queuedFollowUpHeld() {
			queue := m.renderQueuedFollowUp()
			content.WriteString(queue)
			content.WriteString("\n")
			p.reservedHeight += lipgloss.Height(queue)
		}
		if cue := m.renderComposerOverflowCue(); cue != "" {
			content.WriteString(cue)
			content.WriteString("\n")
			p.reservedHeight++
		}
		input := m.input
		if m.state != StateIdle {
			input.Placeholder = "Write a follow-up · enter queue"
		}
		input.SetVirtualCursor(false)
		composerY := strings.Count(content.String(), "\n")
		content.WriteString(input.View())
		p.cursor = offsetCursor(input.Cursor(), 0, composerY)
		p.reservedHeight += m.inputLines
	default:
		// Retain one safe bottom row when no interactive owner is visible.
		p.reservedHeight++
	}

	p.content = content.String()
	return p
}

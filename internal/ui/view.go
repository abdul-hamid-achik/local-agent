package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *Model) View() tea.View {
	if !m.ready {
		// Bubble Tea has not delivered terminal dimensions yet, so centering would
		// be guesswork. Keep the same product identity and startup language as the
		// full shell instead of flashing an unrelated debug placeholder.
		v := tea.NewView("LOCAL AGENT\nStarting…")
		v.AltScreen = true
		v.WindowTitle = m.windowTitleBase()
		return v
	}
	if hint := m.narrowTerminalHint(); hint != "" {
		return m.renderNarrowTerminalView(hint)
	}
	if m.terminalInputResumeActive() {
		return m.renderTerminalInputResumeView()
	}

	// The conversation, status, and composer own the complete terminal width.
	// Infrequent controls are exposed through overlays instead of persistent
	// chrome that competes with code and tool output.
	paneWidth := m.chatPaneWidth()
	var content strings.Builder
	var viewCursor *tea.Cursor
	content.WriteString(m.viewport.View())
	content.WriteString("\n")
	if !m.compactCompletionOwnsDivider() {
		content.WriteString(m.styles.Divider.Render(rule(paneWidth)))
		content.WriteString("\n")
	}

	// Permission requests replace the composer in-flow. They deliberately do
	// not use overlayOnContent: the transcript stays visible, code keeps the
	// terminal width, and the decision reads as the next conversational action.
	if m.pendingApproval != nil {
		content.WriteString(m.renderApproval())
	} else if m.readScopePrompt != nil {
		content.WriteString(m.renderReadScopePrompt())
	} else {
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
			content.WriteString("\n")
			if m.activityComposerGap() {
				content.WriteString("\n")
			}
		}

		// Ordinary turns keep the composer available so the next instruction can be
		// drafted while work continues. Owned operations and a queued follow-up use
		// the compact liveness line until authority returns to the textarea.
		if m.pendingPaste != nil || m.pendingSessionSwitch != nil {
			// The status prompt above owns the footer until the user answers.
		} else if m.overlay == OverlayCortexDecision && m.cortexDecision != nil {
			// Human decisions are full-width composer owners. They remain below the
			// visible transcript and never mask it with an overlay.
			content.WriteString(m.cortexDecision.View(m.cortexDecisionBusyMarker()))
		} else if m.overlay == OverlayCompletion && m.isCompletionActive() {
			// Completion is owned by the composer: the popup sits immediately above
			// the unchanged textarea, and only its Bubbles filter owns the terminal
			// cursor. Keeping the draft visible makes the replacement span legible.
			popupY := strings.Count(content.String(), "\n")
			popup, popupCursor := m.renderCompletionModalView()
			content.WriteString(popup)
			content.WriteString("\n")
			input := m.input
			input.SetVirtualCursor(false)
			content.WriteString(input.View())
			viewCursor = offsetCursor(popupCursor, 0, popupY)
		} else if m.overlay == OverlayPlanForm && m.planFormState != nil {
			// Structured planning temporarily owns the composer rows. The transcript
			// remains immediately above this full-width inline form.
			formY := strings.Count(content.String(), "\n")
			form, formCursor := m.renderPlanFormView()
			content.WriteString(form)
			viewCursor = offsetCursor(formCursor, 0, formY)
		} else if m.overlay == OverlayGoalForm && m.goalFormState != nil {
			// Goal definition follows the same composer contract as Plan: one typed
			// inline owner, no transcript mask, and one translated Bubbles cursor.
			formY := strings.Count(content.String(), "\n")
			form, formCursor := m.goalFormState.ViewWithCursor()
			content.WriteString(form)
			viewCursor = offsetCursor(formCursor, 0, formY)
		} else if m.overlay != OverlayNone {
			// Preserve the composer's row allocation behind centered overlays so the
			// transcript does not shift when transient navigation opens.
			input := m.input
			input.SetVirtualCursor(false)
			content.WriteString(strings.Repeat("\n", max(0, lipgloss.Height(input.View())-1)))
		} else if m.queuedFollowUp != nil && (!m.queuedFollowUpHeld() || !m.composerEditable()) {
			// The queue owns one stable footer row until it dispatches, is restored
			// after failure, or the user edits/clears it. Never hide pending input in
			// model state with no visible recovery path.
			content.WriteString(m.renderQueuedFollowUp())
		} else if m.composerEditable() {
			if m.queuedFollowUpHeld() {
				// A rejected active turn and the next queued instruction remain two
				// visible owners. Up swaps them atomically instead of concatenating
				// prompts or attachment sets behind the user's back.
				content.WriteString(m.renderQueuedFollowUp())
				content.WriteString("\n")
			}
			if cue := m.renderComposerOverflowCue(); cue != "" {
				content.WriteString(cue)
				content.WriteString("\n")
			}
			// Render a local copy with Bubbles' virtual cursor disabled. The same
			// copy supplies the one real cursor owned by this top-level view.
			input := m.input
			if m.state != StateIdle {
				input.Placeholder = "Write a follow-up · enter queue"
			}
			input.SetVirtualCursor(false)
			inputView := input.View()
			composerY := strings.Count(content.String(), "\n")
			content.WriteString(inputView)
			viewCursor = offsetCursor(input.Cursor(), 0, composerY)
		}
	}

	// Infrequent controls remain centered overlays. Composer-owned completion,
	// Plan, and Goal surfaces were already rendered in the normal footer flow.
	if m.overlay != OverlayNone && m.overlay != OverlayCompletion &&
		m.overlay != OverlayCortexDecision &&
		m.overlay != OverlayPlanForm && m.overlay != OverlayGoalForm {
		var overlay string
		var localCursor *tea.Cursor
		// Every overlay suppresses the underlying composer cursor. Text-entry
		// overlays may replace it with their own translated child cursor below.
		viewCursor = nil
		switch m.overlay {
		case OverlayHelp:
			overlay = m.renderHelpOverlay(m.width)
		case OverlayModelPicker:
			if m.modelPickerState != nil {
				overlay = m.renderModelPicker()
			}
		case OverlayCloudConsent:
			overlay = m.renderCloudConsent()
		case OverlayModelDetails:
			overlay = m.renderModelDetails()
		case OverlayModelPull:
			overlay, localCursor = m.renderModelPull()
		case OverlaySessionsPicker:
			if m.sessionsPickerState != nil {
				overlay = m.renderSessionsPicker()
			}
		case OverlaySettings:
			overlay = m.renderSettingsPicker()
		case OverlayAgentPicker:
			overlay = m.renderAgentPicker()
		case OverlayModePicker:
			overlay = m.renderModePicker()
		case OverlayRuntimeStatus:
			overlay = m.renderRuntimeStatus()
		case OverlayGoalInspector:
			if m.goalInspectorState != nil {
				overlay = m.goalInspectorState.View()
			}
		case OverlayGoalRecovery:
			if m.goalRecoveryState != nil {
				overlay, localCursor = m.goalRecoveryState.ViewWithCursor()
			}
		}
		if overlay != "" {
			base := content.String()
			content.Reset()
			content.WriteString(m.overlayOnContent(base, overlay))
			viewCursor = overlayCursor(base, overlay, m.width, localCursor)
		}
	}

	v := tea.NewView(content.String() + "\n")
	v.AltScreen = true
	// Cell-motion reporting is the smallest mouse mode that delivers wheel
	// events. Without it, terminals commonly translate wheel input in the alt
	// screen into arrow keys, which moves the focused composer instead of the
	// transcript. Native selection remains available through the terminal's
	// mouse-reporting override (commonly Shift-drag), and Ctrl+Y remains the
	// application-level copy path.
	v.MouseMode = tea.MouseModeCellMotion
	v.Cursor = viewCursor

	// Terminal title progress. The workspace basename differentiates several
	// Local Agent tabs without exposing a full private path through terminal
	// title integrations or window-manager history.
	windowTitle := m.windowTitleBase()
	switch m.state {
	case StateWaiting:
		v.WindowTitle = windowTitle + " \u00b7 thinking..."
	case StateStreaming:
		v.WindowTitle = windowTitle + " \u00b7 streaming..."
	default:
		if m.doneFlash {
			v.WindowTitle = windowTitle + " \u00b7 done"
		} else {
			v.WindowTitle = windowTitle
		}
	}

	return v
}

// activityComposerGap gives the live activity rail one row of breathing room
// before the draft surface. Prompt/form owners remain dense because their
// controls already provide their own framing and exact height contract.
func (m *Model) activityComposerGap() bool {
	if _, active := m.currentWorkingActivity(); !active {
		return false
	}
	if m.pendingApproval != nil || m.readScopePrompt != nil || m.pendingPaste != nil || m.overlay != OverlayNone {
		return false
	}
	return m.queuedFollowUp != nil || (m.composerEditable() && m.renderComposerOverflowCue() == "")
}

func (m *Model) conversationStarted() bool {
	for _, entry := range m.entries {
		switch entry.Kind {
		case "user", "assistant", "tool_group":
			return true
		}
	}
	return false
}

func (m *Model) inspectableToolReceiptAction() (string, bool) {
	if m.lastTurnToolIndex < 0 || m.lastTurnToolIndex >= len(m.toolEntries) ||
		m.toolEntries[m.lastTurnToolIndex].Status == ToolStatusRunning {
		return "", false
	}
	if m.toolEntries[m.lastTurnToolIndex].Collapsed {
		return "inspect receipt", true
	}
	return "hide receipt", true
}

// formatTokens formats a token count as "1.2k" or "8192".
func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// formatDuration formats a duration as "42ms" or "1.3s".
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// truncateDisplay truncates plain text by terminal cell width instead of byte
// count, so model names, paths, and tool output containing Unicode stay valid.
func truncateDisplay(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	const ellipsis = "…"
	if maxWidth <= lipgloss.Width(ellipsis) {
		return ellipsis
	}

	budget := maxWidth - lipgloss.Width(ellipsis)
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > budget {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + ellipsis
}

// wrapText wraps text to the given width, breaking long words if needed.
func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	// Fast path: if a single line fits in terminal cells, return as-is.
	if !strings.Contains(s, "\n") && lipgloss.Width(s) <= width {
		return s
	}
	var result strings.Builder
	for _, line := range strings.Split(s, "\n") {
		result.WriteString(wrapLine(line, width))
		result.WriteString("\n")
	}
	// Trim trailing newline
	return strings.TrimSuffix(result.String(), "\n")
}

// wrapLine wraps a single line to the given width, breaking long words if needed.
func wrapLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return ""
	}

	lines := make([]string, 0, len(words))
	current := ""
	for _, w := range words {
		if current != "" && lipgloss.Width(current)+1+lipgloss.Width(w) <= width {
			current += " " + w
			continue
		}
		if current != "" {
			lines = append(lines, current)
			current = ""
		}

		chunks := splitDisplayChunks(w, width)
		if len(chunks) == 0 {
			continue
		}
		if len(chunks) > 1 {
			lines = append(lines, chunks[:len(chunks)-1]...)
		}
		current = chunks[len(chunks)-1]
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

// splitDisplayChunks splits one long word without slicing through UTF-8 and
// measures terminal cells, which matters for CJK and emoji model output.
func splitDisplayChunks(word string, width int) []string {
	if word == "" || width <= 0 {
		return nil
	}
	var chunks []string
	var chunk strings.Builder
	used := 0
	for _, r := range word {
		rw := lipgloss.Width(string(r))
		if used > 0 && used+rw > width {
			chunks = append(chunks, chunk.String())
			chunk.Reset()
			used = 0
		}
		chunk.WriteRune(r)
		used += rw
		if used >= width {
			chunks = append(chunks, chunk.String())
			chunk.Reset()
			used = 0
		}
	}
	if chunk.Len() > 0 {
		chunks = append(chunks, chunk.String())
	}
	return chunks
}

package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// queuedFollowUp is deliberately limited to one item. A single visible queue
// slot lets the user keep working while a turn runs without creating a hidden
// backlog or losing the ability to revise the next instruction after failure.
type queuedFollowUp struct {
	Prompt string
}

// composerEditable reports whether the textarea currently owns user input.
// Ordinary turns keep it available so drafting does not stop while the model
// reasons or tools run. Owned filesystem/session/goal operations still lock it
// because their completion may replace the active conversation authority.
func (m *Model) composerEditable() bool {
	if m.initializing || m.shuttingDown || m.overlay != OverlayNone ||
		m.pendingApproval != nil || m.pendingPaste != nil || m.readScopePrompt != nil {
		return false
	}
	if m.state == StateIdle {
		return m.queuedFollowUp == nil && !m.composerIsBusy()
	}
	if m.queuedFollowUp != nil || m.goalTurnID != "" || m.goalOperation != "" {
		return false
	}
	return m.state == StateWaiting || m.state == StateStreaming
}

func (m *Model) queueComposerFollowUp() tea.Cmd {
	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" || m.queuedFollowUp != nil {
		return nil
	}
	m.queuedFollowUp = &queuedFollowUp{Prompt: prompt}
	m.input.Reset()
	m.input.SetHeight(1)
	m.inputLines = 1
	m.recalcViewportHeight()
	return nil
}

// renderQueuedFollowUp keeps the single pending instruction visible while the
// active turn settles. It is deliberately one physical row: queue state should
// never steal an unpredictable amount of transcript space.
func (m *Model) renderQueuedFollowUp() string {
	if m.queuedFollowUp == nil {
		return ""
	}
	prompt := strings.Join(strings.Fields(sanitizeTerminalMultiline(m.queuedFollowUp.Prompt)), " ")
	if prompt == "" {
		prompt = "follow-up"
	}

	prefix := "  " + m.styles.FocusIndicator.Render("queued") + m.styles.StatusText.Render(" › ")
	hints := []string{" · ↑ edit · esc clear", " · ↑ edit · esc", " · ↑/esc", ""}
	width := max(1, m.chatPaneWidth())
	hint := hints[len(hints)-1]
	for _, candidate := range hints {
		if width-lipgloss.Width(prefix)-lipgloss.Width(candidate) >= 8 {
			hint = candidate
			break
		}
	}
	available := max(1, width-lipgloss.Width(prefix)-lipgloss.Width(hint))
	return prefix + m.styles.StatusText.Render(truncateDisplay(prompt, available)) +
		m.styles.StatusText.Render(hint)
}

// editQueuedFollowUp returns the one queued instruction to the live composer.
// Up owns this action before ordinary history navigation while a turn runs.
func (m *Model) editQueuedFollowUp() bool {
	if m.queuedFollowUp == nil {
		return false
	}
	prompt := m.queuedFollowUp.Prompt
	if draft := strings.TrimSpace(m.input.Value()); draft != "" {
		prompt += "\n" + m.input.Value()
	}
	m.queuedFollowUp = nil
	m.clearCompletionSuppression()
	m.input.SetValue(prompt)
	m.input.CursorEnd()
	m.input.Focus()
	m.syncInputHeight()
	m.recalcViewportHeight()
	return true
}

// clearQueuedFollowUp releases the queue slot without cancelling the active
// run. Escape owns this action before the run-cancel fallback.
func (m *Model) clearQueuedFollowUp() bool {
	if m.queuedFollowUp == nil {
		return false
	}
	m.queuedFollowUp = nil
	m.input.Focus()
	m.syncInputHeight()
	m.recalcViewportHeight()
	return true
}

// restoreQueuedFollowUp returns authority to the user after a failed or
// cancelled turn. The queue slot is never silently retried after failure.
func (m *Model) restoreQueuedFollowUp() {
	if m.queuedFollowUp == nil {
		return
	}
	prompt := m.queuedFollowUp.Prompt
	m.queuedFollowUp = nil
	m.input.SetValue(prompt)
	m.input.CursorEnd()
	m.syncInputHeight()
}

// dispatchQueuedFollowUp starts the one queued instruction only after the
// preceding turn has completed and its state has been durably settled.
func (m *Model) dispatchQueuedFollowUp() tea.Cmd {
	if m.queuedFollowUp == nil || m.state != StateIdle {
		return nil
	}
	prompt := m.queuedFollowUp.Prompt
	m.queuedFollowUp = nil
	m.input.SetValue(prompt)
	m.input.CursorEnd()
	m.syncInputHeight()
	return m.submitInput()
}

package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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
		m.pendingApproval != nil || m.pendingPaste != nil {
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

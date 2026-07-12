package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// anchorActive is the canonical follow intent. userScrolledUp remains a mirror
// for compatibility with existing state inspection, and these helpers keep the
// two values from disagreeing.
func (m *Model) followPaused() bool {
	return !m.anchorActive
}

func (m *Model) pauseFollow() {
	m.anchorActive = false
	m.userScrolledUp = true
}

func (m *Model) markFollowingLatest() {
	m.anchorActive = true
	m.userScrolledUp = false
}

func (m *Model) resumeFollow() {
	m.markFollowingLatest()
	m.viewport.GotoBottom()
}

func (m *Model) gotoBottomIfFollowing() {
	if m.anchorActive {
		m.viewport.GotoBottom()
	}
}

func (m *Model) restoreFollowPosition(paused bool, yOffset int) {
	if !paused {
		m.resumeFollow()
		return
	}
	m.viewport.SetYOffset(yOffset)
	m.pauseFollow()
}

func (m *Model) transcriptScrollKey(msg tea.KeyPressMsg) bool {
	return key.Matches(msg, m.keys.PageUp) ||
		key.Matches(msg, m.keys.PageDown) ||
		key.Matches(msg, m.keys.HalfPageUp) ||
		key.Matches(msg, m.keys.HalfPageDn)
}

func (m *Model) canJumpToLatest() bool {
	if m.overlay != OverlayNone || m.pendingApproval != nil || m.pendingPaste != nil {
		return false
	}
	if m.state != StateIdle || m.composerIsBusy() {
		return true
	}
	// Preserve normal textarea End behavior for every nonempty draft, including
	// whitespace-only input.
	return m.input.Value() == ""
}

func (m *Model) renderFollowPausedStatus(width int) string {
	candidates := []string{
		"Follow paused · end latest",
		"Paused · end latest",
		"Paused · end",
	}
	available := max(1, width-2)
	chosen := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if lipgloss.Width(candidate) <= available {
			chosen = candidate
			break
		}
	}
	parts := strings.SplitN(chosen, "end", 2)
	line := "  " + m.styles.StatusText.Render(parts[0]) + m.styles.FocusIndicator.Render("end")
	if len(parts) == 2 {
		line += m.styles.StatusText.Render(parts[1])
	}
	return truncateDisplay(line, width)
}

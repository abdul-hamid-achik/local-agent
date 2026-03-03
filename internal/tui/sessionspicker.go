package tui

import (
	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
)

// sessionItem implements list.DefaultItem for the sessions picker.
type sessionItem struct {
	id        int
	title     string
	createdAt string
}

func (i sessionItem) Title() string {
	title := i.title
	if len(title) > 40 {
		title = title[:37] + "..."
	}
	return title
}

func (i sessionItem) Description() string {
	return i.createdAt
}

func (i sessionItem) FilterValue() string { return i.title }

// SessionsPickerState holds state for the sessions picker overlay.
type SessionsPickerState struct {
	List     list.Model
	Sessions []SessionListItem
}

// newSessionsPickerState creates a new SessionsPickerState with a bubbles list.
func newSessionsPickerState(sessions []SessionListItem, width int, isDark bool) *SessionsPickerState {
	items := make([]list.Item, len(sessions))
	for i, s := range sessions {
		items[i] = sessionItem{
			id:        s.ID,
			title:     s.Title,
			createdAt: s.CreatedAt,
		}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles = list.NewDefaultItemStyles(isDark)
	delegate.SetSpacing(0)

	maxW := 54
	if width-8 > maxW {
		maxW = width - 8
	}
	if maxW > 64 {
		maxW = 64
	}

	// Height: items fit, max 20 lines
	pickerH := len(sessions)*delegate.Height() + 4 // +4 for title + filter
	if pickerH > 20 {
		pickerH = 20
	}

	l := list.New(items, delegate, maxW-4, pickerH)
	l.Title = "Sessions"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(true)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()

	return &SessionsPickerState{
		List:     l,
		Sessions: sessions,
	}
}

// renderSessionsPicker renders the sessions picker overlay.
func (m *Model) renderSessionsPicker() string {
	ps := m.sessionsPickerState
	if ps == nil {
		return ""
	}

	maxW := 54
	if m.width-8 > maxW {
		maxW = m.width - 8
	}
	if maxW > 64 {
		maxW = 64
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.FocusIndicator.GetForeground()).
		Padding(0, 1).
		Width(maxW)

	return box.Render(ps.List.View())
}

// closeSessionsPicker dismisses the sessions picker overlay.
func (m *Model) closeSessionsPicker() {
	m.sessionsPickerState = nil
	m.overlay = OverlayNone
	m.input.Focus()
}

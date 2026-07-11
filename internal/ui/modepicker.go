package ui

import (
	"charm.land/bubbles/v2/list"
)

type modeItem struct {
	mode        Mode
	title       string
	description string
	current     bool
}

func (i modeItem) Title() string {
	if i.current {
		return i.title + "  ✓"
	}
	return i.title
}

func (i modeItem) Description() string { return i.description }
func (i modeItem) FilterValue() string { return i.title }

type ModePickerState struct {
	List list.Model
}

func newModePickerState(current Mode, terminalWidth, terminalHeight int, isDark bool) *ModePickerState {
	definitions := []modeItem{
		{mode: ModeAsk, title: "ASK", description: "Read, search, and answer without mutations"},
		{mode: ModePlan, title: "PLAN", description: "Explore and design without mutations"},
		{mode: ModeBuild, title: "BUILD", description: "Edit, execute, and use approved MCP tools"},
	}
	items := make([]list.Item, len(definitions))
	selected := 0
	for i := range definitions {
		definitions[i].current = definitions[i].mode == current
		if definitions[i].current {
			selected = i
		}
		items[i] = definitions[i]
	}

	delegate := newPickerDelegate(isDark, false)
	width := pickerListWidth(terminalWidth, 52)
	height := pickerListHeight(terminalHeight, len(items)*delegate.Height()+2, 4)
	l := list.New(items, delegate, width, height)
	configurePickerList(&l, isDark)
	l.Title = "Mode"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.Select(selected)
	return &ModePickerState{List: l}
}

func (m *Model) openModePicker() {
	m.modePickerState = newModePickerState(m.mode, m.width, m.height, m.isDark)
	m.overlay = OverlayModePicker
	m.input.Blur()
}

func (m *Model) closeModePicker() {
	m.modePickerState = nil
	m.closeOverlayToParent()
}

func (m *Model) renderModePicker() string {
	if m.modePickerState == nil {
		return ""
	}
	return m.renderPickerFrame(
		m.modePickerState.List.View(),
		52,
		m.pickerNavigationFooter(52, false),
	)
}

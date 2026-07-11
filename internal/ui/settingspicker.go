package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type settingsAction int

const (
	settingsModel settingsAction = iota
	settingsAgent
	settingsMode
	settingsSessions
	settingsCompact
	settingsRuntime
	settingsHelp
)

type settingsItem struct {
	action      settingsAction
	title       string
	value       string
	description string
}

func (i settingsItem) Title() string {
	if i.value == "" {
		return i.title
	}
	return i.title + " · " + i.value
}

func (i settingsItem) Description() string { return i.description }
func (i settingsItem) FilterValue() string { return i.title + " " + i.value }

// SettingsPickerState is the transient control center that replaces the
// persistent navigation chrome. It contains list/navigation and responsive
// presentation state only; the parent Model owns and applies every setting.
type SettingsPickerState struct {
	List       list.Model
	ItemHeight int
	Compact    bool
}

func newSettingsPickerState(items []settingsItem, terminalWidth, terminalHeight int, isDark bool) *SettingsPickerState {
	listItems := make([]list.Item, len(items))
	for i := range items {
		listItems[i] = items[i]
	}

	compact := compactSettingsRows(terminalWidth, terminalHeight)
	delegate := newSettingsDelegate(isDark, compact)
	itemHeight := delegate.Height()

	width := pickerListWidth(terminalWidth, 58)
	height := settingsListHeight(listItems, itemHeight, terminalHeight)

	l := list.New(listItems, delegate, width, height)
	configurePickerList(&l, isDark)
	l.Title = "Settings"
	setSettingsTitleDensity(&l, compact)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()

	return &SettingsPickerState{
		List:       l,
		ItemHeight: itemHeight,
		Compact:    compact,
	}
}

func compactSettingsRows(terminalWidth, terminalHeight int) bool {
	return terminalWidth <= 40 || terminalHeight <= 20
}

func newSettingsDelegate(isDark, compact bool) list.DefaultDelegate {
	return newPickerDelegate(isDark, compact)
}

func setSettingsTitleDensity(l *list.Model, compact bool) {
	bottom := 1
	if compact {
		// The default title bar reserves a blank row. At the supported 30x12
		// minimum that row is better spent keeping all seven settings visible.
		bottom = 0
	}
	l.Styles.TitleBar = l.Styles.TitleBar.Padding(0, 0, bottom, 0)
}

func settingsDetailWidth(terminalWidth int) int {
	// Match the list's item indentation so the selected detail aligns with the
	// row title while remaining inside the shared picker frame.
	return max(1, pickerListWidth(terminalWidth, 58)-2)
}

func settingsListHeight(items []list.Item, itemHeight, terminalHeight int) int {
	return pickerListHeight(terminalHeight, len(items)*itemHeight+2, 4)
}

func pickerContentWidth(terminalWidth, maximum int) int {
	// Narrow modals should use the available canvas instead of leaving the
	// desktop-sized gutters that make their content wrap prematurely. The
	// shared frame still retains one cell of breathing room on either side.
	width := terminalWidth - 4
	if width > maximum {
		width = maximum
	}
	if width < 20 {
		width = 20
	}
	return width
}

// pickerListWidth leaves room for the modal's horizontal padding. Bubbles
// delegates size and truncate their rows against this width, so keeping the
// list and box interiors aligned prevents narrow rows from wrapping into
// surprise extra lines.
func pickerListWidth(terminalWidth, maximum int) int {
	return max(1, pickerContentWidth(terminalWidth, maximum)-2)
}

// pickerListHeight keeps transient navigation inside the terminal. Chrome is
// the number of rows used by the surrounding border/footer outside the list.
func pickerListHeight(terminalHeight, desired, chrome int) int {
	height := min(desired, 20)
	available := terminalHeight - chrome
	if available < 4 {
		available = 4
	}
	return min(height, available)
}

// resizePickerOverlays preserves the active Bubbles list state while adapting
// its viewport to a terminal resize.
func (m *Model) resizePickerOverlays() {
	if state := m.completionState; state != nil {
		state.Filter.SetWidth(completionFilterInputWidth(m.width))
	}
	if state := m.settingsPickerState; state != nil {
		compact := compactSettingsRows(m.width, m.height)
		delegate := newSettingsDelegate(m.isDark, compact)
		state.List.SetDelegate(delegate)
		setSettingsTitleDensity(&state.List, compact)
		state.ItemHeight = delegate.Height()
		state.Compact = compact
		state.List.SetSize(
			pickerListWidth(m.width, 58),
			settingsListHeight(state.List.Items(), state.ItemHeight, m.height),
		)
	}
	if state := m.agentPickerState; state != nil {
		state.List.SetSize(
			pickerListWidth(m.width, 52),
			pickerListHeight(m.height, len(state.List.Items())*defaultPickerItemHeight+2, 4),
		)
	}
	if state := m.modePickerState; state != nil {
		state.List.SetSize(
			pickerListWidth(m.width, 52),
			pickerListHeight(m.height, len(state.List.Items())*defaultPickerItemHeight+2, 4),
		)
	}
	if state := m.modelPickerState; state != nil {
		state.List.SetSize(
			pickerListWidth(m.width, 50),
			pickerListHeight(m.height, len(state.Models)*defaultPickerItemHeight+2, 4),
		)
	}
	if state := m.sessionsPickerState; state != nil {
		if state.ready() {
			state.List.SetSize(
				pickerListWidth(m.width, 60),
				pickerListHeight(m.height, len(state.Sessions)*defaultPickerItemHeight+4, 4),
			)
		}
	}
	if m.runtimeStatusState != nil {
		m.refreshRuntimeStatus(true)
	}
}

func (m *Model) settingsItems() []settingsItem {
	modelValue := m.model
	if !m.modelPinned {
		modelValue = "Auto · " + modelValue
	} else if modelValue != "" {
		modelValue = "Pinned · " + modelValue
	}
	profile := m.agentProfile
	if profile == "" {
		profile = "Default"
	}
	compact := "Auto"
	if m.forceCompact {
		compact = "On"
	}
	runtime := fmt.Sprintf("%d servers · %d tools", m.serverCount, m.toolCount)
	if len(m.failedServers) > 0 {
		runtime += fmt.Sprintf(" · %d failed", len(m.failedServers))
	}
	if m.iceEnabled {
		runtime += " · ICE"
	}

	return []settingsItem{
		{action: settingsModel, title: "Model", value: modelValue, description: "Choose an installed local model"},
		{action: settingsAgent, title: "Agent profile", value: profile, description: "Change prompt, skills, model, and MCP scope"},
		{action: settingsMode, title: "Mode", value: m.modeConfigs[m.mode].Label, description: "ASK, PLAN, or BUILD authority"},
		{action: settingsSessions, title: "Sessions", value: "Resume", description: "Open a saved workspace session"},
		{action: settingsCompact, title: "Compact layout", value: compact, description: "Toggle the explicit compact transcript preference"},
		{action: settingsRuntime, title: "Runtime status", value: runtime, description: "Inspect local model, tools, servers, and failures"},
		{action: settingsHelp, title: "Help", value: "Shortcuts", description: "Keyboard reference and slash commands"},
	}
}

func (m *Model) openSettingsPicker() {
	m.overlayParent = OverlayNone
	m.settingsPickerState = newSettingsPickerState(m.settingsItems(), m.width, m.height, m.isDark)
	m.overlay = OverlaySettings
	m.input.Blur()
}

func (m *Model) refreshSettingsPicker() {
	if m.settingsPickerState == nil {
		return
	}
	selected := m.settingsPickerState.List.Index()
	m.settingsPickerState = newSettingsPickerState(m.settingsItems(), m.width, m.height, m.isDark)
	if selected >= 0 && selected < len(m.settingsPickerState.List.Items()) {
		m.settingsPickerState.List.Select(selected)
	}
}

func (m *Model) closeSettingsPicker() {
	m.settingsPickerState = nil
	m.dismissOverlay()
}

func (m *Model) activateSettings(action settingsAction) tea.Cmd {
	switch action {
	case settingsModel:
		m.openSettingsChild(m.openModelPicker)
	case settingsAgent:
		m.openSettingsChild(m.openAgentPicker)
	case settingsMode:
		m.openSettingsChild(m.openModePicker)
	case settingsSessions:
		m.openSettingsChild(m.openSessionsPicker)
		return m.requestSessions()
	case settingsCompact:
		m.forceCompact = !m.forceCompact
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.refreshSettingsPicker()
	case settingsRuntime:
		m.openSettingsChild(m.openRuntimeStatus)
	case settingsHelp:
		m.openSettingsChild(func() {
			m.overlay = OverlayHelp
			m.initHelpViewport()
			m.input.Blur()
		})
	}
	return nil
}

func (m *Model) renderSettingsPicker() string {
	if m.settingsPickerState == nil {
		return ""
	}
	content := m.settingsPickerState.List.View()
	if m.settingsPickerState.Compact && m.width >= 36 {
		if item, ok := m.settingsPickerState.List.SelectedItem().(settingsItem); ok && strings.TrimSpace(item.description) != "" {
			detail := truncateDisplay(strings.TrimSpace(item.description), settingsDetailWidth(m.width))
			content = strings.TrimRight(content, "\n") + "\n" +
				m.styles.OverlayDim.Render("  "+detail)
		}
	}
	return m.renderPickerFrame(content, 58, m.pickerNavigationFooter(58, false))
}

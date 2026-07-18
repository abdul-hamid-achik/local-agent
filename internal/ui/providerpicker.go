package ui

import (
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

type providerItem struct {
	descriptor llm.ProviderDescriptor
}

func (i providerItem) Title() string {
	title := sanitizeTerminalSingleLine(i.descriptor.Name)
	if title == "" {
		title = "Unnamed provider"
	}
	if i.descriptor.Active {
		title += "  ✓"
	}
	return title
}

func (i providerItem) Description() string {
	providerType := sanitizeTerminalSingleLine(i.descriptor.Type)
	model := sanitizeTerminalSingleLine(i.descriptor.Model)
	apiKeyEnv := sanitizeTerminalSingleLine(i.descriptor.APIKeyEnv)
	parts := make([]string, 0, 3)
	if providerType != "" {
		parts = append(parts, providerType)
	}
	if model != "" {
		parts = append(parts, model)
	}
	if i.descriptor.Remote {
		if i.descriptor.KeyPresent {
			parts = append(parts, "key ready")
		} else if apiKeyEnv != "" {
			parts = append(parts, "missing $"+apiKeyEnv)
		} else {
			parts = append(parts, "remote")
		}
	} else {
		parts = append(parts, "local")
	}
	return strings.Join(parts, " · ")
}

func (i providerItem) FilterValue() string {
	return sanitizeTerminalSingleLine(
		i.descriptor.Name + " " + i.descriptor.Type + " " + i.descriptor.Model + " " + i.descriptor.APIKeyEnv,
	)
}

// ProviderPickerState is the Bubbles list for /provider.
type ProviderPickerState struct {
	List        list.Model
	ItemHeight  int
	ItemSpacing int
}

func newProviderPickerState(catalog []llm.ProviderDescriptor, current string, terminalWidth, terminalHeight int, isDark bool, reducedMotion ...bool) *ProviderPickerState {
	items := make([]list.Item, 0, len(catalog))
	selected := 0
	for _, descriptor := range catalog {
		if strings.TrimSpace(descriptor.Name) == "" {
			continue
		}
		if descriptor.Name == current || descriptor.Active {
			selected = len(items)
		}
		items = append(items, providerItem{descriptor: descriptor})
	}
	if len(items) == 0 {
		items = append(items, providerItem{descriptor: llm.ProviderDescriptor{
			Name:   "ollama",
			Type:   "ollama",
			Active: true,
		}})
	}

	delegate := newPickerDelegate(isDark, false)
	width := pickerListWidth(terminalWidth, 56)
	height := pickerListHeight(terminalHeight, len(items)*delegate.Height()+2, 4)
	l := list.New(items, delegate, width, height)
	configurePickerList(&l, isDark, reducedMotion...)
	l.Title = "Provider"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(len(items) > 6)
	l.DisableQuitKeybindings()
	if selected >= len(items) {
		selected = 0
	}
	l.Select(selected)
	return &ProviderPickerState{
		List:        l,
		ItemHeight:  delegate.Height(),
		ItemSpacing: delegate.Spacing(),
	}
}

func (m *Model) openProviderPicker() {
	catalog := m.providerCatalog()
	m.providerPickerState = newProviderPickerState(
		catalog,
		m.activeProviderName(),
		m.width,
		m.height,
		m.isDark,
		m.reducedMotion,
	)
	m.overlay = OverlayProviderPicker
	m.input.Blur()
}

func (m *Model) closeProviderPicker() {
	m.providerPickerState = nil
	m.closeOverlayToParent()
}

func (m *Model) selectProviderProfile(name string) tea.Cmd {
	m.closeProviderPicker()
	return m.beginProviderSwitch(name, "")
}

func (m *Model) renderProviderPicker() string {
	if m.providerPickerState == nil {
		return ""
	}
	return m.renderPickerFrame(
		m.providerPickerState.List.View(),
		56,
		m.pickerNavigationFooter(56, m.providerPickerState.List.FilterState() != list.Unfiltered),
	)
}

func (m *Model) providerCatalog() []llm.ProviderDescriptor {
	if m.modelManager == nil {
		return []llm.ProviderDescriptor{{Name: "ollama", Type: "ollama", Active: true}}
	}
	return m.modelManager.ProviderCatalog()
}

// providerPickerPointerProjection mirrors the exact overlay composition used
// by View. Mouse coordinates are terminal cells, so every row owns its own
// horizontally centered half-open rectangle.
type providerPickerPointerProjection struct {
	lines    []string
	startY   int
	baseRows int
	width    int
}

func (m *Model) projectProviderPickerPointer() (providerPickerPointerProjection, bool) {
	if m.providerPickerState == nil || m.overlay != OverlayProviderPicker || m.width <= 0 {
		return providerPickerPointerProjection{}, false
	}
	overlay := m.renderProviderPicker()
	if overlay == "" {
		return providerPickerPointerProjection{}, false
	}
	frame := m.projectFrame()
	base := m.viewport.View() + "\n" + frame.Footer.Content
	return providerPickerPointerProjection{
		lines:    strings.Split(overlay, "\n"),
		startY:   centeredOverlayStartY(base, overlay),
		baseRows: len(strings.Split(base, "\n")),
		width:    m.width,
	}, true
}

func (p providerPickerPointerProjection) rowRect(localY int) CellRect {
	if localY < 0 || localY >= len(p.lines) || p.startY+localY >= p.baseRows {
		return CellRect{}
	}
	lineWidth := lipgloss.Width(p.lines[localY])
	if lineWidth <= 0 {
		return CellRect{}
	}
	startX := centeredOverlayLineX(p.width, p.lines[localY])
	return NewCellRect(startX, p.startY+localY, startX+lineWidth, p.startY+localY+1)
}

func (p providerPickerPointerProjection) contains(x, y int) bool {
	return p.rowRect(y-p.startY).Contains(x, y)
}

// providerPickerTitleRows matches Bubbles' title/filter section. Both the
// literal title and the active filter use the same TitleBar style, including
// its responsive bottom padding.
func providerPickerTitleRows(state *ProviderPickerState) int {
	if state == nil {
		return 0
	}
	title := state.List.Styles.Title.Render(state.List.Title)
	if state.List.FilterState() == list.Filtering {
		title = state.List.FilterInput.View()
	}
	return lipgloss.Height(state.List.Styles.TitleBar.Render(title))
}

// providerPickerItemAt returns an index in VisibleItems, never an index into
// the unfiltered backing catalog. That distinction keeps pointer selection
// correct while Bubbles has an applied filter.
func (m *Model) providerPickerItemAt(x, y int) (providerItem, int, bool) {
	state := m.providerPickerState
	projection, ok := m.projectProviderPickerPointer()
	if !ok || state.ItemHeight <= 0 {
		return providerItem{}, 0, false
	}

	localY := y - projection.startY
	itemStartY := 1 + providerPickerTitleRows(state) // one frame-border row
	itemY := localY - itemStartY
	stride := state.ItemHeight + max(0, state.ItemSpacing)
	if itemY < 0 || stride <= 0 || itemY%stride >= state.ItemHeight {
		return providerItem{}, 0, false
	}

	// The frame has a one-cell border plus one-cell horizontal padding.
	itemRow := Inset(projection.rowRect(localY), Insets{Left: pickerFrameCursorX, Right: pickerFrameCursorX})
	if !itemRow.Contains(x, y) {
		return providerItem{}, 0, false
	}

	rowOnPage := itemY / stride
	if rowOnPage < 0 || rowOnPage >= state.List.Paginator.PerPage {
		return providerItem{}, 0, false
	}
	index := state.List.Paginator.Page*state.List.Paginator.PerPage + rowOnPage
	visible := state.List.VisibleItems()
	if index < 0 || index >= len(visible) {
		return providerItem{}, 0, false
	}
	item, ok := visible[index].(providerItem)
	if !ok || strings.TrimSpace(item.descriptor.Name) == "" {
		return providerItem{}, 0, false
	}
	return item, index, true
}

func (m *Model) updateProviderPickerWheel(msg tea.MouseWheelMsg) {
	state := m.providerPickerState
	projection, ok := m.projectProviderPickerPointer()
	if !ok || !projection.contains(msg.X, msg.Y) || len(state.List.VisibleItems()) == 0 {
		return
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		state.List.CursorUp()
	case tea.MouseWheelDown:
		state.List.CursorDown()
	}
}

func (m *Model) selectProviderPickerPointer(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	item, index, ok := m.providerPickerItemAt(msg.X, msg.Y)
	if !ok {
		return nil
	}
	m.providerPickerState.List.Select(index)
	return m.selectProviderProfile(item.descriptor.Name)
}

package tui

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
	"github.com/abdulachik/local-agent/internal/config"
)

// modelItem implements list.DefaultItem for the model picker.
type modelItem struct {
	name       string
	size       string
	capability string
	isCurrent  bool
}

func (i modelItem) Title() string {
	title := i.name
	if i.isCurrent {
		title += " ●"
	}
	return title
}

func (i modelItem) Description() string {
	return fmt.Sprintf("%s · %s", i.size, i.capability)
}

func (i modelItem) FilterValue() string { return i.name }

// ModelPickerState holds state for the model picker overlay.
type ModelPickerState struct {
	List         list.Model
	Models       []config.Model
	CurrentModel string
}

// newModelPickerState creates a new ModelPickerState with a bubbles list.
func newModelPickerState(models []config.Model, currentModel string, isDark bool) *ModelPickerState {
	capLabels := map[config.ModelCapability]string{
		config.CapabilitySimple:   "Fast",
		config.CapabilityMedium:   "Balanced",
		config.CapabilityComplex:  "Capable",
		config.CapabilityAdvanced: "Advanced",
	}

	items := make([]list.Item, len(models))
	selectedIdx := 0
	for i, model := range models {
		if model.Name == currentModel {
			selectedIdx = i
		}
		items[i] = modelItem{
			name:       model.Name,
			size:       model.Size,
			capability: capLabels[model.Capability],
			isCurrent:  model.Name == currentModel,
		}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles = list.NewDefaultItemStyles(isDark)
	delegate.SetSpacing(0)

	const pickerW = 50
	// Height: items + title bar (2 lines)
	pickerH := len(models)*delegate.Height() + 2
	if pickerH > 20 {
		pickerH = 20
	}

	l := list.New(items, delegate, pickerW, pickerH)
	l.Title = "Select Model"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.Select(selectedIdx)

	return &ModelPickerState{
		List:         l,
		Models:       models,
		CurrentModel: currentModel,
	}
}

// renderModelPicker renders the model picker overlay.
func (m *Model) renderModelPicker() string {
	ps := m.modelPickerState
	if ps == nil {
		return ""
	}

	const maxW = 50
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.OverlayBorder).
		Padding(0, 1).
		Width(maxW)

	return box.Render(ps.List.View())
}

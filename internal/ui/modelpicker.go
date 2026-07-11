package ui

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

// modelItem implements list.DefaultItem for the model picker.
type modelItem struct {
	name       string
	size       string
	capability string
	isCurrent  bool
	unsafe     bool // too large for this machine (e.g. Gemma on 16GB)
}

func (i modelItem) Title() string {
	title := i.name
	if i.unsafe {
		return title + "  ⚠"
	}
	if i.isCurrent {
		title += " ●"
	}
	return title
}

func (i modelItem) Description() string {
	if i.unsafe {
		return fmt.Sprintf("%s · needs >16GB — unavailable", i.size)
	}
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
func newModelPickerState(models []config.Model, currentModel string, terminalWidth, terminalHeight int, isDark bool) *ModelPickerState {
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
			unsafe:     config.CheckModelMemorySafe(model.Name) != nil,
		}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles = list.NewDefaultItemStyles(isDark)
	delegate.SetSpacing(0)

	pickerW := pickerListWidth(terminalWidth, 50)
	// Height: items + title bar (2 lines)
	pickerH := len(models)*delegate.Height() + 2
	pickerH = pickerListHeight(terminalHeight, pickerH, 4)

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

	return m.renderPickerFrame(
		ps.List.View(),
		50,
		m.pickerNavigationFooter(50, false),
	)
}

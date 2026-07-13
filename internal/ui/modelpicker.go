package ui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

const (
	modelPickerMaximumWidth = 76
	modelPickerCompactWidth = 40
	modelPickerCompactRows  = 14
)

// OllamaModelSource is the execution boundary reported by Ollama. It is kept
// in the UI package so the picker does not depend on a particular wire client.
type OllamaModelSource uint8

const (
	OllamaModelLocal OllamaModelSource = iota
	OllamaModelCloud
	OllamaModelRemote
)

// OllamaModelDescriptor is the UI-facing inventory contract. The Ollama
// adapter owns discovery and enrichment; the picker only projects that state.
type OllamaModelDescriptor struct {
	Name             string
	DisplayName      string
	Source           OllamaModelSource
	SizeBytes        int64
	ParameterSize    string
	Quantization     string
	ContextLength    int
	EffectiveContext int
	AllocatedContext int
	SizeVRAM         int64
	Capabilities     []string
	Current          bool
	Running          bool
	Selectable       bool
	Fit              bool
	AutoRoutable     bool
	RequiresConsent  bool
	ConsentGranted   bool
	Reason           string
}

// OllamaModelInventoryMsg replaces the picker's cached inventory. RequestID
// lets the parent discard stale asynchronous refreshes.
type OllamaModelInventoryMsg struct {
	RequestID uint64
	Models    []OllamaModelDescriptor
	Err       error
}

// ollamaModelInventoryCommittedMsg returns after the manager has atomically
// installed one inventory snapshot outside Bubble Tea's Update goroutine.
type ollamaModelInventoryCommittedMsg struct {
	Inventory        OllamaModelInventoryMsg
	RecoveredModel   string
	RecoveryErr      error
	SelectionChanged bool
	PreviousModel    string
	SelectedModel    string
	SelectionReason  string
	SelectionErr     error
}

// OllamaModelDetailsRequestedMsg asks the parent to enrich/show one model.
// Cached descriptors may be rendered immediately while /api/show completes.
type OllamaModelDetailsRequestedMsg struct{ Model OllamaModelDescriptor }

type OllamaModelDetailsResultMsg struct {
	Model OllamaModelDescriptor
	Err   error
}

type modelItem struct {
	name       string
	descriptor OllamaModelDescriptor
	size       string // retained for focused compatibility tests
	capability string // retained for focused compatibility tests
	isCurrent  bool
	unsafe     bool
}

func (i modelItem) Title() string {
	name := i.descriptor.DisplayName
	if name == "" {
		name = i.name
	}
	parts := []string{modelGroupLabel(descriptorGroup(i.descriptor)), name}
	if state := modelRowState(i.descriptor, i.isCurrent, i.unsafe); state != "" {
		parts = append(parts, state)
	}
	return strings.Join(parts, " · ")
}

func (i modelItem) Description() string {
	if i.descriptor.Name == "" { // legacy config projection
		if i.unsafe {
			return fmt.Sprintf("%s · needs >16GB — unavailable", i.size)
		}
		return fmt.Sprintf("%s · %s", i.size, i.capability)
	}
	parts := make([]string, 0, 5)
	if size := humanModelBytes(i.descriptor.SizeBytes); size != "" {
		parts = append(parts, size)
	}
	if i.descriptor.ParameterSize != "" {
		parts = append(parts, i.descriptor.ParameterSize)
	}
	if i.descriptor.Quantization != "" {
		parts = append(parts, i.descriptor.Quantization)
	}
	if capabilities := compactCapabilities(i.descriptor.Capabilities); capabilities != "" {
		parts = append(parts, capabilities)
	}
	if i.descriptor.ContextLength > 0 {
		parts = append(parts, compactTokenCount(i.descriptor.ContextLength)+" max ctx")
	}
	if i.descriptor.EffectiveContext > 0 && i.descriptor.EffectiveContext != i.descriptor.ContextLength {
		parts = append(parts, compactTokenCount(i.descriptor.EffectiveContext)+" effective")
	}
	return strings.Join(parts, " · ")
}

func (i modelItem) FilterValue() string {
	return strings.Join([]string{
		i.name,
		i.descriptor.DisplayName,
		modelGroupLabel(descriptorGroup(i.descriptor)),
		modelRowState(i.descriptor, i.isCurrent, i.unsafe),
		strings.Join(i.descriptor.Capabilities, " "),
		i.descriptor.Reason,
	}, " ")
}

type ModelPickerState struct {
	List         list.Model
	Models       []config.Model // compatibility projection for existing callers
	Inventory    []OllamaModelDescriptor
	CurrentModel string
	RequestID    uint64
	Notice       string
	Compact      bool
	ItemHeight   int
}

func (s *ModelPickerState) SelectedDescriptor() (OllamaModelDescriptor, bool) {
	if s == nil {
		return OllamaModelDescriptor{}, false
	}
	item, ok := s.List.SelectedItem().(modelItem)
	if !ok {
		return OllamaModelDescriptor{}, false
	}
	return item.descriptor, item.descriptor.Name != ""
}

func (s *ModelPickerState) SelectedReason() string {
	descriptor, ok := s.SelectedDescriptor()
	if !ok {
		return ""
	}
	return modelDecisionReason(descriptor)
}

func newModelPickerState(models []config.Model, currentModel string, terminalWidth, terminalHeight int, isDark bool) *ModelPickerState {
	descriptors := make([]OllamaModelDescriptor, 0, len(models))
	for _, model := range models {
		descriptors = append(descriptors, OllamaModelDescriptor{
			Name: model.Name, DisplayName: model.Name, Source: OllamaModelLocal,
			ParameterSize: model.Size, ContextLength: model.ContextSize,
			Capabilities: []string{model.Capability.String()}, Current: model.Name == currentModel,
			Selectable: true, Fit: config.CheckModelMemorySafe(model.Name) == nil, AutoRoutable: true,
		})
	}
	state := newOllamaModelPickerState(descriptors, currentModel, terminalWidth, terminalHeight, isDark)
	state.Models = models
	return state
}

func newOllamaModelPickerState(models []OllamaModelDescriptor, currentModel string, terminalWidth, terminalHeight int, isDark bool) *ModelPickerState {
	models = append([]OllamaModelDescriptor(nil), models...)
	sort.SliceStable(models, func(i, j int) bool {
		gi, gj := descriptorGroup(models[i]), descriptorGroup(models[j])
		return gi < gj
	})

	items := make([]list.Item, 0, len(models))
	selectedIdx := 0
	for _, model := range models {
		if model.Name == currentModel || model.Current {
			selectedIdx = len(items)
		}
		items = append(items, modelItem{name: model.Name, descriptor: model})
	}

	compact := compactModelPicker(terminalWidth, terminalHeight)
	// Model identity and decision state belong to the navigable row; the
	// selected-detail strip below owns metadata. A single-line delegate avoids
	// repeating size/capability/context for every visible item and keeps the
	// inventory scannable at both regular and compact sizes.
	delegate := newPickerDelegate(isDark, true)
	pickerW := pickerListWidth(terminalWidth, modelPickerMaximumWidth)
	pickerH := modelPickerListHeight(terminalHeight, len(items), delegate.Height())
	l := list.New(items, delegate, pickerW, pickerH)
	configurePickerList(&l, isDark)
	setSettingsTitleDensity(&l, compact)
	l.Title = "Ollama models"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(!compact && len(items)*delegate.Height() > pickerH-2)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	if len(items) > 0 {
		l.Select(selectedIdx)
	}

	state := &ModelPickerState{
		List:         l,
		Inventory:    models,
		CurrentModel: currentModel,
		Compact:      compact,
		ItemHeight:   delegate.Height(),
	}
	if len(models) == 0 {
		state.Notice = "No models found · add a model or refresh Ollama"
	}
	return state
}

func descriptorGroup(model OllamaModelDescriptor) int {
	switch model.Source {
	case OllamaModelLocal:
		return 0
	case OllamaModelCloud:
		return 1
	default:
		return 2
	}
}

func modelGroupLabel(group int) string {
	switch group {
	case 0:
		return "LOCAL"
	case 1:
		return "CLOUD"
	case 2:
		return "REMOTE"
	default:
		return "UNAVAILABLE"
	}
}

func modelRowState(model OllamaModelDescriptor, legacyCurrent, legacyUnsafe bool) string {
	switch {
	case !model.Selectable || !model.Fit || legacyUnsafe:
		return "unavailable"
	case model.RequiresConsent && !model.ConsentGranted:
		return "review"
	case model.Current || legacyCurrent:
		return "current"
	case model.Running:
		return "running"
	default:
		return "available"
	}
}

func modelDecisionReason(model OllamaModelDescriptor) string {
	switch {
	case model.RequiresConsent && !model.ConsentGranted:
		if strings.TrimSpace(model.Reason) != "" {
			return strings.TrimSpace(model.Reason)
		}
		return "Ollama Cloud confirmation required before use"
	case !model.Selectable || !model.Fit:
		if strings.TrimSpace(model.Reason) != "" {
			return strings.TrimSpace(model.Reason)
		}
		return "model is unavailable under the current Ollama policy"
	default:
		return ""
	}
}

func compactModelPicker(terminalWidth, terminalHeight int) bool {
	return terminalWidth <= modelPickerCompactWidth || terminalHeight <= modelPickerCompactRows
}

func modelPickerListHeight(terminalHeight, itemCount, itemHeight int) int {
	// Reserve room for the frame, primary key hints, and the selected-model
	// detail. Compact terminals may need one extra wrapped decision line.
	if terminalHeight <= modelPickerCompactRows {
		// At the supported 30x12 minimum, keep one navigable result below the
		// title and give the selected-detail strip the remaining rows. Bubbles
		// scrolls the result window as selection moves.
		return 2
	}
	return pickerListHeight(terminalHeight, itemCount*itemHeight+2, 7)
}

func modelSelectionState(model OllamaModelDescriptor) string {
	parts := []string{modelGroupLabel(descriptorGroup(model))}
	switch {
	case !model.Selectable || !model.Fit:
		parts = append(parts, "unavailable")
	case model.RequiresConsent && !model.ConsentGranted:
		parts = append(parts, "review required")
	case model.Current:
		parts = append(parts, "current")
	case model.Running:
		parts = append(parts, "running")
	default:
		parts = append(parts, "available")
	}
	if model.Current && model.Running {
		parts = append(parts, "running")
	}
	if model.ConsentGranted {
		parts = append(parts, "conversation consent")
	}
	return strings.Join(parts, " · ")
}

func (m *Model) renderModelSelectionDetail(state *ModelPickerState, width int) string {
	if state == nil {
		return ""
	}
	descriptor, ok := state.SelectedDescriptor()
	if !ok {
		return m.styles.OverlayDim.Render(wrapText(state.Notice, width))
	}

	lines := []string{m.styles.OverlayAccent.Render(wrapText(modelSelectionState(descriptor), width))}
	if metadata := (modelItem{descriptor: descriptor}).Description(); metadata != "" {
		// Capability joins are compact inside list rows, but the selected-detail
		// strip should wrap at semantic boundaries instead of splitting a word.
		metadata = strings.ReplaceAll(metadata, "+", " · ")
		if state.Compact {
			metadata = strings.ReplaceAll(metadata, " max ctx", " ctx")
		}
		lines = append(lines, m.styles.OverlayDim.Render(wrapText(metadata, width)))
	}
	if reason := modelDecisionReason(descriptor); reason != "" {
		label := "Unavailable"
		if descriptor.RequiresConsent && !descriptor.ConsentGranted {
			label = "Review"
		}
		lines = append(lines, m.styles.OverlayDim.Render(wrapText(label+" · "+reason, width)))
	}
	if state.Notice != "" {
		lines = append(lines, m.styles.OverlayDim.Render(wrapText(state.Notice, width)))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderModelPicker() string {
	ps := m.modelPickerState
	if ps == nil {
		return ""
	}
	selectAction := "select"
	if descriptor, ok := ps.SelectedDescriptor(); ok && descriptor.RequiresConsent && !descriptor.ConsentGranted {
		selectAction = "review"
	}
	width := pickerListWidth(m.width, modelPickerMaximumWidth)
	footer := m.renderKeyHints(width,
		keyHint{Key: m.keys.Cancel.Help().Key, Action: m.overlayCloseLabel()},
		keyHint{Key: m.keys.CompleteSelect.Help().Key, Action: selectAction},
		keyHint{Key: "/", Action: "filter"},
		keyHint{Key: "d", Action: "details"}, keyHint{Key: "a", Action: "add"},
		keyHint{Key: "r", Action: "refresh"},
	)
	content := strings.TrimRight(ps.List.View(), "\n")
	if detail := m.renderModelSelectionDetail(ps, width); detail != "" {
		content += "\n" + detail
	}
	return m.renderPickerFrame(content, modelPickerMaximumWidth, footer)
}

func humanModelBytes(size int64) string {
	if size <= 0 {
		return ""
	}
	const gib = int64(1024 * 1024 * 1024)
	const mib = int64(1024 * 1024)
	if size >= gib {
		return fmt.Sprintf("%.1f GB", float64(size)/float64(gib))
	}
	return fmt.Sprintf("%d MB", max(int64(1), size/mib))
}

func compactTokenCount(value int) string {
	if value >= 1000 {
		return fmt.Sprintf("%dK", value/1000)
	}
	return fmt.Sprintf("%d", value)
}

func compactCapabilities(values []string) string {
	wanted := []string{"tools", "thinking", "vision", "completion", "embedding"}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "tool" || value == "tool_calling" {
			value = "tools"
		}
		seen[value] = true
	}
	result := make([]string, 0, len(seen))
	for _, value := range wanted {
		if seen[value] {
			result = append(result, value)
		}
	}
	return strings.Join(result, "+")
}

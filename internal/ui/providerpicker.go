package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

type providerItem struct {
	descriptor llm.ProviderDescriptor
}

func (i providerItem) Title() string {
	title := i.descriptor.Name
	if i.descriptor.Active {
		title += "  ✓"
	}
	return title
}

func (i providerItem) Description() string {
	parts := []string{i.descriptor.Type}
	if i.descriptor.Model != "" {
		parts = append(parts, i.descriptor.Model)
	}
	if i.descriptor.Remote {
		if i.descriptor.KeyPresent {
			parts = append(parts, "key ready")
		} else if i.descriptor.APIKeyEnv != "" {
			parts = append(parts, "missing $"+i.descriptor.APIKeyEnv)
		} else {
			parts = append(parts, "remote")
		}
	} else {
		parts = append(parts, "local")
	}
	return strings.Join(parts, " · ")
}

func (i providerItem) FilterValue() string {
	return i.descriptor.Name + " " + i.descriptor.Type + " " + i.descriptor.Model
}

// ProviderPickerState is the Bubbles list for /provider.
type ProviderPickerState struct {
	List list.Model
}

func newProviderPickerState(catalog []llm.ProviderDescriptor, current string, terminalWidth, terminalHeight int, isDark bool) *ProviderPickerState {
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
	configurePickerList(&l, isDark)
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
	return &ProviderPickerState{List: l}
}

func (m *Model) openProviderPicker() {
	catalog := m.providerCatalog()
	m.providerPickerState = newProviderPickerState(catalog, m.activeProviderName(), m.width, m.height, m.isDark)
	m.overlay = OverlayProviderPicker
	m.input.Blur()
}

func (m *Model) closeProviderPicker() {
	m.providerPickerState = nil
	m.closeOverlayToParent()
}

func (m *Model) selectProviderProfile(name string) {
	if err := m.switchProvider(name); err != nil {
		m.entries = append(m.entries, ChatEntry{Kind: "error", Content: err.Error()})
	} else {
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: fmt.Sprintf("Provider: %s · model %s", m.activeProviderName(), m.model),
		})
	}
	m.closeProviderPicker()
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
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

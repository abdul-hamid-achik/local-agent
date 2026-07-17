package ui

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

func (m *Model) activeProviderName() string {
	if m.modelManager == nil {
		return "ollama"
	}
	if name := strings.TrimSpace(m.modelManager.ActiveProviderName()); name != "" {
		return name
	}
	if m.modelManager.RemoteProvider() {
		return m.modelManager.RemoteProviderLabel()
	}
	return "ollama"
}

func (m *Model) providerNames() []string {
	if m.modelManager == nil {
		return []string{"ollama"}
	}
	names := m.modelManager.ProviderNames()
	if len(names) == 0 {
		return []string{"ollama"}
	}
	return names
}

// switchProvider activates a named provider profile and refreshes the UI model
// identity. API keys are resolved from the process environment at switch time.
func (m *Model) switchProvider(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is empty")
	}
	if m.modelManager == nil {
		return fmt.Errorf("model manager is unavailable")
	}
	if err := m.modelManager.SwitchProvider(name); err != nil {
		return err
	}
	m.model = m.modelManager.Model()
	m.modelPinned = true
	if m.modelManager.RemoteProvider() {
		m.modelList = []string{m.model}
		if policy := m.modelManager.ContextPolicy(m.model); policy.Effective > 0 {
			m.numCtx = policy.Effective
		}
	} else {
		// Restore Ollama context allocation when returning from a remote profile.
		if m.modelManager.NumCtx() > 0 {
			m.numCtx = m.modelManager.NumCtx()
		}
		// Keep modelList as previously discovered Ollama inventory when present.
		if len(m.modelList) == 0 && m.model != "" {
			m.modelList = []string{m.model}
		}
	}
	if m.completer != nil {
		m.completer.UpdateModels(m.modelList)
		m.completer.UpdateProviders(m.providerNames())
	}
	// Remote providers do not use local memory-admission for model names.
	if !m.modelManager.RemoteProvider() {
		if err := config.CheckModelMemorySafe(m.model); err != nil {
			// Soft warning only: SwitchProvider already selected the model.
			_ = err
		}
	}
	m.saveManualProviderPreference(name)
	return nil
}

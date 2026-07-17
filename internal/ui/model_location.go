package ui

import "strings"

// currentModelSurfaceLabel keeps a non-local execution boundary ahead of the
// model name so narrow surfaces cannot truncate away the fact that prompts
// leave the machine. Unknown inventory retains the legacy name-only display.
func (m *Model) currentModelSurfaceLabel(compact bool) string {
	if m == nil {
		return ""
	}
	name := sanitizeTerminalSingleLine(m.model)
	if m.modelManager != nil && m.modelManager.RemoteProvider() {
		provider := sanitizeTerminalSingleLine(m.activeProviderName())
		if provider == "" {
			provider = "remote"
		}
		boundary := strings.ToUpper(provider) + " · remote prompts"
		if compact || name == "" {
			return boundary
		}
		return strings.Join([]string{boundary, name}, " · ")
	}
	descriptor, ok := m.ollamaModelDescriptor(m.model)
	if !ok {
		return name
	}
	boundary := ""
	switch descriptor.Source {
	case OllamaModelCloud:
		boundary = "CLOUD · remote prompts"
	case OllamaModelRemote:
		boundary = "REMOTE · remote prompts"
	}
	if boundary == "" || compact || name == "" {
		if boundary != "" {
			return boundary
		}
		return name
	}
	return strings.Join([]string{boundary, name}, " · ")
}

func (m *Model) currentModelIsNonLocal() bool {
	if m == nil {
		return false
	}
	if m.modelManager != nil && m.modelManager.RemoteProvider() {
		return true
	}
	descriptor, ok := m.ollamaModelDescriptor(m.model)
	return ok && descriptor.Source != OllamaModelLocal
}

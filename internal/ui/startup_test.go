package ui

import (
	"strings"
	"testing"
)

func TestStartupProgressRendersInMainPane(t *testing.T) {
	m := newTestModel(t)
	m.initializing = true
	updated, _ := m.Update(StartupStatusMsg{
		ID: "ollama", Label: "Ollama", Status: "connecting", Detail: "line one\nline two",
	})
	m = updated.(*Model)
	updated, _ = m.Update(StartupStatusMsg{
		ID: "mcp:local", Label: "MCP", Status: "connected", Detail: `{"secret":"hidden"}`,
	})
	m = updated.(*Model)

	content := m.renderEntries()
	for _, want := range []string{"Starting local services", "Ollama", "line one line two", "MCP", "details available in logs"} {
		if !strings.Contains(content, want) {
			t.Errorf("startup pane missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(m.renderStatusLine(), "ready") {
		t.Fatalf("initializing status claimed readiness: %q", m.renderStatusLine())
	}
	assertRenderedLinesFit(t, content, m.chatPaneWidth())
}

func TestStartupStatusUpdatesByID(t *testing.T) {
	m := newTestModel(t)
	m.initializing = true
	updated, _ := m.Update(StartupStatusMsg{ID: "ollama", Label: "Ollama", Status: "connecting"})
	m = updated.(*Model)
	updated, _ = m.Update(StartupStatusMsg{ID: "ollama", Label: "Ollama", Status: "connected"})
	m = updated.(*Model)
	if len(m.startupItems) != 1 || m.startupItems[0].Status != "connected" {
		t.Fatalf("startup update was duplicated: %#v", m.startupItems)
	}
}

func TestSanitizeStartupDetail(t *testing.T) {
	if got := sanitizeStartupDetail(" a\n\tb   c "); got != "a b c" {
		t.Fatalf("sanitized detail = %q", got)
	}
	if got := sanitizeStartupDetail(`[1,2,3]`); got != "details available in logs" {
		t.Fatalf("JSON detail = %q", got)
	}
}

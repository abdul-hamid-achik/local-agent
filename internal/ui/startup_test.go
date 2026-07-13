package ui

import (
	"strings"
	"testing"
)

func TestStartupKeepsStableWelcomeShellAndOneFooterProgressLine(t *testing.T) {
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
	for _, want := range []string{"LOCAL AGENT", "Local-first", "enter send"} {
		if !strings.Contains(content, want) {
			t.Errorf("stable startup shell missing %q:\n%s", want, content)
		}
	}
	for _, hidden := range []string{"line one line two", "details available in logs"} {
		if strings.Contains(content, hidden) {
			t.Errorf("startup shell exposed per-service detail %q:\n%s", hidden, content)
		}
	}
	status := m.renderStatusLine()
	for _, want := range []string{"Starting", "local runtime", "1/2"} {
		if !strings.Contains(status, want) {
			t.Fatalf("startup footer omitted %q: %q", want, status)
		}
	}
	if strings.Contains(strings.ToLower(status), "ready") {
		t.Fatalf("initializing status claimed readiness: %q", status)
	}
	assertRenderedLinesFit(t, content, m.chatPaneWidth())

	m.initializing = false
	m.startupItems = nil
	if settled := m.renderEntries(); settled != content {
		t.Fatalf("welcome shell jumped when startup settled:\nduring:\n%s\nafter:\n%s", content, settled)
	}
}

func TestPreWindowStartupUsesProductShellInsteadOfDebugPlaceholder(t *testing.T) {
	m := newTestModel(t)
	m.ready = false
	view := m.View()
	for _, want := range []string{"LOCAL AGENT", "Starting"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("pre-window startup omitted %q: %q", want, view.Content)
		}
	}
	if strings.Contains(strings.ToLower(view.Content), "initializing") {
		t.Fatalf("pre-window startup leaked implementation placeholder: %q", view.Content)
	}
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

package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRuntimeStatusBoundsFailuresAndScrollsToFinalEntry(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m = updated.(*Model)
	for i := 1; i <= 8; i++ {
		m.failedServers = append(m.failedServers, FailedServer{
			Name:   fmt.Sprintf("server-%02d", i),
			Reason: "connection refused after a deliberately long local transport diagnostic",
		})
	}
	m.openSettingsPicker()
	m.openSettingsChild(m.openRuntimeStatus)

	top := m.renderRuntimeStatus()
	assertRenderedLinesFit(t, top, 40)
	assertRenderedHeightFits(t, top, 20)
	plainTop := ansi.Strip(top)
	if !strings.Contains(plainTop, "esc/q back") || !strings.Contains(plainTop, "↓") {
		t.Fatalf("runtime footer is not persistently actionable:\n%s", top)
	}

	updated, _ = m.Update(charKey('G'))
	m = updated.(*Model)
	bottom := m.renderRuntimeStatus()
	if !strings.Contains(bottom, "server-08") {
		t.Fatalf("scrolling did not reach final failure:\n%s", bottom)
	}
	assertRenderedLinesFit(t, bottom, 40)
	assertRenderedHeightFits(t, bottom, 20)
}

func TestRuntimeStatusPreservesScrollAcrossResize(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 10; i++ {
		m.failedServers = append(m.failedServers, FailedServer{Name: fmt.Sprintf("failed-%d", i), Reason: strings.Repeat("detail ", 8)})
	}
	m.openRuntimeStatus()
	m.runtimeStatusState.Viewport.GotoBottom()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m = updated.(*Model)
	if m.runtimeStatusState == nil || m.runtimeStatusState.Viewport.YOffset() == 0 {
		t.Fatal("runtime resize discarded the scroll position")
	}
	assertRenderedLinesFit(t, m.renderRuntimeStatus(), 60)
	assertRenderedHeightFits(t, m.renderRuntimeStatus(), 20)
}

func TestRuntimeStatusSeparatesLocalToolsFromMCPServers(t *testing.T) {
	m := newTestModel(t)
	home := t.TempDir()
	workspace := filepath.Join(home, "src", "project")
	t.Setenv("HOME", home)
	m.agent.SetWorkDir(workspace)
	m.toolCount = 19
	m.serverCount = 0

	content := m.buildRuntimeStatusContent(52)
	for _, want := range []string{"Workspace", "~/src/project", "Tools", fmt.Sprintf("%d available", m.agent.ToolCount()), "MCP", "0 servers configured"} {
		if !strings.Contains(content, want) {
			t.Fatalf("runtime status missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "across 0 MCP servers") {
		t.Fatalf("runtime status still conflates local tools with MCP servers:\n%s", content)
	}
}

func TestRuntimeStatusReconcilesReconnectedEcosystemServers(t *testing.T) {
	m := newTestModel(t)
	m.failedServers = []FailedServer{
		{Name: "mcphub", Reason: "stale failure"},
		{Name: "cortex", Reason: "connection refused"},
	}
	// Registry test seams are not needed here: project the same live/failure
	// inputs used by Runtime and assert the rendered vocabulary independently.
	connections := projectEcosystemConnections([]string{"mcphub", "monitor"}, m.failedServers)
	if got := summarizeConnectionHealth(connections); got != "degraded · 2 connected · 1 unavailable" {
		t.Fatalf("runtime projection = %q", got)
	}
	for _, connection := range connections {
		if connection.Label == "MCPHub" && connection.Health != capabilityConnected {
			t.Fatalf("stale MCPHub failure won over live connection: %#v", connections)
		}
	}
}

func TestCompactWorkspacePathPreservesRepositoryName(t *testing.T) {
	path := filepath.Join(string(filepath.Separator), "Users", "person", "projects", "local-agent")
	if got, want := compactWorkspacePath(path, 22), "…/projects/local-agent"; got != want {
		t.Fatalf("compact workspace = %q, want %q", got, want)
	}
	if got, want := compactWorkspacePath(path, 14), "…/local-agent"; got != want {
		t.Fatalf("narrow workspace = %q, want %q", got, want)
	}
}

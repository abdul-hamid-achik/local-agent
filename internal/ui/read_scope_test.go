package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
)

func TestScopeCommandUpdatesAgentReadRootsAsynchronously(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "mcphub")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatal(err)
	}
	external, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t)
	t.Cleanup(m.agent.Close)
	m.agent.SetWorkDir(workspace)

	parsed := m.cmdRegistry.Execute(m.buildCommandContext(), "scope", []string{"add-read", external})
	if parsed.Error != "" || parsed.Action != command.ActionAddReadRoot {
		t.Fatalf("parsed /scope add-read = %#v", parsed)
	}
	cmd := m.handleCommandAction(parsed)
	if cmd == nil || !m.readScopeOpRunning {
		t.Fatal("scope add did not start asynchronously")
	}
	receipt := awaitCommandMessage[ReadScopeResultMsg](t, commandMessages(cmd), 2*time.Second)
	updated, _ := m.Update(receipt)
	m = updated.(*Model)
	if m.readScopeOpRunning {
		t.Fatal("matching add receipt did not settle operation")
	}
	if roots := m.agent.ReadRoots(); len(roots) != 1 || roots[0] != external {
		t.Fatalf("agent roots = %#v", roots)
	}
	if roots := m.buildCommandContext().ReadRoots; len(roots) != 1 || roots[0] != external {
		t.Fatalf("command context roots = %#v", roots)
	}
	if transcript := entryText(m.entries); !strings.Contains(transcript, "Added process-local read-only root") || !strings.Contains(transcript, "Writes remain confined") {
		t.Fatalf("missing add receipt: %s", transcript)
	}

	remove := m.cmdRegistry.Execute(m.buildCommandContext(), "scope", []string{"remove-read", external})
	cmd = m.handleCommandAction(remove)
	receipt = awaitCommandMessage[ReadScopeResultMsg](t, commandMessages(cmd), 2*time.Second)
	updated, _ = m.Update(receipt)
	m = updated.(*Model)
	if roots := m.agent.ReadRoots(); len(roots) != 0 {
		t.Fatalf("roots after remove = %#v", roots)
	}
}

func TestScopeCommandSurfacesAuthorityErrorsAndIgnoresStaleReceipts(t *testing.T) {
	m := newTestModel(t)
	t.Cleanup(m.agent.Close)
	workspace := t.TempDir()
	m.agent.SetWorkDir(workspace)

	cmd := m.handleCommandAction(command.Result{Action: command.ActionAddReadRoot, Data: workspace})
	result := awaitCommandMessage[ReadScopeResultMsg](t, commandMessages(cmd), 2*time.Second)
	updated, _ := m.Update(result)
	m = updated.(*Model)
	if transcript := entryText(m.entries); !strings.Contains(transcript, "/scope add-read failed") || !strings.Contains(transcript, "overlaps") {
		t.Fatalf("authority error not surfaced: %s", transcript)
	}

	m.readScopeOpRunning = true
	m.readScopeOpToken = 9
	updated, _ = m.Update(ReadScopeResultMsg{Token: 8, Operation: "clear-read", Count: 99})
	m = updated.(*Model)
	if !m.readScopeOpRunning {
		t.Fatal("stale scope receipt settled the active operation")
	}
}

func TestGracefulShutdownWaitsForReadScopeReceipt(t *testing.T) {
	m := newTestModel(t)
	t.Cleanup(m.agent.Close)
	m.readScopeOpRunning = true
	m.readScopeOpToken = 4
	if cmd := m.beginShutdown(); cmd == nil {
		t.Fatal("shutdown did not wait for read-scope operation")
	}

	updated, cmd := m.Update(ReadScopeResultMsg{Token: 3, Operation: "clear-read"})
	m = updated.(*Model)
	if !m.readScopeOpRunning || cmd != nil {
		t.Fatal("stale read-scope receipt released shutdown")
	}
	updated, cmd = m.Update(ReadScopeResultMsg{Token: 4, Operation: "clear-read"})
	m = updated.(*Model)
	if m.readScopeOpRunning || cmd == nil {
		t.Fatal("matching read-scope receipt did not release shutdown")
	}
}

func entryText(entries []ChatEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.Content)
	}
	return strings.Join(parts, "\n")
}

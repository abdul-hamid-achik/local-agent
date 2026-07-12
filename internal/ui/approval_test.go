package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
	"github.com/charmbracelet/x/ansi"
)

func TestPendingApprovalEscapeDeniesAndCancels(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	m.pendingApproval = &ToolApprovalMsg{ToolName: "bash", Response: responses}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	updated, _ := m.Update(escKey())
	m = updated.(*Model)
	if m.pendingApproval != nil {
		t.Fatal("approval remained pending after Escape")
	}
	select {
	case response := <-responses:
		if response.Allowed {
			t.Fatal("Escape approved the tool")
		}
	case <-time.After(time.Second):
		t.Fatal("Escape did not answer the approval request")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Escape did not cancel the active turn")
	}
}

func TestPendingApprovalAcceptsUppercaseDecision(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	m.pendingApproval = &ToolApprovalMsg{ToolName: "write", Response: responses}

	updated, _ := m.Update(charKey('Y'))
	m = updated.(*Model)
	if m.pendingApproval != nil {
		t.Fatal("approval remained pending after Y")
	}
	if response := <-responses; !response.Allowed {
		t.Fatal("uppercase Y did not approve")
	}
}

func TestPendingApprovalAlwaysDecisionCarriesPersistentAuthority(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	m.pendingApproval = &ToolApprovalMsg{ToolName: "write", Response: responses}

	updated, _ := m.Update(charKey('a'))
	m = updated.(*Model)
	if m.pendingApproval != nil {
		t.Fatal("approval remained pending after a")
	}
	response := <-responses
	if !response.Allowed || !response.Always {
		t.Fatalf("always response = %#v, want allowed persistent authority", response)
	}
}

func TestApprovalDetailShowsArgumentsAndBoundsSize(t *testing.T) {
	detail, inspectable := approvalDetail("bash", map[string]any{"command": "rm -rf ./build && make test"})
	if !inspectable {
		t.Fatal("small exact approval detail was rejected")
	}
	if !strings.Contains(detail, "rm -rf ./build") {
		t.Fatalf("approval detail hid the command: %s", detail)
	}
	large, inspectable := approvalDetail("write", map[string]any{"content": strings.Repeat("x", 10_000)})
	if inspectable || !strings.Contains(large, "Refused") || !strings.Contains(large, "inspection limit") {
		t.Fatalf("uninspectable approval was not refused: %s", large)
	}
}

func TestUninspectableApprovalFailsClosed(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	updated, _ := m.Update(ToolApprovalMsg{
		ToolName: "bash",
		Args:     map[string]any{"command": strings.Repeat("x", maxApprovalDetail+1)},
		Response: responses,
	})
	m = updated.(*Model)
	if m.pendingApproval != nil {
		t.Fatal("uninspectable tool call remained approvable")
	}
	if response := <-responses; response.Allowed {
		t.Fatal("uninspectable tool call was approved")
	}
}

func TestApprovalDecisionKeepsIdentityAndEveryActionAtMinimumWidth(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = updated.(*Model)
	responses := make(chan permission.ApprovalResponse, 1)
	updated, _ = m.Update(ToolApprovalMsg{
		ToolName: "write_file",
		Args:     map[string]any{"path": "approval-probe.txt"},
		Response: responses,
	})
	m = updated.(*Model)

	prompt := ansi.Strip(m.renderStatusLine())
	for _, want := range []string{"write_file", "esc cancel", "y allow", "n deny", "a always"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("minimum approval prompt lost %q:\n%s", want, prompt)
		}
	}
	if got, want := m.footerHeight(), 2+lipgloss.Height(prompt); got != want {
		t.Fatalf("approval footer height = %d, want %d", got, want)
	}
	if got := lipgloss.Height(m.View().Content); got > m.height {
		t.Fatalf("minimum approval view height = %d, want <= %d", got, m.height)
	}
}

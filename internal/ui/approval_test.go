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

func openApprovalForTest(t *testing.T, m *Model, request ToolApprovalMsg) *Model {
	t.Helper()
	updated, _ := m.Update(request)
	m = updated.(*Model)
	if m.pendingApproval == nil || m.approvalState == nil || m.overlay != OverlayApproval {
		t.Fatalf("approval modal did not open: pending=%v state=%v overlay=%v", m.pendingApproval != nil, m.approvalState != nil, m.overlay)
	}
	return m
}

func TestPendingApprovalEscapeCancelsHostTurn(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	m = openApprovalForTest(t, m, ToolApprovalMsg{ToolName: "bash", Response: responses})
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	updated, _ := m.Update(escKey())
	m = updated.(*Model)
	if m.pendingApproval != nil || m.approvalState != nil || m.overlay != OverlayNone {
		t.Fatal("approval remained active after Escape")
	}
	select {
	case response := <-responses:
		if response.Normalize().Decision != permission.DecisionCancelled {
			t.Fatalf("Escape decision = %q, want cancelled", response.Normalize().Decision)
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

func TestPendingApprovalAcceptsUppercaseAllowOnce(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	m = openApprovalForTest(t, m, ToolApprovalMsg{ToolName: "write", Response: responses})

	updated, _ := m.Update(charKey('Y'))
	m = updated.(*Model)
	if m.pendingApproval != nil {
		t.Fatal("approval remained pending after Y")
	}
	if decision := (<-responses).Normalize().Decision; decision != permission.DecisionAllowOnce {
		t.Fatalf("Y decision = %q, want allow once", decision)
	}
}

func TestPendingApprovalSessionDecisionIsExplicit(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "write",
		Scope:    permission.ApprovalScope{Kind: permission.ScopeExactRequest},
		Response: responses,
	})

	updated, _ := m.Update(charKey('s'))
	m = updated.(*Model)
	if m.pendingApproval != nil {
		t.Fatal("approval remained pending after s")
	}
	if decision := (<-responses).Normalize().Decision; decision != permission.DecisionAllowSession {
		t.Fatalf("s decision = %q, want allow session", decision)
	}
}

func TestLargeWriteApprovalUsesViewportInsteadOfRefusal(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	content := strings.Repeat("x", 10_000)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName:        "write",
		Args:            map[string]any{"path": "AGENTS.md", "content": content},
		ArgumentsSHA256: strings.Repeat("a", 64),
		Preview: permission.ApprovalPreview{
			Kind:          permission.PreviewFileWrite,
			Path:          "AGENTS.md",
			ByteSize:      int64(len(content)),
			Diff:          "+" + content,
			DiffTruncated: true,
		},
		Scope:    permission.ApprovalScope{Kind: permission.ScopeExactRequest},
		Response: responses,
	})

	preview := ansi.Strip(m.buildApprovalContent(60))
	for _, want := range []string{"Write 9.8 KiB", "AGENTS.md", "Proposed change", "truncated"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("large approval preview missing %q:\n%s", want, preview)
		}
	}
	for _, entry := range m.entries {
		if strings.Contains(entry.Content, content[:256]) {
			t.Fatal("approval arguments leaked into the conversation transcript")
		}
	}

	updated, _ := m.Update(charKey('d'))
	m = updated.(*Model)
	arguments := ansi.Strip(m.buildApprovalContent(60))
	compactArguments := strings.ReplaceAll(arguments, "\n", "")
	if !strings.Contains(arguments, "Exact arguments") || !strings.Contains(compactArguments, content[:256]) {
		t.Fatalf("details did not expose exact arguments in the viewport:\n%s", arguments[:min(len(arguments), 500)])
	}
}

func TestUnencodableApprovalIsTypedHostRefusal(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	updated, _ := m.Update(ToolApprovalMsg{
		ToolName: "custom",
		Args:     map[string]any{"invalid": make(chan struct{})},
		Response: responses,
	})
	m = updated.(*Model)
	if m.pendingApproval != nil {
		t.Fatal("unencodable request remained approvable")
	}
	response := (<-responses).Normalize()
	if response.Decision != permission.DecisionHostRefuse || response.Code != "approval_preview_unavailable" {
		t.Fatalf("technical failure = %#v, want typed host refusal", response)
	}
}

func TestApprovalModalFitsMinimumTerminalAndKeepsDecisionKeys(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = updated.(*Model)
	responses := make(chan permission.ApprovalResponse, 1)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "write_file",
		Args:     map[string]any{"path": "approval-probe.txt"},
		Preview:  permission.ApprovalPreview{Kind: permission.PreviewFileWrite, Path: "approval-probe.txt"},
		Response: responses,
	})

	modal := ansi.Strip(m.renderApproval())
	for _, want := range []string{"write_file", "esc", "y", "s", "n"} {
		if !strings.Contains(modal, want) {
			t.Fatalf("minimum approval modal lost %q:\n%s", want, modal)
		}
	}
	if got := lipgloss.Height(m.View().Content); got > m.height {
		t.Fatalf("minimum approval view height = %d (modal %d), want <= %d:\n%s", got, lipgloss.Height(m.renderApproval()), m.height, modal)
	}
}

func TestApprovalDetailNoLongerRejectsLargeExactArguments(t *testing.T) {
	detail, inspectable := approvalDetail("write", map[string]any{"content": strings.Repeat("x", 10_000)})
	if !inspectable || !strings.Contains(detail, strings.Repeat("x", 256)) {
		t.Fatal("large exact arguments were rejected instead of delegated to the viewport")
	}
}

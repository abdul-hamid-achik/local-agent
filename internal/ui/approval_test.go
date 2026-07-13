package ui

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

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

func TestMCPApprovalShowsActionAndConsequence(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 16})
	m = updated.(*Model)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "cortex__start_task",
		Args:     map[string]any{"title": "Polish the TUI"},
		Preview: permission.ApprovalPreview{
			Kind:        permission.PreviewGeneric,
			ActionLabel: "Start Cortex task",
			Consequence: "Server metadata indicates this call may create or update durable state.",
		},
		Response: make(chan permission.ApprovalResponse, 1),
	})

	preview := ansi.Strip(m.buildApprovalContent(48))
	for _, want := range []string{"Action", "Start Cortex task", "Impact", "durable state", "exact arguments"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("MCP approval preview missing %q:\n%s", want, preview)
		}
	}
	assertRenderedLinesFit(t, m.renderApproval(), 48)
	if got := lipgloss.Height(m.View().Content); got > m.height {
		t.Fatalf("MCP approval height = %d, want <= %d", got, m.height)
	}
}

func TestApprovalSanitizesUntrustedTerminalMetadata(t *testing.T) {
	m := newTestModel(t)
	unsafeTool := "cortex__status\x1b]52;c;TOOL_SECRET\x07\n\u202espoof"
	unsafeAction := "\x1b]8;;https://ACTION_SECRET.invalid\x07Check status\x1b]8;;\x07\t\u2066"
	unsafeImpact := "Read metadata\x1b[2J\rIMPACT_SPOOF\u202e"
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: unsafeTool,
		Args:     map[string]any{},
		Preview: permission.ApprovalPreview{
			Kind:        permission.PreviewGeneric,
			ActionLabel: unsafeAction,
			Consequence: unsafeImpact,
		},
		Response: make(chan permission.ApprovalResponse, 1),
	})

	plain := ansi.Strip(m.renderApproval())
	for _, forbidden := range []string{"TOOL_SECRET", "ACTION_SECRET", "\u202e", "\u2066"} {
		if strings.Contains(plain, forbidden) {
			t.Fatalf("untrusted terminal metadata survived as %q:\n%s", forbidden, plain)
		}
	}
	for _, want := range []string{"Permission · cortex__status", "Check status", "Read metadata", "esc", "y", "s", "n"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("sanitized approval lost %q:\n%s", want, plain)
		}
	}

	for _, sanitized := range []string{
		sanitizeApprovalMetadata(unsafeTool),
		sanitizeApprovalMetadata(unsafeAction),
		sanitizeApprovalMetadata(unsafeImpact),
	} {
		for _, character := range sanitized {
			if unicode.IsControl(character) || isBidiControl(character) {
				t.Fatalf("unsafe rune %U survived approval metadata sanitization: %q", character, sanitized)
			}
		}
	}
}

func TestApprovalMetadataFallbackIsBoundedAndNoColorSafe(t *testing.T) {
	previous := noColor
	noColor = true
	t.Cleanup(func() { noColor = previous })

	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = updated.(*Model)
	toolName := "cortex__" + strings.Repeat("界", 200)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: toolName,
		Args:     map[string]any{},
		Preview: permission.ApprovalPreview{
			Kind:        permission.PreviewGeneric,
			Consequence: strings.Repeat("untrusted effect metadata ", 100),
		},
		Response: make(chan permission.ApprovalResponse, 1),
	})

	rendered := m.renderApproval()
	content := strings.Join(strings.Fields(ansi.Strip(m.buildApprovalContent(30))), " ")
	if !strings.Contains(content, "Run cortex__") || strings.Contains(content, toolName) {
		t.Fatalf("raw tool fallback was not bounded:\n%s", content)
	}
	if hasANSIColor(rendered) {
		t.Fatalf("NO_COLOR approval emitted ANSI color sequences: %q", rendered)
	}
	assertRenderedLinesFit(t, rendered, 30)
	assertRenderedHeightFits(t, m.View().Content, 12)

	bounded := boundedApprovalMetadata(strings.Repeat("界", 200), approvalMaximumActionBytes)
	if len(bounded) > approvalMaximumActionBytes || !utf8.ValidString(bounded) || !strings.HasSuffix(bounded, "...") {
		t.Fatalf("bounded metadata bytes=%d valid=%v value=%q", len(bounded), utf8.ValidString(bounded), bounded)
	}
}

func TestGenericApprovalFallsBackToRawToolName(t *testing.T) {
	m := newTestModel(t)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "cortex__status", Args: map[string]any{},
		Preview:  permission.ApprovalPreview{Kind: permission.PreviewGeneric},
		Response: make(chan permission.ApprovalResponse, 1),
	})
	if preview := ansi.Strip(m.buildApprovalContent(60)); !strings.Contains(preview, "Run cortex__status") {
		t.Fatalf("generic action did not fall back to raw tool name:\n%s", preview)
	}
}

func TestApprovalDetailNoLongerRejectsLargeExactArguments(t *testing.T) {
	detail, inspectable := approvalDetail("write", map[string]any{"content": strings.Repeat("x", 10_000)})
	if !inspectable || !strings.Contains(detail, strings.Repeat("x", 256)) {
		t.Fatal("large exact arguments were rejected instead of delegated to the viewport")
	}
}

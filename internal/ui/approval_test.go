package ui

import (
	"context"
	"image/color"
	"path/filepath"
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
	if m.pendingApproval == nil || m.approvalState == nil || m.overlay != OverlayNone {
		t.Fatalf("inline approval did not open: pending=%v state=%v overlay=%v", m.pendingApproval != nil, m.approvalState != nil, m.overlay)
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

func TestUndersizedTerminalPausesApprovalAndDraftUntilResize(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("preserved draft")
	responses := make(chan permission.ApprovalResponse, 1)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "write_file",
		Args:     map[string]any{"path": "review-before-allow.txt"},
		Response: responses,
	})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: minTerminalWidth - 1, Height: 20})
	m = updated.(*Model)
	if visible := ansi.Strip(m.View().Content); !strings.Contains(visible, "Input paused") || strings.Contains(visible, "Permission") {
		t.Fatalf("undersized frame exposed the hidden decision instead of pause state:\n%s", visible)
	}

	for _, input := range []tea.Msg{charKey('y'), charKey('n'), enterKey(), escKey(), tea.PasteMsg{Content: "hidden paste"}} {
		updated, cmd := m.Update(input)
		m = updated.(*Model)
		if cmd != nil || m.pendingApproval == nil || m.approvalState == nil {
			t.Fatalf("hidden input resolved approval: input=%T cmd=%v pending=%v", input, cmd != nil, m.pendingApproval != nil)
		}
		if m.input.Value() != "preserved draft" {
			t.Fatalf("hidden input changed draft to %q", m.input.Value())
		}
		select {
		case response := <-responses:
			t.Fatalf("hidden input emitted decision %q", response.Normalize().Decision)
		default:
		}
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: minTerminalWidth, Height: minTerminalHeight})
	m = updated.(*Model)
	visible := ansi.Strip(m.View().Content)
	if !strings.Contains(visible, "Permission") || !strings.Contains(visible, "write_file") {
		t.Fatalf("exact approval did not reappear after resize:\n%s", visible)
	}
	updated, _ = m.Update(charKey('y'))
	m = updated.(*Model)
	if m.pendingApproval != nil {
		t.Fatal("visible approval remained pending after explicit allow")
	}
	if decision := (<-responses).Normalize().Decision; decision != permission.DecisionAllowOnce {
		t.Fatalf("visible approval decision = %q, want allow once", decision)
	}
}

func TestUndersizedTerminalStillAllowsGracefulQuitFromApproval(t *testing.T) {
	m := newTestModel(t)
	responses := make(chan permission.ApprovalResponse, 1)
	m = openApprovalForTest(t, m, ToolApprovalMsg{ToolName: "bash", Response: responses})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	m = updated.(*Model)

	updated, cmd := m.Update(ctrlKey('c'))
	m = updated.(*Model)
	if cmd == nil || !m.shuttingDown || m.pendingApproval != nil {
		t.Fatalf("Ctrl+C did not retain graceful shutdown: cmd=%v shuttingDown=%v pending=%v", cmd != nil, m.shuttingDown, m.pendingApproval != nil)
	}
	if decision := (<-responses).Normalize().Decision; decision != permission.DecisionCancelled {
		t.Fatalf("shutdown approval decision = %q, want cancelled", decision)
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

func TestApprovalChoiceSelectionDefaultsToDenyAndSupportsArrowsAndVim(t *testing.T) {
	tests := []struct {
		name         string
		move         []tea.KeyPressMsg
		wantIndex    int
		wantDecision permission.ApprovalDecision
	}{
		{name: "safe default", wantIndex: 0, wantDecision: permission.DecisionUserDeny},
		{name: "down to once", move: []tea.KeyPressMsg{downKey()}, wantIndex: 1, wantDecision: permission.DecisionAllowOnce},
		{name: "vim to session", move: []tea.KeyPressMsg{charKey('j'), charKey('j')}, wantIndex: 2, wantDecision: permission.DecisionAllowSession},
		{name: "up wraps to session", move: []tea.KeyPressMsg{upKey()}, wantIndex: 2, wantDecision: permission.DecisionAllowSession},
		{name: "vim up wraps to session", move: []tea.KeyPressMsg{charKey('k')}, wantIndex: 2, wantDecision: permission.DecisionAllowSession},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			responses := make(chan permission.ApprovalResponse, 1)
			m = openApprovalForTest(t, m, ToolApprovalMsg{
				ToolName: "write_file",
				Args:     map[string]any{"path": "selection.txt"},
				Response: responses,
			})

			for _, move := range tt.move {
				updated, _ := m.Update(move)
				m = updated.(*Model)
			}
			if got := m.approvalState.ChoiceIndex; got != tt.wantIndex {
				t.Fatalf("choice index = %d, want %d", got, tt.wantIndex)
			}
			selected := ansi.Strip(m.renderApprovalChoices(m.approvalContentWidth()))
			if !strings.Contains(selected, "› "+approvalChoices[tt.wantIndex].Key) {
				t.Fatalf("selected choice has no visible focus indicator:\n%s", selected)
			}

			updated, _ := m.Update(enterKey())
			m = updated.(*Model)
			if m.pendingApproval != nil {
				t.Fatal("Enter did not resolve the selected permission choice")
			}
			if got := (<-responses).Normalize().Decision; got != tt.wantDecision {
				t.Fatalf("Enter decision = %q, want %q", got, tt.wantDecision)
			}
		})
	}
}

func TestApprovalPreservesAndAcknowledgesDraftAndQueueAcrossResolutions(t *testing.T) {
	tests := []struct {
		name         string
		resolve      tea.KeyPressMsg
		wantDecision permission.ApprovalDecision
		wantCancel   bool
	}{
		{name: "allow once", resolve: charKey('y'), wantDecision: permission.DecisionAllowOnce},
		{name: "deny", resolve: charKey('n'), wantDecision: permission.DecisionUserDeny},
		{name: "escape", resolve: escKey(), wantDecision: permission.DecisionCancelled, wantCancel: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
			m = updated.(*Model)
			m.state = StateStreaming
			m.input.SetValue("unfinished composer draft")
			m.input.CursorEnd()
			m.queuedFollowUp = &queuedFollowUp{Prompt: "queued instruction remains intact"}
			ctx, cancel := context.WithCancel(context.Background())
			m.cancel = cancel
			responses := make(chan permission.ApprovalResponse, 1)

			m = openApprovalForTest(t, m, ToolApprovalMsg{
				ToolName: "bash",
				Args:     map[string]any{"command": "go test ./internal/ui"},
				Response: responses,
			})
			if m.input.Focused() {
				t.Fatal("composer retained focus while permission owned the footer")
			}
			plain := ansi.Strip(m.renderApproval())
			for _, want := range []string{"Draft saved", "queued follow-up saved"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("approval did not acknowledge %q:\n%s", want, plain)
				}
			}

			updated, _ = m.Update(tt.resolve)
			m = updated.(*Model)
			if got := m.input.Value(); got != "unfinished composer draft" {
				t.Fatalf("resolution changed composer draft to %q", got)
			}
			if m.queuedFollowUp == nil || m.queuedFollowUp.Prompt != "queued instruction remains intact" {
				t.Fatalf("resolution changed queued follow-up: %#v", m.queuedFollowUp)
			}
			if m.input.Focused() {
				t.Fatal("queued follow-up did not retain footer authority after approval")
			}
			if got := (<-responses).Normalize().Decision; got != tt.wantDecision {
				t.Fatalf("decision = %q, want %q", got, tt.wantDecision)
			}
			select {
			case <-ctx.Done():
				if !tt.wantCancel {
					t.Fatal("allow/deny cancelled the active run")
				}
			default:
				if tt.wantCancel {
					t.Fatal("Escape did not cancel the active run")
				}
			}
		})
	}
}

func TestApprovalRestoresComposerFocusFromCurrentAuthority(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Model)
		wantFocused bool
	}{
		{name: "ordinary running draft", setup: func(m *Model) {
			m.state = StateStreaming
			m.input.SetValue("continue drafting")
		}, wantFocused: true},
		{name: "queued follow-up owns footer", setup: func(m *Model) {
			m.state = StateStreaming
			m.queuedFollowUp = &queuedFollowUp{Prompt: "already queued"}
		}},
		{name: "goal turn owns authority", setup: func(m *Model) {
			m.state = StateStreaming
			m.goalTurnID = "goal-turn"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			tt.setup(m)
			responses := make(chan permission.ApprovalResponse, 1)
			m = openApprovalForTest(t, m, ToolApprovalMsg{
				ToolName: "read_file",
				Args:     map[string]any{"path": "focus.txt"},
				Response: responses,
			})
			updated, _ := m.Update(charKey('y'))
			m = updated.(*Model)
			<-responses
			if got := m.input.Focused(); got != tt.wantFocused {
				t.Fatalf("composer focused = %v, want %v", got, tt.wantFocused)
			}
		})
	}
}

func TestApprovalSurfaceNamesActiveAuthority(t *testing.T) {
	m := newTestModel(t)
	m.mode = ModeNormal
	m.width = 120
	m.height = 36
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "mcphub__cortex__cortex_investigate",
		Args:     map[string]any{"taskId": "task-1", "question": "inspect"},
		Response: make(chan permission.ApprovalResponse, 1),
	})

	view := ansi.Strip(m.renderApproval())
	if !strings.Contains(view, "Permission · mcphub__cortex__cortex_investigate · NORMAL") {
		t.Fatalf("approval omitted authority mode:\n%s", view)
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

func TestInlineApprovalFitsMinimumTerminalAndKeepsDecisionKeys(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = updated.(*Model)
	m.state = StateStreaming
	m.input.SetValue("draft")
	m.queuedFollowUp = &queuedFollowUp{Prompt: "queued follow-up"}
	responses := make(chan permission.ApprovalResponse, 1)
	target := filepath.Join(string(filepath.Separator), "Users", "person", "projects", "local-agent", "approval-probe.txt")
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "write_file",
		Args:     map[string]any{"path": target},
		Preview:  permission.ApprovalPreview{Kind: permission.PreviewFileWrite, Path: target},
		Response: responses,
	})

	inline := ansi.Strip(m.renderApproval())
	for _, want := range []string{"write_file", "Draft + queue saved", "approval-probe.txt", "esc", "enter", "n deny", "y once", "s identical/session", "pgdn more", "d arguments"} {
		if !strings.Contains(inline, want) {
			t.Fatalf("minimum inline approval lost %q:\n%s", want, inline)
		}
	}
	if got := lipgloss.Height(m.View().Content); got > m.height {
		t.Fatalf("minimum approval view height = %d (surface %d), want <= %d:\n%s", got, lipgloss.Height(m.renderApproval()), m.height, inline)
	}
}

func TestApprovalDiffPrioritizesMaterialAdditionOverEmptyPreimage(t *testing.T) {
	m := newTestModel(t)
	lines := m.renderApprovalDiff("-\n+must not be written", 60)
	if len(lines) != 1 || !strings.Contains(ansi.Strip(lines[0]), "+must not be written") {
		t.Fatalf("material diff was not first: %#v", lines)
	}
}

func TestApprovalPreservesPausedTranscriptAcrossOpenDetailsResizeAndResolve(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	setScrollableTranscript(m)
	m.viewport.SetYOffset(6)
	m.pauseFollow()
	wantOffset := m.viewport.YOffset()
	responses := make(chan permission.ApprovalResponse, 1)
	m.state = StateStreaming
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "write_file",
		Args: map[string]any{
			"path":    "scroll.txt",
			"content": strings.Repeat("a long exact argument line that must remain inspectable ", 80),
		},
		Preview: permission.ApprovalPreview{
			Kind:        permission.PreviewFileWrite,
			Path:        "scroll.txt",
			Consequence: strings.Repeat("bounded consequence ", 30),
		},
		Response: responses,
	})
	assertPausedApprovalTranscript := func(stage string) {
		t.Helper()
		if got := m.viewport.YOffset(); got != wantOffset {
			t.Fatalf("%s moved transcript from %d to %d", stage, wantOffset, got)
		}
		if !m.followPaused() || !m.userScrolledUp {
			t.Fatalf("%s changed follow intent: active=%v scrolled=%v", stage, m.anchorActive, m.userScrolledUp)
		}
	}
	assertPausedApprovalTranscript("open")

	updated, _ = m.Update(charKey('d'))
	m = updated.(*Model)
	assertPausedApprovalTranscript("details")
	if !m.approvalState.ShowArguments {
		t.Fatal("details key did not expose exact arguments")
	}
	approvalBeforeWheel := m.approvalState.Viewport.YOffset()
	updated, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = updated.(*Model)
	assertPausedApprovalTranscript("approval wheel")
	if got := m.approvalState.Viewport.YOffset(); got <= approvalBeforeWheel {
		t.Fatalf("approval wheel offset = %d, want > %d", got, approvalBeforeWheel)
	}
	approvalBeforeResize := m.approvalState.Viewport.YOffset()

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 22})
	m = updated.(*Model)
	assertPausedApprovalTranscript("resize")
	if got := m.approvalState.Viewport.YOffset(); got != approvalBeforeResize {
		t.Fatalf("resize moved approval details from %d to %d", approvalBeforeResize, got)
	}

	updated, _ = m.Update(charKey('n'))
	m = updated.(*Model)
	assertPausedApprovalTranscript("resolve")
	if got := (<-responses).Normalize().Decision; got != permission.DecisionUserDeny {
		t.Fatalf("resolution = %q, want deny", got)
	}
}

func TestApprovalThemeChangeRebuildsCachedBodyAndPreservesOffsets(t *testing.T) {
	previous := noColor
	noColor = false
	t.Cleanup(func() { noColor = previous })

	m := newTestModel(t)
	setScrollableTranscript(m)
	m.viewport.SetYOffset(6)
	m.pauseFollow()
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "write_file",
		Preview: permission.ApprovalPreview{
			Kind:        permission.PreviewFileWrite,
			Path:        "theme.txt",
			Consequence: strings.Repeat("inspect the proposed local change ", 30),
		},
		Response: make(chan permission.ApprovalResponse, 1),
	})
	m.approvalState.Viewport.SetYOffset(2)
	wantTranscriptOffset := m.viewport.YOffset()
	wantApprovalOffset := m.approvalState.Viewport.YOffset()
	labelWidth := min(10, max(6, m.approvalContentWidth()/5))
	oldStyledLabel := m.styles.OverlayAccent.Width(labelWidth).Render("Impact")

	updated, _ := m.Update(tea.BackgroundColorMsg{Color: color.White})
	m = updated.(*Model)
	newStyledLabel := m.styles.OverlayAccent.Width(labelWidth).Render("Impact")
	cachedBody := m.approvalState.Viewport.View()
	if oldStyledLabel == newStyledLabel {
		t.Fatal("test palettes rendered the same approval label")
	}
	if !strings.Contains(cachedBody, newStyledLabel) || strings.Contains(cachedBody, oldStyledLabel) {
		t.Fatalf("approval body retained stale theme styles:\n%s", cachedBody)
	}
	if got := m.viewport.YOffset(); got != wantTranscriptOffset || !m.followPaused() {
		t.Fatalf("theme change moved paused transcript: offset=%d want=%d paused=%v", got, wantTranscriptOffset, m.followPaused())
	}
	if got := m.approvalState.Viewport.YOffset(); got != wantApprovalOffset {
		t.Fatalf("theme change moved approval body: offset=%d want=%d", got, wantApprovalOffset)
	}
}

func TestApprovalUsesInlineComposerWidthAndShowsDiff(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 32})
	m = updated.(*Model)
	m.entries = append(m.entries, ChatEntry{Kind: "user", Content: "conversation remains visible"})
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "edit",
		Args:     map[string]any{"path": "internal/ui/view.go"},
		Preview: permission.ApprovalPreview{
			Kind: permission.PreviewFilePatch,
			Path: "internal/ui/view.go",
			Diff: "@@ -1 +1 @@\n-old composer\n+inline composer",
		},
		Response: make(chan permission.ApprovalResponse, 1),
	})

	if m.overlay != OverlayNone {
		t.Fatalf("approval covered the transcript with overlay %v", m.overlay)
	}
	if got := m.approvalState.Viewport.Width(); got <= 86 || got != m.approvalContentWidth() {
		t.Fatalf("inline approval width = %d, want full composer width %d", got, m.approvalContentWidth())
	}
	plain := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"conversation remains visible",
		"Permission · edit",
		"internal/ui/view.go",
		"-old composer",
		"+inline composer",
		"y once",
		"n deny",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("inline approval view missing %q:\n%s", want, plain)
		}
	}
	if strings.Index(plain, "conversation remains visible") > strings.Index(plain, "Permission · edit") {
		t.Fatalf("approval did not remain below the transcript:\n%s", plain)
	}
	assertRenderedLinesFit(t, m.View().Content, 140)
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

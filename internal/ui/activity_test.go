package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
	"github.com/charmbracelet/x/ansi"
)

func TestMinimumTerminalWorkingStatesFit(t *testing.T) {
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		set  func(*Model)
		want string
	}{
		{name: "idle", set: func(*Model) {}, want: "ctrl+p settings"},
		{name: "failed runtime", set: func(m *Model) {
			m.failedServers = []FailedServer{{Name: "tools", Reason: "offline"}}
		}, want: "failed"},
		{name: "waiting", set: func(m *Model) {
			m.state = StateWaiting
			m.turnStartedAt = base.Add(-1500 * time.Millisecond)
		}, want: "Thinking"},
		{name: "reasoning", set: func(m *Model) {
			m.state = StateStreaming
			m.turnStartedAt = base.Add(-2 * time.Second)
			m.thinkBuf.WriteString("checking")
		}, want: "Reasoning"},
		{name: "streaming", set: func(m *Model) {
			m.state = StateStreaming
			m.turnStartedAt = base.Add(-3 * time.Second)
			m.streamBuf.WriteString("partial")
		}, want: "Responding"},
		{name: "session loading", set: func(m *Model) { m.sessionLoading = true }, want: "Restoring session"},
		{name: "file operation", set: func(m *Model) { m.fileLoading = true }, want: "Reading local file"},
		{name: "commit", set: func(m *Model) { m.commitRunning = true }, want: "Generating commit"},
		{name: "export", set: func(m *Model) { m.exportRunning = true }, want: "Publishing export"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			m.model = "qwen3.5:2b"
			m.toolCount = 19
			m.reducedMotion = true
			m.now = func() time.Time { return base }
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
			m = updated.(*Model)
			tt.set(m)
			m.recalcViewportHeight()
			m.viewport.SetContent(m.renderEntries())

			view := m.View().Content
			if !strings.Contains(view, tt.want) {
				t.Fatalf("minimum view missing %q:\n%s", tt.want, view)
			}
			if tt.name == "idle" && !strings.Contains(ansi.Strip(view), "Ask or type / for commands") {
				t.Fatalf("minimum composer placeholder is not actionable:\n%s", view)
			}
			assertRenderedLinesFit(t, view, 30)
			if got := lipgloss.Height(strings.TrimSuffix(view, "\n")); got > 12 {
				t.Fatalf("minimum view height = %d, want <= 12:\n%s", got, view)
			}
			if m.composerIsBusy() && !strings.Contains(view, "esc") && tt.name != "export" {
				t.Fatalf("cancellable working state lost Esc affordance:\n%s", view)
			}
		})
	}
}

func TestMinimumTerminalNoticeKeepsSettingsRecoveryVisible(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = updated.(*Model)
	m.entries = append(m.entries, ChatEntry{Kind: "error", Content: "local runtime unavailable"})
	m.invalidateEntryCache()
	m.recalcViewportHeight()
	m.viewport.SetContent(m.renderEntries())
	m.viewport.GotoBottom()

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "ctrl+p settings") {
		t.Fatalf("minimum notice state lost the Settings recovery path:\n%s", view)
	}
	assertRenderedHeightFits(t, m.View().Content, m.height)
}

func TestAnimationClocksStopOutsideTheirPhase(t *testing.T) {
	m := newTestModel(t)
	m.initializing = false
	m.state = StateIdle

	spinnerMsg := m.spin.Tick()
	if _, cmd := m.Update(spinnerMsg); cmd != nil {
		t.Fatal("idle spinner tick scheduled another frame")
	}

	m.state = StateStreaming
	spinnerMsg = m.spin.Tick()
	if _, cmd := m.Update(spinnerMsg); cmd == nil {
		t.Fatal("active streaming spinner did not schedule another frame")
	}

	m.state = StateIdle
	scrambleCmd := m.scramble.Tick()
	if _, cmd := m.Update(scrambleCmd()); cmd != nil {
		t.Fatal("idle scramble tick scheduled another frame")
	}

	m.state = StateWaiting
	scrambleCmd = m.scramble.Tick()
	if _, cmd := m.Update(scrambleCmd()); cmd == nil {
		t.Fatal("waiting scramble did not schedule another frame")
	}
}

func TestToolCardUsesSharedSpinnerAndCompletedReceiptIsStable(t *testing.T) {
	m := newTestModel(t)
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(1500 * time.Millisecond) }
	m.state = StateStreaming
	m.toolsPending = 1
	m.toolEntries = []ToolEntry{{
		ID: "call-1", Name: "read_file", Args: `{"path":"internal/ui/view.go"}`,
		Status: ToolStatusRunning, StartTime: base, Collapsed: true,
	}}
	m.entries = []ChatEntry{
		{Kind: "user", Content: "inspect the UI"},
		{Kind: "tool_group", ToolIndex: 0},
	}
	m.toolCardMgr.AddCardWithID("call-1", "read_file", ToolCardFile, base)
	m.toolCardMgr.Cards[0].SetSummary("internal/ui/view.go")
	m.viewport.SetContent(m.renderEntries())
	before := m.viewport.View()
	footerBefore := m.renderWorkingLine()

	msg := m.spin.Tick()
	updated, _ := m.Update(msg)
	m = updated.(*Model)
	after := m.viewport.View()
	footerAfter := m.renderWorkingLine()
	if before == after {
		t.Fatalf("shared spinner tick did not repaint the running card:\n%s", after)
	}
	if footerBefore != footerAfter || !strings.Contains(footerAfter, "Tool running") || strings.Contains(footerAfter, "internal/ui") {
		t.Fatalf("tool footer competed with the animated card: before=%q after=%q", footerBefore, footerAfter)
	}
	for _, want := range []string{"Reading", "internal/ui", "1.5s"} {
		if !strings.Contains(after, want) {
			t.Fatalf("running tool receipt missing %q:\n%s", want, after)
		}
	}

	m.toolsPending = 0
	m.toolEntries[0].Status = ToolStatusDone
	m.toolCardMgr.Cards[0].State = ToolCardSuccess
	m.toolCardMgr.Cards[0].Duration = 2 * time.Second
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	stable := m.viewport.View()

	foreign := spinner.TickMsg{Time: base}
	updated, _ = m.Update(foreign)
	m = updated.(*Model)
	if got := m.viewport.View(); got != stable {
		t.Fatalf("completed card changed across an idle tick:\nbefore:\n%s\nafter:\n%s", stable, got)
	}
}

func TestApprovalModalPausesActivityClock(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	responses := make(chan permission.ApprovalResponse, 1)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "write_file",
		Args:     map[string]any{"path": "internal/ui/view.go"},
		Response: responses,
	})

	modal := ansi.Strip(m.renderApproval())
	for _, want := range []string{"Permission · write_file", "once", "session", "deny"} {
		if !strings.Contains(modal, want) {
			t.Fatalf("approval modal omitted %q:\n%s", want, modal)
		}
	}
	if m.needsSpinner() || m.needsScramble() || m.renderWorkingLine() != "" {
		t.Fatal("approval modal left a hidden activity clock or footer line active")
	}

	updated, cmd := m.Update(charKey('y'))
	m = updated.(*Model)
	if cmd == nil || m.pendingApproval != nil || !m.needsSpinner() {
		t.Fatal("approval response did not resume the streaming activity clock")
	}
	if response := <-responses; !response.Allowed {
		t.Fatal("approval response was not delivered")
	}
}

func TestShutdownTransfersWaitingAnimationOwnership(t *testing.T) {
	m := newTestModel(t)
	m.state = StateWaiting
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	stale := ScrambleTickMsg{ID: m.scramble.id, Frame: m.scramble.frame}

	cmd := m.beginShutdown()
	if cmd == nil || m.needsScramble() || !m.needsSpinner() {
		t.Fatal("shutdown did not transfer from scramble to spinner ownership")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("shutdown did not cancel the waiting turn")
	}
	frame := m.scramble.frame
	updated, _ := m.Update(stale)
	m = updated.(*Model)
	if m.scramble.frame != frame {
		t.Fatal("a hidden waiting tick advanced during shutdown")
	}
	if got := strings.Count(m.View().Content, "Stopping safely"); got != 1 {
		t.Fatalf("shutdown activity rendered %d competing surfaces", got)
	}
}

func TestOwnedOperationsNeverRenderReady(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*Model)
	}{
		{name: "file", set: func(m *Model) { m.fileLoading = true }},
		{name: "commit", set: func(m *Model) { m.commitRunning = true }},
		{name: "export", set: func(m *Model) { m.exportRunning = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			tt.set(m)
			if got := m.renderStatusLine(); strings.Contains(strings.ToLower(got), "ready") {
				t.Fatalf("owned operation rendered misleading ready status: %q", got)
			}
			if line := m.renderWorkingLine(); line == "" {
				t.Fatal("owned operation has no visible working line")
			}
		})
	}
}

func TestPausedFollowRecoveryFitsTheSingleWorkingRow(t *testing.T) {
	for _, width := range []int{30, 40, 80} {
		m := newTestModel(t)
		m.width = width
		m.state = StateStreaming
		m.turnStartedAt = m.nowTime().Add(-2 * time.Second)
		m.pauseFollow()

		line := ansi.Strip(m.renderWorkingLine())
		if !strings.Contains(line, "end") {
			t.Fatalf("width %d paused working line lost End recovery: %q", width, line)
		}
		if !strings.Contains(line, "esc") {
			t.Fatalf("width %d paused working line lost cancellation: %q", width, line)
		}
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("width %d paused working line is %d cells: %q", width, got, line)
		}
	}
}

func TestCompletedTurnShowsShortUsefulReceipt(t *testing.T) {
	m := newTestModel(t)
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(2400 * time.Millisecond) }
	m.turnStartedAt = base
	m.state = StateStreaming

	updated, _ := m.Update(AgentDoneMsg{})
	m = updated.(*Model)
	status := m.renderStatusLine()
	for _, want := range []string{"Done", "2.4s"} {
		if !strings.Contains(status, want) {
			t.Fatalf("completion receipt missing %q: %q", want, status)
		}
	}

	updated, _ = m.Update(DoneFlashExpiredMsg{})
	m = updated.(*Model)
	if strings.Contains(m.renderStatusLine(), "Done") {
		t.Fatal("completion receipt did not settle back to idle status")
	}
}

func TestReducedMotionUsesStaticWorkingGlyph(t *testing.T) {
	t.Setenv("LOCAL_AGENT_REDUCED_MOTION", "1")
	m := newTestModel(t)
	if !m.reducedMotion {
		t.Fatal("reduced-motion environment was not applied")
	}
	m.state = StateStreaming
	line := m.renderWorkingLine()
	if !strings.Contains(line, "•") || !strings.Contains(line, "Responding") {
		t.Fatalf("reduced-motion working line is not clear and static: %q", line)
	}
	if cmd := m.startSpinnerCmd(); cmd != nil {
		t.Fatal("reduced motion scheduled a spinner clock")
	}
}

func TestFormatWorkingElapsed(t *testing.T) {
	tests := map[time.Duration]string{
		0:                       "0.0s",
		1500 * time.Millisecond: "1.5s",
		12 * time.Second:        "12s",
		64 * time.Second:        "1m04s",
		-500 * time.Millisecond: "0.0s",
	}
	for input, want := range tests {
		if got := formatWorkingElapsed(input); got != want {
			t.Errorf("formatWorkingElapsed(%s) = %q, want %q", input, got, want)
		}
	}
}

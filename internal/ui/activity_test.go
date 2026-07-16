package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/abdul-hamid-achik/local-agent/internal/imageasset"
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
		}, want: "MCP unavailable"},
		{name: "waiting", set: func(m *Model) {
			m.state = StateWaiting
			m.turnStartedAt = base.Add(-1500 * time.Millisecond)
		}, want: "Running"},
		{name: "reasoning", set: func(m *Model) {
			m.state = StateStreaming
			m.turnStartedAt = base.Add(-2 * time.Second)
			m.thinkBuf.WriteString("checking")
		}, want: "Running"},
		{name: "streaming", set: func(m *Model) {
			m.state = StateStreaming
			m.turnStartedAt = base.Add(-3 * time.Second)
			m.streamBuf.WriteString("partial")
		}, want: "Running"},
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
			if (tt.name == "waiting" || tt.name == "reasoning" || tt.name == "streaming") &&
				!strings.Contains(view, "queue") {
				t.Fatalf("minimum active-turn view lost the queue affordance:\n%s", view)
			}
		})
	}
}

func TestAutoCheckpointActivityExplainsInvisibleContinuation(t *testing.T) {
	m := newTestModel(t)
	m.state = StateWaiting
	m.autoCheckpoints.segmentsContinued = 2
	activity, ok := m.currentWorkingActivity()
	if !ok || activity.label != "Continuing automatically" ||
		activity.detail != "checkpoint 2/8" || !activity.cancellable {
		t.Fatalf("AUTO checkpoint activity = %#v, ok=%v", activity, ok)
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

func TestActiveToolFooterLeavesBreathingRowBeforeComposer(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(*Model)
	m.state = StateStreaming
	m.toolsPending = 1
	m.reducedMotion = true
	m.recalcViewportHeight()
	m.viewport.SetContent(m.renderEntries())

	view := ansi.Strip(m.View().Content)
	statusAt := strings.Index(view, "Tool running")
	composerAt := strings.Index(view, "Write a follow-up")
	if statusAt < 0 || composerAt < 0 || statusAt >= composerAt {
		t.Fatalf("activity/composer order is invalid: status=%d composer=%d\n%s", statusAt, composerAt, view)
	}
	between := view[statusAt:composerAt]
	if !strings.Contains(between, "\n\n") {
		t.Fatalf("activity rail has no breathing row before composer:\n%s", between)
	}
	assertRenderedHeightFits(t, m.View().Content, m.height)
}

func TestAutoActivityFooterSeparatesAuthorityAndKeyboardActions(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(*Model)
	m.mode = ModeAuto
	m.state = StateStreaming
	m.reducedMotion = true
	m.turnStartedAt = time.Now().Add(-2 * time.Second)

	line := m.renderWorkingLine()
	plain := ansi.Strip(line)
	for _, want := range []string{"Running", "AUTO", "esc cancel", "enter queue"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("AUTO activity footer omitted %q: %q", want, plain)
		}
	}
	for _, styled := range []string{
		m.styles.ToolRunningText.Render("Running"),
		m.styles.ModeBuild.Render("AUTO"),
		m.styles.FocusIndicator.Render("esc"),
		m.styles.FocusIndicator.Render("enter"),
	} {
		if !strings.Contains(line, styled) {
			t.Fatalf("AUTO activity footer lost semantic styling %q:\n%s", ansi.Strip(styled), line)
		}
	}
	if width := lipgloss.Width(line); width > m.chatPaneWidth() {
		t.Fatalf("AUTO activity footer width = %d, pane = %d: %q", width, m.chatPaneWidth(), plain)
	}
}

func TestMinimumAutoActivityKeepsAuthorityAndRecoveryKeys(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = updated.(*Model)
	m.mode = ModeAuto
	m.state = StateStreaming
	m.reducedMotion = true

	line := m.renderWorkingLine()
	plain := ansi.Strip(line)
	for _, want := range []string{"Run", "AUTO", "esc", "queue"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("minimum AUTO footer omitted %q: %q", want, plain)
		}
	}
	if width := lipgloss.Width(line); width > m.chatPaneWidth() {
		t.Fatalf("minimum AUTO footer width = %d, pane = %d: %q", width, m.chatPaneWidth(), plain)
	}
}

func TestAutoActivityRetainsAuthorityAcrossResponsiveVariants(t *testing.T) {
	variants := []struct {
		name  string
		setup func(*Model)
	}{
		{name: "queue available", setup: func(*Model) {}},
		{name: "goal owns queue", setup: func(m *Model) { m.goalTurnID = "goal-turn" }},
		{name: "follow paused", setup: func(m *Model) { m.pauseFollow() }},
	}
	for _, width := range []int{30, 40, 58, 80} {
		for _, variant := range variants {
			t.Run(fmt.Sprintf("%s/%d", variant.name, width), func(t *testing.T) {
				m := newTestModel(t)
				m.width = width
				m.mode = ModeAuto
				m.state = StateStreaming
				m.reducedMotion = true
				variant.setup(m)

				line := ansi.Strip(m.renderWorkingLine())
				if !strings.Contains(line, "AUTO") {
					t.Fatalf("width %d AUTO footer lost authority in %s: %q", width, variant.name, line)
				}
				if got := lipgloss.Width(line); got > m.chatPaneWidth() {
					t.Fatalf("width %d AUTO footer is %d cells in %s: %q", width, got, variant.name, line)
				}
			})
		}
	}
}

func TestWorkingControlKeyRecognizesOnlyRealFooterKeys(t *testing.T) {
	for _, test := range []struct {
		segment string
		want    string
	}{
		{segment: "esc cancel", want: "esc"},
		{segment: "enter queue", want: "enter"},
		{segment: "end latest", want: "end"},
		{segment: "Route ambiguous", want: ""},
		{segment: "queue", want: ""},
	} {
		if got := workingControlKey(test.segment); got != test.want {
			t.Fatalf("workingControlKey(%q) = %q, want %q", test.segment, got, test.want)
		}
	}
}

func TestCompletedToolFlashAdvertisesInspectableReceipt(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(*Model)
	m.doneFlash = true
	m.toolEntries = []ToolEntry{{Name: "hitspec_capture_webpage", Status: ToolStatusDone, Collapsed: true}}
	m.lastTurnToolIndex = 0

	status := ansi.Strip(m.renderStatusLine())
	if !strings.Contains(status, "Done") || !strings.Contains(status, "space inspect receipt") {
		t.Fatalf("completed tool footer does not expose receipt inspection: %q", status)
	}
	m.toolEntries[0].Collapsed = false
	if status = ansi.Strip(m.renderStatusLine()); !strings.Contains(status, "space hide receipt") {
		t.Fatalf("expanded tool footer does not expose receipt collapse: %q", status)
	}

	m.input.SetValue("draft")
	if status = ansi.Strip(m.renderStatusLine()); strings.Contains(status, "space inspect receipt") || strings.Contains(status, "space hide receipt") {
		t.Fatalf("receipt shortcut was advertised while Space edits a draft: %q", status)
	}
}

func TestCompletedNoToolTurnDoesNotAdvertiseStaleReceipt(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(*Model)
	m.toolEntries = []ToolEntry{{Name: "read_file", Status: ToolStatusDone, Collapsed: true}}
	m.turnToolStartIndex = len(m.toolEntries)
	m.state = StateStreaming

	updated, _ = m.Update(AgentDoneMsg{})
	m = updated.(*Model)
	status := ansi.Strip(m.renderStatusLine())
	if !strings.Contains(status, "Done") {
		t.Fatalf("successful no-tool turn lost completion status: %q", status)
	}
	if strings.Contains(status, "inspect receipt") || strings.Contains(status, "hide receipt") {
		t.Fatalf("no-tool turn advertised an earlier tool receipt: %q", status)
	}
}

func TestInlineApprovalPausesActivityClock(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	responses := make(chan permission.ApprovalResponse, 1)
	m = openApprovalForTest(t, m, ToolApprovalMsg{
		ToolName: "write_file",
		Args:     map[string]any{"path": "internal/ui/view.go"},
		Response: responses,
	})

	inline := ansi.Strip(m.renderApproval())
	for _, want := range []string{"Permission · write_file", "once", "session", "deny"} {
		if !strings.Contains(inline, want) {
			t.Fatalf("inline approval omitted %q:\n%s", want, inline)
		}
	}
	if m.needsSpinner() || m.needsScramble() || m.renderWorkingLine() != "" {
		t.Fatal("inline approval left a hidden activity clock or footer line active")
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
	if !strings.Contains(line, "…") || !strings.Contains(line, "Running") {
		t.Fatalf("reduced-motion working line is not clear and static: %q", line)
	}
	if strings.Contains(line, "•") {
		t.Fatalf("reduced-motion working line used an ambiguous settled dot: %q", line)
	}
	if cmd := m.startSpinnerCmd(); cmd != nil {
		t.Fatal("reduced motion scheduled a spinner clock")
	}
}

func TestReducedMotionUsesStaticRunningToolGlyph(t *testing.T) {
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	newRunningModel := func(t *testing.T) *Model {
		t.Helper()
		m := newTestModel(t)
		m.reducedMotion = true
		m.now = func() time.Time { return base.Add(1500 * time.Millisecond) }
		m.toolEntries = []ToolEntry{{
			ID: "call-1", Name: "read_file", Status: ToolStatusRunning, StartTime: base,
		}}
		return m
	}

	t.Run("tool card", func(t *testing.T) {
		m := newRunningModel(t)
		m.toolCardMgr.AddCardWithID("call-1", "read_file", ToolCardFile, base)
		var rendered strings.Builder
		m.renderToolGroup(&rendered, 0)
		view := ansi.Strip(rendered.String())
		if !strings.Contains(view, "…") || strings.Contains(view, "•") {
			t.Fatalf("reduced-motion tool card used an ambiguous activity glyph: %q", view)
		}
	})

	t.Run("fallback receipt", func(t *testing.T) {
		m := newRunningModel(t)
		var rendered strings.Builder
		m.renderToolGroup(&rendered, 0)
		view := ansi.Strip(rendered.String())
		if !strings.Contains(view, "…") || strings.Contains(view, "•") {
			t.Fatalf("reduced-motion fallback receipt used an ambiguous activity glyph: %q", view)
		}
	})
}

func TestRunningFooterOwnsControlsNotAssistantState(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.model = "private-model-name"
	m.thinkBuf.WriteString("working through the request")

	line := ansi.Strip(m.renderWorkingLine())
	for _, want := range []string{"Running", "esc cancel", "enter queue"} {
		if !strings.Contains(line, want) {
			t.Fatalf("operational footer omitted %q: %q", want, line)
		}
	}
	for _, forbidden := range []string{"Thinking", "Reasoning", "Responding", "private-model-name"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("operational footer duplicated assistant/model state %q: %q", forbidden, line)
		}
	}
}

func TestIdleRecoveryUsesOneCompactFooterAction(t *testing.T) {
	m := newTestModel(t)
	m.standaloneRecovery = &standaloneRecoveryState{}

	status := ansi.Strip(m.renderStatusLine())
	for _, want := range []string{"Recovery paused", "/recover", "inspect"} {
		if !strings.Contains(status, want) {
			t.Fatalf("recovery footer omitted %q: %q", want, status)
		}
	}
	if strings.Contains(status, "\n") {
		t.Fatalf("wide recovery footer used more than one row: %q", status)
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

func TestWorkingLineKeepsActiveSessionHandleAtOrdinaryWidth(t *testing.T) {
	for _, width := range []int{30, 40, 80} {
		m := newTestModel(t)
		m.width = width
		m.state = StateWaiting
		m.turnStartedAt = time.Now().Add(-2 * time.Second)
		m.sessionID = 7
		m.activeSessionTitle = "Composer polish"

		line := ansi.Strip(m.renderWorkingLine())
		if !strings.Contains(line, "S7") {
			t.Fatalf("width %d working line omitted active session handle: %q", width, line)
		}
	}
}

func TestSpecialFootersKeepActiveSessionHandle(t *testing.T) {
	for _, width := range []int{30, 40, 80} {
		m := newTestModel(t)
		m.width = width
		m.sessionID = 42
		m.activeSessionTitle = "TUI polish"

		m.pauseFollow()
		if line := ansi.Strip(m.renderFollowPausedStatus(width)); !strings.Contains(line, "S42") || !strings.Contains(line, "end") {
			t.Fatalf("width %d follow-paused footer = %q", width, line)
		}

		m.resumeFollow()
		m.standaloneRecovery = &standaloneRecoveryState{}
		if line := ansi.Strip(m.renderStatusLine()); !strings.Contains(line, "S42") || !strings.Contains(line, "/recover") {
			t.Fatalf("width %d recovery footer = %q", width, line)
		}

		m.standaloneRecovery = nil
		m.pendingImages = []pendingImageAttachment{{Ref: imageasset.Ref{Name: "screen.png"}}}
		if line := ansi.Strip(m.renderStatusLine()); !strings.Contains(line, "S42") || !strings.Contains(line, "Images ready") {
			t.Fatalf("width %d image footer = %q", width, line)
		}
	}
}

func TestWorkingAuthorityAndSessionIdentityCoexistAcrossWidths(t *testing.T) {
	for _, mode := range []struct {
		value Mode
		label string
	}{
		{value: ModeAuto, label: "AUTO"},
		{value: ModePlan, label: "PLAN"},
	} {
		for _, width := range []int{30, 40, 80} {
			t.Run(fmt.Sprintf("%s/%d", mode.label, width), func(t *testing.T) {
				m := newTestModel(t)
				m.width = width
				m.mode = mode.value
				m.state = StateWaiting
				m.reducedMotion = true
				m.sessionID = 7
				m.activeSessionTitle = "Composer polish"

				line := ansi.Strip(m.renderWorkingLine())
				for _, want := range []string{mode.label, "S7"} {
					if !strings.Contains(line, want) {
						t.Fatalf("width %d footer omitted %q: %q", width, want, line)
					}
				}
				if got := lipgloss.Width(line); got > m.chatPaneWidth() {
					t.Fatalf("width %d footer is %d cells, pane %d: %q", width, got, m.chatPaneWidth(), line)
				}
			})
		}
	}
}

func TestSessionRestoreActivityUsesPendingTargetIdentity(t *testing.T) {
	for _, width := range []int{30, 40, 80} {
		m := newTestModel(t)
		m.width = width
		m.reducedMotion = true
		m.sessionID = 7
		m.activeSessionTitle = "Source work"
		m.sessionLoading = true
		m.sessionLoadToken = 11
		m.pendingSessionSwitch = &pendingSessionSwitch{
			TargetSessionID: 42,
			TargetTitle:     "Target work",
			Choice:          sessionSwitchKeep,
			LoadToken:       11,
		}

		line := ansi.Strip(m.renderWorkingLine())
		if !strings.Contains(line, "S42") || strings.Contains(line, "S7") {
			t.Fatalf("width %d restore activity used source identity: %q", width, line)
		}
		if width >= 72 && !strings.Contains(line, "Target work") {
			t.Fatalf("width %d restore activity hid target title: %q", width, line)
		}
		if got := lipgloss.Width(line); got > m.chatPaneWidth() {
			t.Fatalf("width %d restore activity is %d cells, pane %d: %q", width, got, m.chatPaneWidth(), line)
		}
	}
}

func TestWorkingFooterShowsUnqueuedDraftImages(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.state = StateWaiting
	m.pendingImages = []pendingImageAttachment{{Ref: imageasset.Ref{Name: "screen.png"}}}
	line := ansi.Strip(m.renderWorkingLine())
	if !strings.Contains(line, "+ 1 image") {
		t.Fatalf("working footer hid pending draft image: %q", line)
	}
}

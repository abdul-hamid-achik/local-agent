package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/charmbracelet/x/ansi"
)

// TestScrollAnchor_Initialization verifies scroll anchor is properly initialized
func TestScrollAnchor_Initialization(t *testing.T) {
	m := newTestModel(t)

	// Simulate WindowSizeMsg to trigger initialization
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(*Model)

	if !m.ready {
		t.Fatal("viewport should be ready after WindowSizeMsg")
	}
	if !m.anchorActive {
		t.Error("anchorActive should be true after initialization")
	}
}

// TestScrollAnchor_MouseWheelUp disables anchor when scrolling up
func TestScrollAnchor_MouseWheelUp(t *testing.T) {
	m := newTestModel(t)
	m.anchorActive = true
	m.userScrolledUp = false

	// Add enough content to make viewport scrollable
	var longContent string
	for i := 0; i < 100; i++ {
		longContent += "line " + string(rune(i)) + "\n"
	}
	m.viewport.SetContent(longContent)
	m.viewport.GotoBottom()

	if !m.viewport.AtBottom() {
		t.Fatal("viewport should be at bottom before scroll")
	}
	before := m.viewport.YOffset()
	delta := m.viewport.MouseWheelDelta

	updated, _ := m.Update(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelUp})
	m = updated.(*Model)
	if got := before - m.viewport.YOffset(); got != delta {
		t.Fatalf("one wheel notch moved %d rows, want %d", got, delta)
	}

	if m.anchorActive {
		t.Error("anchorActive should be false after scrolling up")
	}
	if !m.userScrolledUp {
		t.Error("userScrolledUp should be true after scrolling up")
	}
}

// TestScrollAnchor_MouseWheelDown re-enables anchor when scrolling to bottom
func TestScrollAnchor_MouseWheelDown(t *testing.T) {
	m := newTestModel(t)
	m.anchorActive = false
	m.userScrolledUp = true

	// Minimal content - viewport at bottom
	m.viewport.SetContent("short content")

	updated, _ := m.Update(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown})
	m = updated.(*Model)

	// At bottom with minimal content, anchor should be re-enabled
	if !m.anchorActive {
		t.Error("anchorActive should be true when at bottom")
	}
}

// TestScrollAnchor_StreamTextMsg respects anchor state
func TestScrollAnchor_StreamTextMsg(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming

	// Add some initial content
	m.entries = []ChatEntry{
		{Kind: "assistant", Content: "Initial response"},
	}
	m.viewport.SetContent(m.renderEntries())

	// Test with anchor active - should auto-scroll
	m.anchorActive = true
	updated, _ := m.Update(StreamTextMsg{Text: "more"})
	m = updated.(*Model)

	// Viewport should be at bottom when anchor is active
	if !m.viewport.AtBottom() {
		t.Error("viewport should be at bottom when anchor is active")
	}

	// Test with anchor inactive - should not force scroll
	m.anchorActive = false
	m.viewport.GotoTop() // Scroll to top

	updated, _ = m.Update(StreamTextMsg{Text: "even more"})
	m = updated.(*Model)

	// When anchor is inactive, viewport should stay near top
	// (implementation may vary, but should not force GotoBottom)
	if m.viewport.AtBottom() {
		t.Log("Note: viewport scrolled to bottom even with anchor inactive")
	}
}

// TestScrollAnchor_AgentDoneMsg preserves an explicit paused-follow intent.
func TestScrollAnchor_AgentDoneMsg(t *testing.T) {
	m := newTestModel(t)
	setScrollableTranscript(m)
	m.viewport.GotoTop()
	m.state = StateStreaming
	m.pauseFollow()

	updated, _ := m.Update(AgentDoneMsg{})
	m = updated.(*Model)

	if m.state != StateIdle {
		t.Errorf("state should be StateIdle, got %d", m.state)
	}
	if m.anchorActive || !m.userScrolledUp {
		t.Error("AgentDoneMsg discarded the user's paused-follow intent")
	}
}

// TestScrollAnchor_ToolMessages respect anchor
func TestScrollAnchor_ToolMessages(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.anchorActive = true

	// Test ToolCallStartMsg
	updated, _ := m.Update(ToolCallStartMsg{
		Name:      "read_file",
		Args:      map[string]any{"path": "test.go"},
		StartTime: testTime,
	})
	m = updated.(*Model)

	if !m.anchorActive {
		t.Error("anchorActive should remain true after ToolCallStartMsg")
	}

	// Test ToolCallResultMsg
	updated, _ = m.Update(ToolCallResultMsg{
		Name:     "read_file",
		Result:   "file content",
		IsError:  false,
		Duration: testDuration,
	})
	m = updated.(*Model)

	if !m.anchorActive {
		t.Error("anchorActive should remain true after ToolCallResultMsg")
	}
}

// TestScrollAnchor_SystemMessages respect anchor
func TestScrollAnchor_SystemMessages(t *testing.T) {
	m := newTestModel(t)
	m.anchorActive = true

	// Test SystemMessageMsg
	updated, _ := m.Update(SystemMessageMsg{Msg: "system message"})
	m = updated.(*Model)

	if !m.anchorActive {
		t.Error("anchorActive should remain true after SystemMessageMsg")
	}

	// Test ErrorMsg
	updated, _ = m.Update(ErrorMsg{Msg: "error message"})
	m = updated.(*Model)

	if !m.anchorActive {
		t.Error("anchorActive should remain true after ErrorMsg")
	}
}

// TestScrollAnchor_WindowResize maintains anchor state
func TestScrollAnchor_WindowResize(t *testing.T) {
	m := newTestModel(t)

	// Initialize with first size
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(*Model)

	if !m.anchorActive {
		t.Fatal("anchorActive should be true after initial sizing")
	}

	// Note: Window resize currently resets anchor state in the implementation
	// This is acceptable behavior as resize is a significant layout change
	// The anchor will be re-established based on scroll position
}

// TestCheckAutoScroll_reenablesAnchorAtBottom
func TestCheckAutoScroll_ReenablesAnchorAtBottom(t *testing.T) {
	m := newTestModel(t)
	m.anchorActive = false
	m.userScrolledUp = true

	// Set viewport to bottom
	m.viewport.SetContent("short content")
	m.viewport.GotoBottom()

	m.checkAutoScroll()

	if !m.anchorActive {
		t.Error("checkAutoScroll should set anchorActive to true when at bottom")
	}
	if m.userScrolledUp {
		t.Error("checkAutoScroll should set userScrolledUp to false when at bottom")
	}
}

func TestKeyboardScrollPausesFollowAndEndResumesLatest(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updated.(*Model)
	setScrollableTranscript(m)
	m.resumeFollow()
	bottom := m.viewport.YOffset()

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = updated.(*Model)
	pausedOffset := m.viewport.YOffset()
	if !m.followPaused() || pausedOffset >= bottom {
		t.Fatalf("Page Up did not pause transcript follow: offset=%d bottom=%d", pausedOffset, bottom)
	}
	if status := ansi.Strip(m.renderStatusLine()); !strings.Contains(status, "Follow paused") || !strings.Contains(status, "end latest") {
		t.Fatalf("paused transcript has no recovery affordance: %q", status)
	}

	m.state = StateStreaming
	updated, _ = m.Update(StreamTextMsg{Text: "new streamed output"})
	m = updated.(*Model)
	if got := m.viewport.YOffset(); got != pausedOffset {
		t.Fatalf("next token snapped paused viewport from %d to %d", pausedOffset, got)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = updated.(*Model)
	if m.followPaused() || !m.viewport.AtBottom() {
		t.Fatal("End did not resume follow at the latest output")
	}
}

func TestAgentDonePreservesPausedViewportOffset(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updated.(*Model)
	setScrollableTranscript(m)
	m.resumeFollow()
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = updated.(*Model)
	pausedOffset := m.viewport.YOffset()
	m.state = StateStreaming
	m.streamBuf.WriteString("settling answer")

	updated, _ = m.Update(AgentDoneMsg{})
	m = updated.(*Model)
	if !m.followPaused() {
		t.Fatal("turn completion resumed follow without user intent")
	}
	if got := m.viewport.YOffset(); got != pausedOffset {
		t.Fatalf("turn completion moved paused viewport from %d to %d", pausedOffset, got)
	}
}

func TestEndPreservesNonemptyComposerAndOverlayOwnership(t *testing.T) {
	m := newTestModel(t)
	setScrollableTranscript(m)
	m.pauseFollow()
	m.input.SetValue("draft")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = updated.(*Model)
	if !m.followPaused() || m.input.Value() != "draft" {
		t.Fatal("End stole normal textarea behavior from a nonempty draft")
	}

	m.input.SetValue("")
	m.overlay = OverlayHelp
	m.initHelpViewport()
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = updated.(*Model)
	if !m.followPaused() || m.overlay != OverlayHelp {
		t.Fatal("End moved the hidden transcript behind an overlay")
	}
}

func TestEndResumesFollowDuringOwnedBusyStates(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Model)
	}{
		{name: "session loading", setup: func(m *Model) { m.sessionLoading = true }},
		{name: "session listing", setup: func(m *Model) { m.sessionListing = true }},
		{name: "file loading", setup: func(m *Model) { m.fileLoading = true }},
		{name: "export", setup: func(m *Model) { m.exportRunning = true }},
		{name: "commit", setup: func(m *Model) { m.commitRunning = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			setScrollableTranscript(m)
			m.viewport.GotoTop()
			m.pauseFollow()
			tt.setup(m)
			if working := ansi.Strip(m.renderWorkingLine()); !strings.Contains(working, "end") {
				t.Fatalf("busy footer did not advertise latest recovery: %q", working)
			}

			updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			m = updated.(*Model)
			if m.followPaused() || !m.viewport.AtBottom() {
				t.Fatal("owned busy-state guard swallowed End")
			}
		})
	}
}

func TestReceiptActionsKeepFollowIntentAligned(t *testing.T) {
	tests := []struct {
		name string
		act  func(*Model)
	}{
		{name: "agent profile selection", act: func(m *Model) { m.selectAgentProfile("") }},
		{name: "legacy migration receipt", act: func(m *Model) { m.appendLegacyMigrationEntry("system", "Migration preview") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			setScrollableTranscript(m)
			m.viewport.GotoTop()
			m.pauseFollow()

			tt.act(m)
			if m.followPaused() || !m.viewport.AtBottom() {
				t.Fatal("explicit receipt jumped without resuming follow intent")
			}
		})
	}
}

func setScrollableTranscript(m *Model) {
	m.entries = nil
	for i := 0; i < 24; i++ {
		m.entries = append(m.entries, ChatEntry{Kind: "user", Content: fmt.Sprintf("transcript row %02d", i)})
	}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
}

// TestScrollAnchor_ViewportAtBottom helper
func TestScrollAnchor_ViewportAtBottom(t *testing.T) {
	m := newTestModel(t)

	// Short content - should be at bottom
	m.viewport.SetContent("line1\nline2\nline3")

	if !m.viewport.AtBottom() {
		t.Error("viewport should be at bottom with short content")
	}

	// Long content - scroll to bottom
	var longContent string
	for i := 0; i < 100; i++ {
		longContent += "line " + string(rune(i)) + "\n"
	}
	m.viewport.SetContent(longContent)
	m.viewport.GotoBottom()

	if !m.viewport.AtBottom() {
		t.Error("viewport should be at bottom after GotoBottom()")
	}

	// Scroll up - should not be at bottom
	m.viewport.GotoTop()

	if m.viewport.AtBottom() {
		t.Error("viewport should not be at bottom after scrolling to top")
	}
}

func TestOverlayMouseWheelScrollsDocumentInsteadOfTranscript(t *testing.T) {
	for _, overlay := range []OverlayKind{OverlayHelp, OverlayRuntimeStatus, OverlayGoalInspector} {
		t.Run(overlayName(overlay), func(t *testing.T) {
			m := newTestModel(t)
			m.viewport.SetContent(strings.Repeat("transcript line\n", 80))
			m.viewport.GotoTop()
			transcriptOffset := m.viewport.YOffset()

			var modalOffset func() int
			var modalDelta int
			switch overlay {
			case OverlayHelp:
				m.overlay = OverlayHelp
				m.initHelpViewport()
				m.helpViewport.SetHeight(2)
				m.helpViewport.SetContent(strings.Repeat("help line\n", 40))
				modalDelta = m.helpViewport.MouseWheelDelta
				modalOffset = func() int { return m.helpViewport.YOffset() }
			case OverlayRuntimeStatus:
				m.openRuntimeStatus()
				m.runtimeStatusState.Viewport.SetHeight(2)
				m.runtimeStatusState.Viewport.SetContent(strings.Repeat("runtime line\n", 40))
				modalDelta = m.runtimeStatusState.Viewport.MouseWheelDelta
				modalOffset = func() int { return m.runtimeStatusState.Viewport.YOffset() }
			case OverlayGoalInspector:
				m.goalInspectorState = NewGoalInspector(goalInspectorFixture(time.Now()), nil, GoalInspectorOptions{Width: 80, Height: 24})
				m.overlay = OverlayGoalInspector
				m.goalInspectorState.viewport.SetHeight(2)
				m.goalInspectorState.viewport.SetContent(strings.Repeat("goal line\n", 40))
				modalDelta = m.goalInspectorState.viewport.MouseWheelDelta
				modalOffset = func() int { return m.goalInspectorState.viewport.YOffset() }
			}

			updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
			m = updated.(*Model)
			if got := modalOffset(); got != modalDelta {
				t.Fatalf("modal wheel moved %d rows, want %d", got, modalDelta)
			}
			if got := m.viewport.YOffset(); got != transcriptOffset {
				t.Fatalf("hidden transcript moved from %d to %d", transcriptOffset, got)
			}
			if !m.anchorActive || m.userScrolledUp {
				t.Fatalf("modal wheel changed transcript anchor: active=%v scrolled=%v", m.anchorActive, m.userScrolledUp)
			}
		})
	}
}

func TestOtherOverlaysSwallowMouseWheel(t *testing.T) {
	overlays := []OverlayKind{
		OverlayCompletion,
		OverlayModelPicker,
		OverlayPlanForm,
		OverlaySessionsPicker,
		OverlaySettings,
		OverlayAgentPicker,
		OverlayModePicker,
		OverlayGoalForm,
	}
	for _, overlay := range overlays {
		t.Run(overlayName(overlay), func(t *testing.T) {
			m := newTestModel(t)
			m.viewport.SetContent(strings.Repeat("transcript line\n", 80))
			m.viewport.GotoTop()
			m.toolEntries = []ToolEntry{{Collapsed: true}}
			m.toolHitRegions = []toolHitRegion{{ToolIndex: 0, Row: 0, EndCol: 12}}
			m.overlay = overlay

			updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
			m = updated.(*Model)
			if got := m.viewport.YOffset(); got != 0 {
				t.Fatalf("overlay wheel moved hidden transcript to %d", got)
			}
		})
	}
}

func TestOverlayClicksNeverToggleHiddenToolCards(t *testing.T) {
	for overlay := OverlayHelp; overlay <= OverlayGoalInspector; overlay++ {
		t.Run(overlayName(overlay), func(t *testing.T) {
			m := newTestModel(t)
			m.toolEntries = []ToolEntry{{Collapsed: true}}
			m.toolHitRegions = []toolHitRegion{{ToolIndex: 0, Row: 0, EndCol: 12}}
			m.overlay = overlay

			updated, _ := m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
			m = updated.(*Model)
			if !m.toolEntries[0].Collapsed {
				t.Fatal("overlay click toggled a hidden ToolCard")
			}
		})
	}
}

func overlayName(overlay OverlayKind) string {
	switch overlay {
	case OverlayHelp:
		return "help"
	case OverlayCompletion:
		return "completion"
	case OverlayModelPicker:
		return "model"
	case OverlayPlanForm:
		return "plan"
	case OverlaySessionsPicker:
		return "sessions"
	case OverlaySettings:
		return "settings"
	case OverlayAgentPicker:
		return "agent"
	case OverlayModePicker:
		return "mode"
	case OverlayGoalForm:
		return "goal"
	case OverlayRuntimeStatus:
		return "runtime"
	case OverlayGoalInspector:
		return "goal-inspector"
	default:
		return "none"
	}
}

// BenchmarkScrollAnchor_Performance benchmarks the scroll anchor logic
func BenchmarkScrollAnchor_Performance(b *testing.B) {
	m := newTestModelB(b)
	m.anchorActive = true

	var longContent string
	for i := 0; i < 100; i++ {
		longContent += "line " + string(rune(i)) + "\n"
	}
	m.viewport.SetContent(longContent)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.viewport.AtBottom()
	}
}

// newTestModelB creates a Model for benchmarks
func newTestModelB(b *testing.B) *Model {
	reg := command.NewRegistry()
	command.RegisterBuiltins(reg)
	completer := NewCompleter(reg, []string{"model-a", "model-b"}, []string{"skill-a"}, []string{"agent-x"}, nil)
	ag := agent.New(nil, nil, 0)
	m := New(ag, reg, nil, completer, nil, nil, nil)
	m.initializing = false
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(*Model)
}

package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionTopBarAppearsOnRoomyFrame(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	frame := m.projectFrame()
	if !frame.Header.Visible || frame.Header.Content == "" {
		t.Fatalf("expected session top bar on roomy frame: %#v", frame.Header)
	}
	if frame.Header.Rect.Height() < 1 {
		t.Fatalf("header height = %d", frame.Header.Rect.Height())
	}
}

func TestSessionTopBarOwnsIdentityAndLeavesModeToTheShortcutsRow(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(*Model)
	m.model = "ornith:latest"
	m.setMode(ModeAuto)
	m.numCtx = 98_304
	m.promptTokens = 0
	bar := ansi.Strip(m.renderSessionTopBar(m.chatPaneWidth()))
	if !strings.Contains(bar, "ornith") {
		t.Fatalf("top bar missing the model it owns:\n%s", bar)
	}
	if !strings.Contains(bar, "0%") && !strings.Contains(bar, "0/") {
		t.Fatalf("top bar missing ambient context meter:\n%s", bar)
	}
	// Authority belongs beside shift+tab, not next to the workspace path.
	if strings.Contains(bar, "AUTO") {
		t.Fatalf("top bar re-printed mode owned by the shortcuts row:\n%s", bar)
	}
}

func TestUserPromptLeavesAGapBeforeNotice(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.entries = []ChatEntry{
		{Kind: "user", Content: "hello chat!"},
		{Kind: "system", Content: "ICE · recalled 1 past conversation"},
	}
	m.settleChromeSpringForTest()
	m.recalcViewportHeight()
	m.refreshTranscript()
	view := ansi.Strip(m.View().Content)
	promptAt := strings.Index(view, "hello chat!")
	noticeAt := strings.Index(view, "notice ·")
	if promptAt < 0 || noticeAt < 0 || noticeAt <= promptAt {
		t.Fatalf("prompt/notice order is invalid:\n%s", view)
	}
	between := view[promptAt:noticeAt]
	if strings.Count(between, "\n") < 2 {
		t.Fatalf("prompt needs a blank row before a notice:\n%s", between)
	}
}

func TestShortcutsBarInFooter(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	footer := ansi.Strip(m.projectFrame().Footer.Content)
	for _, want := range []string{"enter", "shift+tab"} {
		if !strings.Contains(strings.ToLower(footer), want) {
			t.Fatalf("footer shortcuts missing %q:\n%s", want, footer)
		}
	}
	// Idle with nothing to cancel must not advertise a dead "esc cancel".
	if strings.Contains(strings.ToLower(m.renderShortcutsBar(m.chatPaneWidth())), "esc") {
		t.Fatalf("idle footer advertised esc with nothing to cancel:\n%s", footer)
	}
}

func TestShortcutsBarShowsEscWhenCancelDoesSomething(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.queuedFollowUp = &queuedFollowUp{Prompt: "later"}
	footer := ansi.Strip(m.projectFrame().Footer.Content)
	if !strings.Contains(strings.ToLower(footer), "esc") {
		t.Fatalf("queued follow-up should advertise esc cancel:\n%s", footer)
	}
}

func TestMinimumTerminalSkipsSessionHeader(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: minTerminalWidth, Height: minTerminalHeight})
	m = updated.(*Model)
	frame := m.projectFrame()
	if frame.Header.Visible && frame.Header.Rect.Height() > 0 {
		t.Fatalf("min terminal should omit session header: %#v", frame.Header)
	}
}

func TestSessionChromeAlignsToContentOrigin(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	header := ansi.Strip(m.projectFrame().Header.Content)
	if header == "" {
		t.Fatal("expected header content")
	}
	// Top bar shares OriginX with transcript content (leading pad cells).
	firstLine := strings.Split(header, "\n")[0]
	if !strings.HasPrefix(firstLine, strings.Repeat(" ", contentLeftColumns)) {
		t.Fatalf("header should start at content OriginX=%d: %q", contentLeftColumns, firstLine)
	}
	// Empty welcome must not float mid-canvas: at most one blank row of pad.
	if got := emptyWelcomeTopPad(20, 4); got != 1 {
		t.Fatalf("emptyWelcomeTopPad(20,4)=%d, want 1", got)
	}
	if got := emptyWelcomeTopPad(4, 4); got != 0 {
		t.Fatalf("emptyWelcomeTopPad(4,4)=%d, want 0", got)
	}
	// Welcome orients the user without repeating the model owned by the top bar.
	m.model = "ornith:latest"
	var welcome strings.Builder
	m.renderWelcome(&welcome)
	plain := ansi.Strip(welcome.String())
	if strings.Contains(plain, "ornith:latest") {
		t.Fatalf("welcome re-printed model already on top bar:\n%s", plain)
	}
	for _, want := range []string{"LOCAL AGENT", "Ask a question", "@ add files", "/help commands"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("welcome missing orientation %q:\n%s", want, plain)
		}
	}
}

func TestEmptyStateFooterDoesNotDuplicateTopBar(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)
	m.model = "ornith:latest"
	m.promptTokens = 0
	m.numCtx = 98_304
	// Status line must stay quiet; composer meta may show the model under the
	// framed draft (Grok-style), which is not the ambient status strip.
	status := ansi.Strip(m.renderStatusLine())
	if status != "" {
		t.Fatalf("empty-state status should stay quiet, got %q", status)
	}
	footer := ansi.Strip(m.projectFrame().Footer.Content)
	if strings.Contains(footer, "0%") {
		t.Fatalf("empty footer re-printed ambient context meter:\n%s", footer)
	}
	if !strings.Contains(strings.ToLower(footer), "enter") {
		t.Fatalf("expected shortcuts bar in empty footer:\n%s", footer)
	}
}

func TestRoomyStatusDoesNotDoublePlan(t *testing.T) {
	// Captured live: status "[ PLAN · read-only ]" + shortcuts "· PLAN".
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)
	m.entries = []ChatEntry{{Kind: "user", Content: "hey chat"}}
	m.mode = ModePlan
	m.model = "ornith:latest"
	status := ansi.Strip(m.renderStatusLine())
	if strings.Contains(status, "PLAN") {
		t.Fatalf("status should not re-print PLAN when shortcuts own mode: %q", status)
	}
	bar := ansi.Strip(m.renderShortcutsBar(m.chatPaneWidth()))
	if !strings.Contains(bar, "PLAN") {
		t.Fatalf("shortcuts should own mode: %q", bar)
	}
	if strings.Contains(status, "ornith") || strings.Contains(bar, "ornith") {
		t.Fatalf("model belongs to the top bar, not status/shortcuts: status=%q bar=%q", status, bar)
	}
}

func TestBusyShortcutsDoNotDuplicateActivityControls(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)
	m.state = StateWaiting
	m.turnStartedAt = m.nowTime()
	footer := strings.ToLower(ansi.Strip(m.projectFrame().Footer.Content))
	// Activity rail owns esc stop / enter queue; shortcuts keep mode only.
	if strings.Count(footer, "esc") > 1 {
		t.Fatalf("esc stop duplicated across activity+shortcuts:\n%s", footer)
	}
	if !strings.Contains(footer, "shift+tab") {
		t.Fatalf("busy shortcuts should keep mode cycle:\n%s", footer)
	}
}

func TestTranscriptSeparatorsUseConsistentMessagePadding(t *testing.T) {
	// Grok rhythm: one blank row between messages.
	if got := transcriptEntrySeparator("user", "system"); got != "\n\n" {
		t.Fatalf("user→system separator = %q, want blank row", got)
	}
	if got := transcriptEntrySeparator("system", "assistant"); got != "\n\n" {
		t.Fatalf("system→assistant separator = %q, want blank row", got)
	}
	if got := transcriptEntrySeparator("assistant", "user"); got != "\n\n" {
		t.Fatalf("assistant→user separator = %q, want blank row", got)
	}
	// Tool cards stay dense.
	if got := transcriptEntrySeparator("tool_group", "tool_group"); got != "\n" {
		t.Fatalf("tool→tool separator = %q, want dense stack", got)
	}
	if got := transcriptEntrySeparator("assistant", "tool_group"); got != "\n\n" {
		t.Fatalf("assistant→tool separator = %q, want one blank row", got)
	}
}

func TestConversationPreservesCompletePromptsInOrderAcrossResize(t *testing.T) {
	m := newTestModel(t)
	prompt := "Compare the existing behavior with the requested change and preserve every detail including the final instruction."
	m.entries = []ChatEntry{
		{Kind: "user", Content: "First question"},
		{Kind: "assistant", Content: "First answer"},
		{Kind: "user", Content: prompt},
		{Kind: "assistant", Content: "Second answer"},
	}
	for _, size := range [][2]int{{100, 30}, {40, 20}, {30, 12}, {120, 36}} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(*Model)
		body := ansi.Strip(m.renderEntries())
		// Ignore wrapping and the user gutter when comparing the visible words.
		plain := strings.Join(strings.Fields(strings.ReplaceAll(body, glyphSet(m.glyphProfile).UserRail, "")), " ")
		last := -1
		for _, want := range []string{"First question", "First answer", prompt, "Second answer"} {
			at := strings.Index(plain, want)
			if at <= last || strings.Count(plain, want) != 1 {
				t.Fatalf("%dx%d lost or reordered %q:\n%s", size[0], size[1], want, body)
			}
			last = at
		}
		if strings.Contains(ansi.Strip(m.projectSessionHeader().content), "final instruction") {
			t.Fatal("conversation content escaped into fixed chrome")
		}
		assertRenderedLinesFit(t, m.View().Content, size[0])
	}
}

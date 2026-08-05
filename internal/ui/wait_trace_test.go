package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// waitTraceTestModel is a Unicode, wide-tier model with a frozen clock the
// tests advance explicitly.
func waitTraceTestModel(t *testing.T, base time.Time) *Model {
	t.Helper()
	m := newTestModel(t)
	m.model = "qwen3.5:0.8b"
	m.now = func() time.Time { return base }
	// Settle the model-identity observation so later transitions are measured
	// against a stable baseline record.
	m.observeWaitTrace()
	return m
}

func TestWaitTraceRecordsAResponseThroughUpdate(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	m := waitTraceTestModel(t, base)

	m.state = StateWaiting
	updated, _ := m.Update(struct{}{})
	m = updated.(*Model)
	wait := m.chromeSpring.wait
	if !wait.inWait || !wait.waitStart.Equal(base) {
		t.Fatalf("waiting entry not observed: %+v", wait)
	}

	m.now = func() time.Time { return base.Add(1200 * time.Millisecond) }
	m.state = StateStreaming
	updated, _ = m.Update(struct{}{})
	m = updated.(*Model)
	wait = m.chromeSpring.wait
	if wait.inWait || wait.samples != 1 || wait.baseline != 1200*time.Millisecond ||
		wait.last != 1200*time.Millisecond {
		t.Fatalf("first response not recorded: %+v", wait)
	}
}

func TestWaitTraceBaselineIsAnEMAAndDiscardsUnansweredWaits(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	m := waitTraceTestModel(t, base)

	record := func(wait time.Duration, answered bool) {
		start := m.nowTime()
		m.state = StateWaiting
		m.observeWaitTrace()
		m.now = func() time.Time { return start.Add(wait) }
		if answered {
			m.state = StateStreaming
		} else {
			m.state = StateIdle
		}
		m.observeWaitTrace()
		m.state = StateIdle
		m.observeWaitTrace()
	}

	record(2*time.Second, true)
	record(1*time.Second, true)
	wait := m.chromeSpring.wait
	// EMA with newest-quarter weight: (2s*3 + 1s) / 4 = 1.75s.
	if wait.samples != 2 || wait.baseline != 1750*time.Millisecond {
		t.Fatalf("EMA baseline = %+v, want samples=2 baseline=1.75s", wait)
	}

	// A cancelled wait was never answered; it must not move the baseline.
	record(30*time.Second, false)
	wait = m.chromeSpring.wait
	if wait.samples != 2 || wait.baseline != 1750*time.Millisecond {
		t.Fatalf("unanswered wait moved the baseline: %+v", wait)
	}
}

// Two local models of different weight classes have nothing to say about each
// other's latency, so a switch must discard what came before rather than
// converge away from it.
func TestWaitTraceBaselineResetsOnModelSwitch(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	m := waitTraceTestModel(t, base)
	m.chromeSpring.wait.baseline = 2 * time.Second
	m.chromeSpring.wait.last = 2 * time.Second
	m.chromeSpring.wait.samples = 3

	m.model = "gemma4:12b"
	m.observeWaitTrace()
	wait := m.chromeSpring.wait
	if wait.samples != 0 || wait.baseline != 0 || wait.lastModel != "gemma4:12b" {
		t.Fatalf("model switch did not reset the baseline: %+v", wait)
	}
}

func TestWaitTraceHeadMapsElapsedAgainstBaseline(t *testing.T) {
	baseline := 2 * time.Second
	tests := []struct {
		elapsed time.Duration
		head    int
		overdue bool
	}{
		{elapsed: 0, head: 0, overdue: false},
		{elapsed: 500 * time.Millisecond, head: 0, overdue: false},
		// At exactly the baseline the head sits on the expected-reply marker.
		{elapsed: 2 * time.Second, head: 3, overdue: false},
		{elapsed: 3 * time.Second, head: 4, overdue: false},
		// Late: pinned at the last cell, not yet the 2x warning.
		{elapsed: 3900 * time.Millisecond, head: 5, overdue: false},
		{elapsed: 4 * time.Second, head: 5, overdue: true},
		{elapsed: time.Minute, head: 5, overdue: true},
	}
	for _, tt := range tests {
		head, overdue := waitTraceHead(tt.elapsed, baseline, waitTraceCells)
		if head != tt.head || overdue != tt.overdue {
			t.Fatalf("waitTraceHead(%v) = (%d, %v), want (%d, %v)",
				tt.elapsed, head, overdue, tt.head, tt.overdue)
		}
	}
}

func TestWaitTraceRendersHeadMarkerAndTrack(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	m := waitTraceTestModel(t, base)
	m.state = StateWaiting
	m.turnStartedAt = base.Add(-500 * time.Millisecond)
	m.chromeSpring.wait.inWait = true
	m.chromeSpring.wait.waitStart = base.Add(-500 * time.Millisecond)
	m.chromeSpring.wait.baseline = 2 * time.Second
	m.chromeSpring.wait.last = 2 * time.Second
	m.chromeSpring.wait.samples = 1

	trace := ansi.Strip(m.renderWaitTrace(waitTraceCells))
	if trace != "●··│··" {
		t.Fatalf("early trace = %q, want %q", trace, "●··│··")
	}

	// At the baseline the head covers the marker.
	m.chromeSpring.wait.waitStart = base.Add(-2 * time.Second)
	trace = ansi.Strip(m.renderWaitTrace(waitTraceCells))
	if trace != "···●··" {
		t.Fatalf("on-time trace = %q, want %q", trace, "···●··")
	}

	// Overdue: head pinned at the right edge, marker visible again.
	m.chromeSpring.wait.waitStart = base.Add(-10 * time.Second)
	trace = ansi.Strip(m.renderWaitTrace(waitTraceCells))
	if trace != "···│·●" {
		t.Fatalf("overdue trace = %q, want %q", trace, "···│·●")
	}

	// The trace appears in the activity rail at the wide tier.
	line := ansi.Strip(m.renderWorkingLine())
	if !strings.Contains(line, "···│·●") || !strings.Contains(line, "Running") {
		t.Fatalf("working line missing the wait trace:\n%s", line)
	}
}

func TestWaitTraceStaysHonestWithoutABaseline(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	m := waitTraceTestModel(t, base)
	m.state = StateWaiting
	m.chromeSpring.wait.inWait = true
	m.chromeSpring.wait.waitStart = base.Add(-time.Second)

	if got := m.renderWaitTrace(waitTraceCells); got != "" {
		t.Fatalf("no-baseline trace = %q, want empty (scramble keeps the first wait)", got)
	}
	// Narrow tier: one motion cell cannot encode a position.
	m.chromeSpring.wait.baseline = 2 * time.Second
	m.chromeSpring.wait.samples = 1
	if got := m.renderWaitTrace(1); got != "" {
		t.Fatalf("narrow trace = %q, want empty", got)
	}
	// ASCII profile: the waiting phase owns the spinner, not the trace.
	m.glyphProfile = GlyphASCII
	if got := m.renderWaitTrace(waitTraceCells); got != "" {
		t.Fatalf("ASCII trace = %q, want empty", got)
	}
}

func TestRuntimeStatusShowsResponseReceiptOnlyAfterAFirstResponse(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	m := waitTraceTestModel(t, base)

	before := ansi.Strip(m.buildRuntimeStatusContent(60))
	if strings.Contains(before, "Response") {
		t.Fatalf("runtime shows a response receipt before any first response:\n%s", before)
	}

	m.chromeSpring.wait.last = 1200 * time.Millisecond
	m.chromeSpring.wait.baseline = 1500 * time.Millisecond
	m.chromeSpring.wait.samples = 2
	after := ansi.Strip(m.buildRuntimeStatusContent(60))
	if !strings.Contains(after, "Response") ||
		!strings.Contains(after, "last 1.2s") || !strings.Contains(after, "typical 1.5s") {
		t.Fatalf("runtime response receipt missing latency facts:\n%s", after)
	}
}

// TestWaitTraceReducedMotionSchedulesNoTicksAndRendersStaticFrame is the
// reduced-motion contract: no ticks from the observation hook, no animation
// clock ownership, and the frame is today's correct static form — ellipsis
// motion cell, live label, cancellation affordance — with no trace glyphs
// frozen mid-track.
func TestWaitTraceReducedMotionSchedulesNoTicksAndRendersStaticFrame(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	m := waitTraceTestModel(t, base)
	m.reducedMotion = true
	m.state = StateWaiting
	m.turnStartedAt = base.Add(-3 * time.Second)
	m.chromeSpring.wait = waitTraceState{
		inWait:    true,
		waitStart: base.Add(-3 * time.Second),
		baseline:  1500 * time.Millisecond,
		last:      1500 * time.Millisecond,
		samples:   2,
		lastModel: m.model,
	}

	if got := m.renderWaitTrace(waitTraceCells); got != "" {
		t.Fatalf("reduced-motion trace = %q, want empty", got)
	}
	if m.needsScramble() || m.needsSpinner() {
		t.Fatal("reduced motion must not own an animation clock while waiting")
	}
	if cmd := m.maybeKickChromeSpring(); cmd != nil {
		t.Fatal("reduced-motion wait observation scheduled a chrome spring tick")
	}

	first := ansi.Strip(m.renderWorkingLine())
	second := ansi.Strip(m.renderWorkingLine())
	if first != second {
		t.Fatalf("reduced-motion frame is not static:\n%q\n%q", first, second)
	}
	if !strings.Contains(first, "Running") || !strings.Contains(first, "esc") {
		t.Fatalf("reduced-motion frame lost the working grammar:\n%s", first)
	}
	if !strings.Contains(first, "…") {
		t.Fatalf("reduced-motion frame lost the static ellipsis motion cell:\n%s", first)
	}
	if strings.Contains(first, "●") || strings.Contains(first, "│") {
		t.Fatalf("reduced-motion frame contains frozen trace glyphs:\n%s", first)
	}
}

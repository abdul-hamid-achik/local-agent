package ui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// Wait trace: StateWaiting means the request reached Ollama and nothing has
// come back yet — the first stream chunk or tool call flips the state to
// streaming, exactly once per segment.
//
// That phase is where a local harness is least legible. Ollama loads a model
// from disk on first use, so the same prompt against the same model can take
// thirty seconds cold and under a second warm, and the elapsed counter alone
// cannot tell those apart from a wedged daemon. The waiting cells in the
// activity rail used to spend that phase on random braille shimmer. They now
// encode how far the current wait has progressed against this model's own
// typical first response: a head travels a fixed track and the expected-reply
// marker sits at the midpoint.
//
// The comparison is per model and deliberately so. A 0.8b model answering in
// 300ms and a 20b model answering in 8s are both healthy; only a model
// measured against itself says anything.
//
// Discipline: this file adds no clocks. Observation rides the per-update
// maybeKickChromeSpring tail hook and never schedules ticks; rendering rides
// the waiting phase's existing scramble tick, which remains that phase's one
// clock owner. Under reducedMotion the trace renders nothing, leaving the
// static ellipsis frame exactly as it is today, and the same fact stays
// readable without motion in the /runtime Response row.
const (
	// waitTraceCells is the fixed trace width. It matches the six motion
	// cells the waiting scramble already owns at the wide tier, so adopting
	// the trace can never change layout geometry.
	waitTraceCells = 6
	// waitTraceBaselineFloor bounds the render timeline. A sub-250ms baseline
	// would saturate the track instantly and flag Warning on ordinary jitter;
	// the floor only widens the displayed timeline — the recorded EMA keeps
	// the true value.
	waitTraceBaselineFloor = 250 * time.Millisecond
)

// waitTraceState tracks one in-flight wait and the per-model first-response
// baseline. It lives inside chromeSpringState because Model already owns that
// struct and its values are presentation-only; no Model field or Update case
// is added for it.
type waitTraceState struct {
	// inWait/waitStart describe the current outstanding request, stamped when
	// the model enters StateWaiting.
	inWait    bool
	waitStart time.Time

	// baseline is an EMA (newest quarter weight) of completed waits; last is
	// the most recent sample. Both reset when the model identity changes —
	// a different model answers with a different latency profile, and on a
	// local host a different weight class entirely.
	baseline  time.Duration
	last      time.Duration
	samples   int
	lastModel string
}

// observeWaitTrace watches state transitions from the per-update chrome
// spring hook. It only records; it never schedules ticks, in any mode.
func (m *Model) observeWaitTrace() {
	if m == nil {
		return
	}
	s := &m.chromeSpring.wait
	if m.model != s.lastModel {
		// A stale baseline from another model would misplace the
		// expected-reply marker for every wait until it converged, and
		// between two local models of different sizes it would not converge
		// to anything meaningful at all.
		s.lastModel = m.model
		s.baseline = 0
		s.last = 0
		s.samples = 0
	}
	waiting := m.state == StateWaiting
	switch {
	case waiting && !s.inWait:
		s.inWait = true
		s.waitStart = m.nowTime()
	case !waiting && s.inWait:
		s.inWait = false
		if m.state != StateStreaming || s.waitStart.IsZero() {
			// A wait that ended without a reply (cancel, error, shutdown)
			// is not a latency sample; recording it would poison the
			// baseline with the user's patience instead of the model's
			// response time.
			return
		}
		sample := m.nowTime().Sub(s.waitStart)
		if sample <= 0 {
			return
		}
		s.last = sample
		if s.samples == 0 {
			s.baseline = sample
		} else {
			s.baseline = (s.baseline*3 + sample) / 4
		}
		s.samples++
	}
}

// waitTraceHead maps an elapsed wait onto the trace. The expected-reply
// marker sits at cells/2, so elapsed==baseline puts the head exactly on the
// marker; the head pins at the last cell once the reply is late and overdue
// reports the ≥2× threshold where the head adopts the Warning role.
func waitTraceHead(elapsed, baseline time.Duration, cells int) (head int, overdue bool) {
	if baseline < waitTraceBaselineFloor {
		baseline = waitTraceBaselineFloor
	}
	if cells < 2 || elapsed <= 0 {
		return 0, false
	}
	marker := cells / 2
	head = int(float64(elapsed) / float64(baseline) * float64(marker))
	if head >= cells {
		head = cells - 1
	}
	return head, elapsed >= 2*baseline
}

// renderWaitTrace renders the waiting-phase trace, or "" whenever it has
// nothing honest to say: no completed reply yet for this model (the first
// wait keeps the existing shimmer), the narrow one-cell tier, the ASCII
// profile (whose waiting phase owns the spinner), or reducedMotion (whose
// correct static frame is the existing ellipsis — a frozen mid-track head
// would read as a live measurement that never updates).
func (m *Model) renderWaitTrace(cells int) string {
	if m == nil || m.reducedMotion || m.glyphProfile == GlyphASCII ||
		cells < waitTraceCells || m.state != StateWaiting {
		return ""
	}
	s := m.chromeSpring.wait
	if s.samples == 0 || !s.inWait || s.waitStart.IsZero() {
		return ""
	}
	head, overdue := waitTraceHead(m.nowTime().Sub(s.waitStart), s.baseline, waitTraceCells)

	palette := outputSemanticPalette(m.isDark, m.themeID)
	trackStyle := lipgloss.NewStyle().Foreground(palette.Dim)
	markerStyle := lipgloss.NewStyle().Foreground(palette.Border)
	headStyle := lipgloss.NewStyle().Foreground(palette.Accent)
	if overdue {
		headStyle = lipgloss.NewStyle().Foreground(palette.Warning)
	}

	// Position carries the fact; color only reinforces it, so NO_COLOR and
	// monochrome terminals still read the trace correctly. Every glyph is one
	// cell wide: the head and marker reuse the shared vocabulary, and the
	// track reuses the middle dot the rail already uses as its separator.
	glyphs := glyphSet(m.glyphProfile)
	marker := waitTraceCells / 2
	var b strings.Builder
	for cell := 0; cell < waitTraceCells; cell++ {
		switch {
		case cell == head:
			b.WriteString(headStyle.Render(glyphs.Selected))
		case cell == marker:
			b.WriteString(markerStyle.Render(glyphs.Vertical))
		default:
			b.WriteString(trackStyle.Render("·"))
		}
	}
	return b.String()
}

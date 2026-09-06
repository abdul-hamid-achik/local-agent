package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/harmonica"
)

// Context meter motion. Harmonica drives natural
// ease; reducedMotion snaps to targets and never schedules ticks. Layout stays
// stable — we never animate reserved row counts mid-flight.
const (
	chromeSpringFPS      = 30.0
	chromeSpringFrame    = time.Second / 30
	chromeSpringSettled  = 0.008
	chromeSpringVelocity = 0.02
)

type chromeSpringTickMsg struct {
	Token uint64
}

// chromeSpringState is owned by Model; values are presentation-only.
type chromeSpringState struct {
	spring harmonica.Spring

	// Context meter display percent 0→100 (style still uses true occupancy).
	ctxPos, ctxVel, ctxTarget float64
	ctxInited                 bool

	token   uint64
	pending bool

	// wait is the waiting-phase trace record (wait_trace.go). It lives here
	// because Model already owns this presentation-only struct; it drives no
	// spring and schedules no ticks of its own.
	wait waitTraceState
}

func newChromeSpringState() chromeSpringState {
	// Slightly soft spring — calm chrome, not bouncy toys.
	return chromeSpringState{
		spring: harmonica.NewSpring(harmonica.FPS(chromeSpringFPS), 8.0, 0.55),
	}
}

func springSettled(pos, vel, target float64) bool {
	return math.Abs(pos-target) < chromeSpringSettled && math.Abs(vel) < chromeSpringVelocity
}

func (m *Model) chromeSpringActive() bool {
	if m == nil || m.reducedMotion || !m.ready {
		return false
	}
	s := &m.chromeSpring
	return !springSettled(s.ctxPos, s.ctxVel, s.ctxTarget)
}

// pullChromeSpringTargets syncs spring targets from model state. When reduced
// motion is on, positions snap so the next paint is final.
func (m *Model) pullChromeSpringTargets() {
	if m == nil {
		return
	}
	s := &m.chromeSpring

	// Context meter target from true occupancy.
	if m.numCtx > 0 {
		used := max(0, m.promptTokens)
		pct := float64(used*100) / float64(m.numCtx)
		if pct > 100 {
			pct = 100
		}
		if !s.ctxInited {
			s.ctxPos = pct
			s.ctxVel = 0
			s.ctxInited = true
		}
		s.ctxTarget = pct
	} else {
		s.ctxTarget = 0
		if m.reducedMotion || !s.ctxInited {
			s.ctxPos = 0
			s.ctxVel = 0
		}
	}

	if m.reducedMotion {
		s.ctxPos = s.ctxTarget
		s.ctxVel = 0
	}
}

func (m *Model) stepChromeSpring() {
	if m == nil || m.reducedMotion {
		return
	}
	s := &m.chromeSpring
	s.ctxPos, s.ctxVel = s.spring.Update(s.ctxPos, s.ctxVel, s.ctxTarget)
	// Clamp to sensible ranges.
	if s.ctxPos < 0 {
		s.ctxPos = 0
	}
	if s.ctxPos > 100 {
		s.ctxPos = 100
	}
}

func (m *Model) startChromeSpringTick() tea.Cmd {
	if m == nil || m.reducedMotion || m.chromeSpring.pending || !m.chromeSpringActive() {
		return nil
	}
	m.chromeSpring.token++
	token := m.chromeSpring.token
	m.chromeSpring.pending = true
	return tea.Tick(chromeSpringFrame, func(time.Time) tea.Msg {
		return chromeSpringTickMsg{Token: token}
	})
}

// maybeKickChromeSpring updates targets and schedules a frame if motion remains.
func (m *Model) maybeKickChromeSpring() tea.Cmd {
	if m == nil {
		return nil
	}
	// Wait-trace observation rides the same per-update hook. It only records
	// state transitions; it never schedules ticks, in any motion mode.
	m.observeWaitTrace()
	if !m.ready {
		return nil
	}
	m.pullChromeSpringTargets()
	return m.startChromeSpringTick()
}

func (m *Model) handleChromeSpringTick(msg chromeSpringTickMsg) tea.Cmd {
	if m == nil || !m.chromeSpring.pending || msg.Token != m.chromeSpring.token {
		return nil
	}
	m.chromeSpring.pending = false
	if m.reducedMotion {
		m.pullChromeSpringTargets()
		return nil
	}
	m.pullChromeSpringTargets()
	m.stepChromeSpring()
	if !m.chromeSpringActive() {
		// Snap to final targets for clean paint.
		m.chromeSpring.ctxPos = m.chromeSpring.ctxTarget
		m.chromeSpring.ctxVel = 0
		return nil
	}
	return m.startChromeSpringTick()
}

// settleChromeSpringForTest snaps springs to targets for deterministic paint
// assertions (no Bubble Tea tick loop).
func (m *Model) settleChromeSpringForTest() {
	if m == nil {
		return
	}
	m.pullChromeSpringTargets()
	m.chromeSpring.ctxPos = m.chromeSpring.ctxTarget
	m.chromeSpring.ctxVel = 0
	m.chromeSpring.pending = false
}

// displayContextPercent returns the spring-smoothed meter value for paint.
func (m *Model) displayContextPercent() int {
	if m == nil || m.numCtx <= 0 {
		return 0
	}
	if m.reducedMotion || !m.chromeSpring.ctxInited {
		used := max(0, m.promptTokens)
		pct := used * 100 / m.numCtx
		if pct > 100 {
			return 100
		}
		return pct
	}
	pct := int(math.Round(m.chromeSpring.ctxPos))
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// formatContextStatusWithPercent paints used/limit · N% using a display percent
// that may be spring-smoothed while style uses true occupancy for warnings.
func (m *Model) formatContextStatusWithPercent(displayPct int) string {
	if m == nil || m.numCtx <= 0 {
		return ""
	}
	used := max(0, m.promptTokens)
	truePct := used * 100 / m.numCtx
	if truePct > 100 {
		truePct = 100
	}
	style := m.styles.ContextPctLow
	if truePct >= 85 {
		style = m.styles.ContextPctHigh
	} else if truePct >= 65 {
		style = m.styles.ContextPctMid
	}
	sep := strings.TrimSpace(glyphSeparator(m.glyphProfile))
	if sep == "" {
		sep = "·"
	}
	if displayPct < 0 {
		displayPct = 0
	}
	if displayPct > 100 {
		displayPct = 100
	}
	return style.Render(fmt.Sprintf("%s/%s %s %d%%", formatTokens(used), formatTokens(m.numCtx), sep, displayPct))
}

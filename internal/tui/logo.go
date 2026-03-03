package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
)

// Logo animation phases
const (
	LogoPhaseHidden = iota
	LogoPhaseAnimating
	LogoPhaseVisible
	LogoPhaseDone
)

// LogoTickMsg triggers animation frame
type LogoTickMsg struct{}

// LogoModel holds the state for the animated logo
type LogoModel struct {
	phase       int
	alpha       float64
	vel         float64
	spring      harmonica.Spring
	isDark      bool
	frame       int
	displayLogo bool
}

// logoLines returns the ASCII art logo.
func logoLines() []string {
	return []string{
		``,
		` ╦  ╔═╗╔═╗╔═╗╦    ╔═╗╔═╗╔═╗╔╗╔╔╦╗`,
		` ║  ║ ║║  ╠═╣║    ╠═╣║ ╦║╣ ║║║ ║ `,
		` ╩═╝╚═╝╚═╝╩ ╩╩═╝  ╩ ╩╚═╝╚═╝╝╚╝ ╩ `,
		``,
		`  100% local · Your data never leaves`,
		``,
	}
}

// NewLogoModel creates a new animated logo
func NewLogoModel(isDark bool) LogoModel {
	return LogoModel{
		spring: harmonica.NewSpring(harmonica.FPS(60), 4.0, 0.9),
		isDark: isDark,
		phase:  LogoPhaseHidden,
	}
}

// Start begins the logo animation
func (m *LogoModel) Start() {
	m.phase = LogoPhaseAnimating
	m.alpha = 0
	m.vel = 0
	m.frame = 0
}

// Init starts the animation
func (m LogoModel) Init() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
		return LogoTickMsg{}
	})
}

// Update processes animation frames
func (m LogoModel) Update(msg tea.Msg) (LogoModel, tea.Cmd) {
	if _, ok := msg.(LogoTickMsg); ok {
		m.frame++

		// Animate alpha from 0 to 1
		if m.phase == LogoPhaseAnimating {
			target := 1.0
			m.alpha, m.vel = m.spring.Update(m.alpha, m.vel, target)

			if m.alpha >= 0.95 && m.frame > 60 {
				m.phase = LogoPhaseVisible
				m.displayLogo = true
			}

			// Show for a few seconds then hide
			if m.phase == LogoPhaseVisible && m.frame > 180 {
				m.phase = LogoPhaseDone
				m.displayLogo = false
			}
		}

		if m.phase < LogoPhaseDone {
			return m, tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
				return LogoTickMsg{}
			})
		}
	}
	return m, nil
}

// View renders the logo with fade effect
func (m LogoModel) View() string {
	if m.phase == LogoPhaseHidden || m.phase == LogoPhaseDone || !m.displayLogo {
		return ""
	}

	lines := logoLines()
	var b strings.Builder

	for _, line := range lines {
		if m.alpha < 0.1 {
			// Very dim during fade in
			b.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#4c566a")).
				Render(line))
		} else if m.alpha < 0.5 {
			// Medium brightness
			b.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#81a1c1")).
				Render(line))
		} else {
			// Full brightness with gradient
			b.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#88c0d0")).
				Bold(true).
				Render(line))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// IsDone returns true when animation is complete
func (m LogoModel) IsDone() bool {
	return m.phase == LogoPhaseDone
}

// ShouldShow returns true if logo should be displayed
func (m LogoModel) ShouldShow() bool {
	return m.displayLogo && m.phase < LogoPhaseDone
}

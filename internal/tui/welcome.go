package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
)

// Welcome animation phases
const (
	WelcomePhaseLogo = iota
	WelcomePhaseTagline
	WelcomePhaseFeatures
	WelcomePhaseReady
)

// WelcomeTickMsg triggers animation frame
type WelcomeTickMsg struct{}

// WelcomeModel holds the state for the welcome animation
type WelcomeModel struct {
	phase        int
	logoAlpha    float64
	logoVel      float64
	taglineAlpha float64
	taglineVel   float64
	featureIndex int
	featureAlpha float64
	featureVel   float64
	spring       harmonica.Spring
	spinner      spinner.Model
	isDark       bool
	ready        bool
	frame        int
}

// taglines for rotation
var taglines = []string{
	`ASK  →  PLAN  →  BUILD`,
	`0.8B    4B       9B`,
	`Small models · Big results`,
}

// featureList shows key features
var featureList = []struct {
	icon  string
	label string
	desc  string
}{
	{"◈", "Model Routing", "Auto-selects 0.8B → 9B based on task"},
	{"◈", "MCP Native", "Connect any tool via Model Context Protocol"},
	{"◈", "ICE Engine", "Cross-session memory & context"},
	{"◈", "Auto-Memory", "Extracts facts, decisions, TODOs"},
	{"◈", "Thinking/CoT", "Chain-of-thought reasoning display"},
	{"◈", "Skills System", "Domain-specific knowledge injection"},
}

// NewWelcomeModel creates a new welcome animation model
func NewWelcomeModel(isDark bool) WelcomeModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0"))),
	)

	return WelcomeModel{
		spring:  harmonica.NewSpring(harmonica.FPS(60), 6.0, 0.8),
		spinner: s,
		isDark:  isDark,
	}
}

// Init starts the welcome animation
func (m WelcomeModel) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
			return WelcomeTickMsg{}
		}),
		m.spinner.Tick,
	)
}

// Update processes animation frames
func (m WelcomeModel) Update(msg tea.Msg) (WelcomeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case WelcomeTickMsg:
		m.frame++

		// Phase transitions - slowed down for better viewing
		if m.phase == WelcomePhaseLogo && m.logoAlpha >= 0.95 && m.frame > 60 {
			// Wait at least 60 frames (~1 second) on logo
			m.phase = WelcomePhaseTagline
		}
		if m.phase == WelcomePhaseTagline && m.taglineAlpha >= 0.95 && m.frame > 180 {
			// Wait at least 180 frames (~3 seconds) on taglines
			m.phase = WelcomePhaseFeatures
			m.featureIndex = 0
		}
		if m.phase == WelcomePhaseFeatures && m.featureIndex >= len(featureList) {
			m.phase = WelcomePhaseReady
			m.ready = true
		}

		// Animate based on phase
		switch m.phase {
		case WelcomePhaseLogo:
			target := 1.0
			m.logoAlpha, m.logoVel = m.spring.Update(m.logoAlpha, m.logoVel, target)

		case WelcomePhaseTagline:
			target := 1.0
			m.taglineAlpha, m.taglineVel = m.spring.Update(m.taglineAlpha, m.taglineVel, target)

		case WelcomePhaseFeatures:
			// Animate current feature in
			target := 1.0
			m.featureAlpha, m.featureVel = m.spring.Update(m.featureAlpha, m.featureVel, target)

			// Move to next feature after delay (slower - every 90 frames)
			if m.featureAlpha >= 0.95 && m.frame%90 == 0 {
				m.featureIndex++
				if m.featureIndex < len(featureList) {
					m.featureAlpha = 0
					m.featureVel = 0
				}
			}
		}

		// Update spinner
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)

		return m, tea.Batch(
			tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
				return WelcomeTickMsg{}
			}),
			cmd,
		)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

// View renders the welcome animation
func (m WelcomeModel) View() string {
	var b strings.Builder

	// Render logo with fade-in
	if m.phase >= WelcomePhaseLogo {
		logo := logoLines()
		for i, line := range logo {
			if m.phase == WelcomePhaseLogo && i < len(logo)-2 {
				// Apply gradient fade-in effect during logo phase
				alpha := m.logoAlpha
				if alpha < 0.1 {
					alpha = 0.1
				}
				b.WriteString(m.applyFade(line, alpha))
			} else {
				b.WriteString(m.renderLogoLine(line))
			}
			b.WriteString("\n")
		}
	}

	// Render animated tagline
	if m.phase >= WelcomePhaseTagline {
		b.WriteString("\n")
		taglineIdx := (m.frame / 120) % len(taglines)
		tagline := taglines[taglineIdx]
		b.WriteString(m.renderTagline(tagline))
		b.WriteString("\n")
	}

	// Render feature list with animation
	if m.phase >= WelcomePhaseFeatures {
		b.WriteString("\n")
		featureTitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4c566a")).
			Bold(true).
			Render("  Features")
		b.WriteString(featureTitle)
		b.WriteString("\n\n")

		// Show all features, highlight current one
		for i, feat := range featureList {
			if i <= m.featureIndex {
				line := fmt.Sprintf("  %s  %s — %s", feat.icon, feat.label, feat.desc)

				if i == m.featureIndex && m.featureAlpha < 0.95 {
					// Currently animating in
					alpha := m.featureAlpha
					if alpha < 0.2 {
						alpha = 0.2
					}
					b.WriteString(m.applyFade(line, alpha))
				} else if i == m.featureIndex {
					// Current feature with accent
					indicator := lipgloss.NewStyle().
						Foreground(lipgloss.Color("#88c0d0")).
						Render("▸ ")
					b.WriteString(indicator + line[2:])
				} else {
					// Previous features in dim
					dimStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color("#4c566a"))
					b.WriteString("  " + dimStyle.Render(line[2:]))
				}
				b.WriteString("\n")
			}
		}
	}

	// Render ready state
	if m.phase >= WelcomePhaseReady {
		b.WriteString("\n")
		checkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c"))
		readyLine := fmt.Sprintf("  %s  Ready to go! Type a message or press ? for help", checkStyle.Render("✓"))
		b.WriteString(readyLine)
		b.WriteString("\n")
	}

	return b.String()
}

// renderLogoLine applies gradient colors to logo line
func (m WelcomeModel) renderLogoLine(line string) string {
	if noColor {
		return line
	}

	// Apply gradient to box drawing characters
	colors := []string{"#88c0d0", "#81a1c1", "#5e81ac", "#b48ead"}

	result := ""
	for i, r := range line {
		if r == '╭' || r == '─' || r == '╮' || r == '│' || r == '╰' || r == '╯' {
			colorIdx := i % len(colors)
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[colorIdx]))
			result += style.Render(string(r))
		} else if r == '╔' || r == '╗' || r == '║' || r == '═' || r == '╚' || r == '╝' {
			colorIdx := (i + 1) % len(colors)
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[colorIdx]))
			result += style.Render(string(r))
		} else {
			result += string(r)
		}
	}

	return result
}

// renderTagline renders the tagline with animation
func (m WelcomeModel) renderTagline(tagline string) string {
	if noColor {
		return "  " + tagline
	}

	// Split tagline into parts and apply gradient
	parts := strings.Split(tagline, " ")
	result := "  "

	for i, part := range parts {
		colorIdx := i % 3
		var color string
		switch colorIdx {
		case 0:
			color = "#88c0d0"
		case 1:
			color = "#81a1c1"
		case 2:
			color = "#b48ead"
		}

		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Bold(true)
		result += style.Render(part) + " "
	}

	return result
}

// applyFade applies alpha blending to simulate fade
func (m WelcomeModel) applyFade(line string, alpha float64) string {
	if noColor {
		return line
	}

	// Use dimmer color based on alpha
	baseColor := "#4c566a" // dim
	if alpha > 0.7 {
		baseColor = "#88c0d0" // bright
	} else if alpha > 0.4 {
		baseColor = "#5e81ac" // medium
	}

	style := lipgloss.NewStyle().Foreground(lipgloss.Color(baseColor))
	return style.Render(line)
}

// IsReady returns true when welcome animation is complete
func (m WelcomeModel) IsReady() bool {
	return m.ready
}

// pulseEffect creates a subtle pulse animation for status indicators
type PulseModel struct {
	alpha  float64
	vel    float64
	spring harmonica.Spring
	target float64
}

func NewPulseModel() PulseModel {
	return PulseModel{
		spring: harmonica.NewSpring(harmonica.FPS(60), 3.0, 0.6),
		target: 1.0,
	}
}

type PulseTickMsg struct{}

func (m PulseModel) Init() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return PulseTickMsg{}
	})
}

func (m PulseModel) Update(msg tea.Msg) (PulseModel, tea.Cmd) {
	if _, ok := msg.(PulseTickMsg); ok {
		// Oscillate between 0.7 and 1.0
		if m.target == 1.0 && m.alpha >= 0.95 {
			m.target = 0.7
		} else if m.target == 0.7 && m.alpha <= 0.75 {
			m.target = 1.0
		}

		m.alpha, m.vel = m.spring.Update(m.alpha, m.vel, m.target)
		return m, tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
			return PulseTickMsg{}
		})
	}
	return m, nil
}

func (m PulseModel) Alpha() float64 {
	return m.alpha
}

// gradientText applies a horizontal gradient to text
func gradientText(text string, colors []string) string {
	if noColor || len(colors) == 0 {
		return text
	}

	result := ""
	runes := []rune(text)
	colorCount := len(colors)

	for i, r := range runes {
		colorIdx := int(float64(i) / float64(len(runes)) * float64(colorCount))
		if colorIdx >= colorCount {
			colorIdx = colorCount - 1
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[colorIdx]))
		result += style.Render(string(r))
	}

	return result
}

// slideInEffect creates a slide-in animation from left
type SlideInModel struct {
	offset float64
	vel    float64
	spring harmonica.Spring
	target float64
}

func NewSlideInModel() SlideInModel {
	return SlideInModel{
		spring: harmonica.NewSpring(harmonica.FPS(60), 5.0, 0.7),
		target: 0,
		offset: -50, // Start off-screen left
	}
}

type SlideInTickMsg struct{}

func (m SlideInModel) Init() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
		return SlideInTickMsg{}
	})
}

func (m SlideInModel) Update(msg tea.Msg) (SlideInModel, tea.Cmd) {
	if _, ok := msg.(SlideInTickMsg); ok {
		m.offset, m.vel = m.spring.Update(m.offset, m.vel, m.target)
		return m, tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
			return SlideInTickMsg{}
		})
	}
	return m, nil
}

func (m SlideInModel) Offset() int {
	return int(math.Max(0, m.offset))
}

func (m SlideInModel) IsComplete() bool {
	return m.offset <= 0.5
}

package tui

import (
	"math/rand"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

// scrambleChars is the character set for the scramble animation.
const scrambleChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*"

// scrambleWidth is the number of characters in the animation.
const scrambleWidth = 12

// ScrambleTickMsg triggers the next animation frame.
type ScrambleTickMsg struct {
	ID int
}

// ScrambleModel is a custom BubbleTea component that renders a gradient
// character scramble animation, inspired by Charmbracelet's Crush CLI.
type ScrambleModel struct {
	id        int
	chars     []rune
	visible   int
	colorFrom colorful.Color
	colorTo   colorful.Color
	isDark    bool
	rng       *rand.Rand
}

// NewScrambleModel creates a new scramble animation with theme-appropriate colors.
func NewScrambleModel(isDark bool) ScrambleModel {
	s := ScrambleModel{
		id:    1,
		chars: make([]rune, scrambleWidth),
		rng:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	s.SetDark(isDark)
	s.randomizeChars()
	return s
}

// SetDark updates the gradient colors for the current theme.
func (s *ScrambleModel) SetDark(isDark bool) {
	s.isDark = isDark
	if isDark {
		// Dark theme: cool blue → warm purple gradient
		s.colorFrom, _ = colorful.Hex("#88c0d0") // Nord frost
		s.colorTo, _ = colorful.Hex("#b48ead")   // Nord purple
	} else {
		// Light theme: teal → indigo
		s.colorFrom, _ = colorful.Hex("#0088bb")
		s.colorTo, _ = colorful.Hex("#6644aa")
	}
}

// Reset resets the animation (new ID + zero visible). Call when agent starts.
func (s *ScrambleModel) Reset() {
	s.id++
	s.visible = 0
	s.randomizeChars()
}

// Tick schedules the next animation frame (~15 FPS = 66ms).
func (s ScrambleModel) Tick() tea.Cmd {
	id := s.id
	return tea.Tick(66*time.Millisecond, func(time.Time) tea.Msg {
		return ScrambleTickMsg{ID: id}
	})
}

// Update processes tick messages and advances the animation.
func (s ScrambleModel) Update(msg tea.Msg) (ScrambleModel, tea.Cmd) {
	if tick, ok := msg.(ScrambleTickMsg); ok {
		if tick.ID != s.id {
			return s, nil // stale tick, ignore
		}
		s.randomizeChars()
		if s.visible < scrambleWidth {
			s.visible++
		}
		return s, s.Tick()
	}
	return s, nil
}

// View renders the visible characters with an HCL gradient.
func (s ScrambleModel) View() string {
	if s.visible == 0 {
		return ""
	}

	// NO_COLOR fallback
	if noColor {
		dots := ""
		for i := 0; i < s.visible && i < scrambleWidth; i++ {
			dots += "."
		}
		return dots
	}

	result := ""
	for i := 0; i < s.visible && i < len(s.chars); i++ {
		// Calculate gradient position
		t := float64(i) / float64(scrambleWidth-1)
		c := s.colorFrom.BlendHcl(s.colorTo, t).Clamped()
		hex := c.Hex()

		style := lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
		result += style.Render(string(s.chars[i]))
	}
	return result
}

// randomizeChars fills the chars slice with random characters.
func (s *ScrambleModel) randomizeChars() {
	runes := []rune(scrambleChars)
	for i := range s.chars {
		s.chars[i] = runes[s.rng.Intn(len(runes))]
	}
}

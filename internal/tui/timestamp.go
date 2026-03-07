package tui

import (
	"time"

	"charm.land/lipgloss/v2"
)

// TimestampConfig holds configuration for message timestamps.
type TimestampConfig struct {
	Enabled    bool
	Format     string // "time", "relative", "both"
	Position   string // "left", "right"
	MaxAge     time.Duration // For relative timestamps
}

// DefaultTimestampConfig returns default configuration.
func DefaultTimestampConfig() TimestampConfig {
	return TimestampConfig{
		Enabled:  false,
		Format:   "time",
		Position: "left",
		MaxAge:   24 * time.Hour,
	}
}

// TimestampStyles holds styling for timestamps.
type TimestampStyles struct {
	Time      lipgloss.Style
	Relative  lipgloss.Style
	Divider   lipgloss.Style
}

// DefaultTimestampStyles returns default styles.
func DefaultTimestampStyles(isDark bool) TimestampStyles {
	if isDark {
		return TimestampStyles{
			Time:     lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
			Relative: lipgloss.NewStyle().Foreground(lipgloss.Color("#5e81ac")),
			Divider:  lipgloss.NewStyle().Foreground(lipgloss.Color("#3b4252")),
		}
	}
	return TimestampStyles{
		Time:     lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
		Relative: lipgloss.NewStyle().Foreground(lipgloss.Color("#5e81ac")),
		Divider:  lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
	}
}

// TimestampHelper provides utilities for rendering timestamps.
type TimestampHelper struct {
	config  TimestampConfig
	styles  TimestampStyles
	nowFunc func() time.Time
}

// NewTimestampHelper creates a new timestamp helper.
func NewTimestampHelper(config TimestampConfig, isDark bool) *TimestampHelper {
	return &TimestampHelper{
		config:  config,
		styles:  DefaultTimestampStyles(isDark),
		nowFunc: time.Now,
	}
}

// SetDark updates theme.
func (th *TimestampHelper) SetDark(isDark bool) {
	th.styles = DefaultTimestampStyles(isDark)
}

// SetConfig updates the timestamp configuration.
func (th *TimestampHelper) SetConfig(config TimestampConfig) {
	th.config = config
}

// FormatTime formats a timestamp based on config.
func (th *TimestampHelper) FormatTime(t time.Time) string {
	if !th.config.Enabled {
		return ""
	}

	switch th.config.Format {
	case "time":
		return t.Format("15:04")
	case "relative":
		return th.relativeTime(t)
	case "both":
		return t.Format("15:04") + " " + th.relativeTime(t)
	default:
		return t.Format("15:04")
	}
}

// relativeTime returns a human-readable relative time string.
func (th *TimestampHelper) relativeTime(t time.Time) string {
	now := th.nowFunc()
	diff := now.Sub(t)

	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return formatInt(mins) + "m ago"
	}
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return formatInt(hours) + "h ago"
	}
	if diff < 7*24*time.Hour {
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return formatInt(days) + "d ago"
	}

	// Older dates - show date
	return t.Format("Jan 2")
}

// formatInt formats an integer without allocation.
func formatInt(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	// Simple implementation for common cases
	switch n {
	case 10:
		return "10"
	case 11:
		return "11"
	case 12:
		return "12"
	case 13:
		return "13"
	case 14:
		return "14"
	case 15:
		return "15"
	case 16:
		return "16"
	case 17:
		return "17"
	case 18:
		return "18"
	case 19:
		return "19"
	case 20:
		return "20"
	default:
		// Fallback for larger numbers
		if n < 100 {
			tens := n / 10
			ones := n % 10
			return string(rune('0'+tens)) + string(rune('0'+ones))
		}
		return string(rune('0' + n/100))
	}
}

// MessageTime stores the timestamp for a chat message.
type MessageTime struct {
	Time time.Time
}

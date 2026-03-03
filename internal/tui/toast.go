package tui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// ToastKind represents the type of toast notification.
type ToastKind int

const (
	ToastKindInfo ToastKind = iota
	ToastKindSuccess
	ToastKindWarning
	ToastKindError
)

// Toast represents a transient notification message.
type Toast struct {
	ID        int
	Kind      ToastKind
	Message   string
	CreatedAt time.Time
	Duration  time.Duration
	ExpiresAt time.Time
}

// ToastManager manages the lifecycle of toast notifications.
type ToastManager struct {
	toasts  []Toast
	nextID  int
	styles  ToastStyles
	maxToasts int
}

// ToastStyles holds styles for toast rendering.
type ToastStyles struct {
	Info    lipgloss.Style
	Success lipgloss.Style
	Warning lipgloss.Style
	Error   lipgloss.Style
	Border  lipgloss.Style
}

// NewToastManager creates a new toast manager.
func NewToastManager() *ToastManager {
	return &ToastManager{
		toasts:    make([]Toast, 0),
		nextID:    1,
		maxToasts: 3,
	}
}

// SetStyles applies styles to the manager.
func (tm *ToastManager) SetStyles(styles ToastStyles) {
	tm.styles = styles
}

// Add creates a new toast with the given message and duration.
func (tm *ToastManager) Add(kind ToastKind, message string, duration time.Duration) int {
	id := tm.nextID
	tm.nextID++

	toast := Toast{
		ID:        id,
		Kind:      kind,
		Message:   message,
		CreatedAt: time.Now(),
		Duration:  duration,
		ExpiresAt: time.Now().Add(duration),
	}

	tm.toasts = append(tm.toasts, toast)

	// Limit number of toasts
	if len(tm.toasts) > tm.maxToasts {
		tm.toasts = tm.toasts[1:]
	}

	return id
}

// Info adds an info toast.
func (tm *ToastManager) Info(message string) int {
	return tm.Add(ToastKindInfo, message, 3*time.Second)
}

// Success adds a success toast.
func (tm *ToastManager) Success(message string) int {
	return tm.Add(ToastKindSuccess, message, 3*time.Second)
}

// Warning adds a warning toast.
func (tm *ToastManager) Warning(message string) int {
	return tm.Add(ToastKindWarning, message, 5*time.Second)
}

// Error adds an error toast.
func (tm *ToastManager) Error(message string) int {
	return tm.Add(ToastKindError, message, 5*time.Second)
}

// Update removes expired toasts.
func (tm *ToastManager) Update() {
	now := time.Now()
	var active []Toast
	for _, t := range tm.toasts {
		if now.Before(t.ExpiresAt) {
			active = append(active, t)
		}
	}
	tm.toasts = active
}

// HasToasts returns true if there are active toasts.
func (tm *ToastManager) HasToasts() bool {
	return len(tm.toasts) > 0
}

// Render renders all active toasts as a single string.
func (tm *ToastManager) Render(width int) string {
	if len(tm.toasts) == 0 {
		return ""
	}

	var b strings.Builder
	for _, toast := range tm.toasts {
		b.WriteString(tm.renderToast(toast, width))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// renderToast renders a single toast.
func (tm *ToastManager) renderToast(toast Toast, width int) string {
	icon := "○"
	style := tm.styles.Info

	switch toast.Kind {
	case ToastKindSuccess:
		icon = "✓"
		style = tm.styles.Success
	case ToastKindWarning:
		icon = "⚠"
		style = tm.styles.Warning
	case ToastKindError:
		icon = "✗"
		style = tm.styles.Error
	}

	content := icon + " " + toast.Message
	
	// Apply style and truncate if needed
	maxW := width - 4
	if maxW < 20 {
		maxW = 20
	}
	
	rendered := style.Render(content)
	if lipgloss.Width(rendered) > maxW {
		rendered = style.Render(truncate(toast.Message, maxW-3))
	}

	return rendered
}

// DefaultToastStyles returns default styles for toasts based on theme.
func DefaultToastStyles(isDark bool) ToastStyles {
	ld := lipgloss.LightDark(isDark)

	colorInfo := ld(lipgloss.Color("#88c0d0"), lipgloss.Color("#5e81ac"))
	colorSuccess := ld(lipgloss.Color("#a3be8c"), lipgloss.Color("#8fbc8f"))
	colorWarning := ld(lipgloss.Color("#ebcb8b"), lipgloss.Color("#d08770"))
	colorError := ld(lipgloss.Color("#bf616a"), lipgloss.Color("#bf616a"))

	return ToastStyles{
		Info: lipgloss.NewStyle().Foreground(colorInfo),
		Success: lipgloss.NewStyle().Foreground(colorSuccess),
		Warning: lipgloss.NewStyle().Foreground(colorWarning),
		Error: lipgloss.NewStyle().Foreground(colorError),
	}
}

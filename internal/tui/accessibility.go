package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// AccessibilityHelper provides accessibility features like screen reader support.
type AccessibilityHelper struct {
	isDark      bool
	styles      AccessibilityStyles
	speakFunc   func(string) // Function to speak text (for screen readers)
	announceFunc func(string) // Function to announce changes
}

// AccessibilityStyles holds styling.
type AccessibilityStyles struct {
	Announce lipgloss.Style
}

// DefaultAccessibilityStyles returns default styles.
func DefaultAccessibilityStyles(isDark bool) AccessibilityStyles {
	return AccessibilityStyles{
		Announce: lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0")),
	}
}

// NewAccessibilityHelper creates a new accessibility helper.
func NewAccessibilityHelper(isDark bool) *AccessibilityHelper {
	return &AccessibilityHelper{
		isDark:  isDark,
		styles:  DefaultAccessibilityStyles(isDark),
	}
}

// SetDark updates theme.
func (ah *AccessibilityHelper) SetDark(isDark bool) {
	ah.isDark = isDark
	ah.styles = DefaultAccessibilityStyles(isDark)
}

// SetSpeakFunc sets the function to speak text.
func (ah *AccessibilityHelper) SetSpeakFunc(f func(string)) {
	ah.speakFunc = f
}

// SetAnnounceFunc sets the function to announce changes.
func (ah *AccessibilityHelper) SetAnnounceFunc(f func(string)) {
	ah.announceFunc = f
}

// Announce announces a message to the user.
func (ah *AccessibilityHelper) Announce(format string, args ...string) {
	if ah.announceFunc != nil {
		msg := format
		if len(args) > 0 {
			msg = fmt.Sprintf(format, args)
		}
		ah.announceFunc(msg)
	}
}

// Speak speaks text directly.
func (ah *AccessibilityHelper) Speak(text string) {
	if ah.speakFunc != nil {
		ah.speakFunc(text)
	}
}

// DescribeEntry creates an accessibility description for a chat entry.
func (ah *AccessibilityHelper) DescribeEntry(entry ChatEntry, index int, toolCount int) string {
	var desc strings.Builder

	switch entry.Kind {
	case "user":
		desc.WriteString("User message")
	case "assistant":
		desc.WriteString("Assistant response")
		if entry.ThinkingContent != "" {
			desc.WriteString(", has thinking")
		}
	case "tool_group":
		desc.WriteString("Tool execution")
		if index >= 0 && index < toolCount {
			desc.WriteString(", tool result")
		}
	case "system":
		desc.WriteString("System message")
	case "error":
		desc.WriteString("Error")
	}

	// Add content preview
	if entry.Content != "" {
		preview := truncateStr(entry.Content, 50)
		desc.WriteString(": ")
		desc.WriteString(preview)
	}

	return desc.String()
}

// DescribeState creates an accessibility description of the current state.
func (ah *AccessibilityHelper) DescribeState(state State, model, mode string) string {
	var desc string

	switch state {
	case StateIdle:
		desc = "Ready"
	case StateWaiting:
		desc = "Waiting for response"
	case StateStreaming:
		desc = "Receiving response"
	}

	if model != "" {
		desc += ", model: " + model
	}
	if mode != "" {
		desc += ", mode: " + mode
	}

	return desc
}

// DescribeOverlay creates an accessibility description of the current overlay.
func (ah *AccessibilityHelper) DescribeOverlay(overlay OverlayKind) string {
	switch overlay {
	case OverlayNone:
		return ""
	case OverlayHelp:
		return "Help overlay open"
	case OverlayCompletion:
		return "Completion menu open"
	case OverlayModelPicker:
		return "Model picker open"
	case OverlayPlanForm:
		return "Plan form open"
	case OverlaySessionsPicker:
		return "Sessions picker open"
	default:
		return "Overlay open"
	}
}

// DescribeTools creates an accessibility description of tool status.
func (ah *AccessibilityHelper) DescribeTools(pending, total int) string {
	if pending == 0 && total == 0 {
		return "No tools running"
	}
	if pending > 0 {
		return fmt.Sprintf("%d tool running", pending)
	}
	return fmt.Sprintf("%d tools completed", total)
}

// truncate truncates a string to maxLength.
func truncateStr(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength-3] + "..."
}

// AccessibilityLabel returns an accessibility label for a view element.
func AccessibilityLabel(role, name string, props ...string) string {
	var b strings.Builder
	b.WriteString(role)
	b.WriteString(": ")
	b.WriteString(name)

	for _, p := range props {
		b.WriteString(", ")
		b.WriteString(p)
	}

	return b.String()
}

// FocusOrder represents the focus order for keyboard navigation.
type FocusOrder struct {
	Current int
	Items   []Focusable
}

// Focusable is an interface for focusable elements.
type Focusable interface {
	Focus() error
	Blur() error
	IsFocused() bool
}

// NewFocusOrder creates a new focus order.
func NewFocusOrder(items []Focusable) *FocusOrder {
	return &FocusOrder{
		Current: 0,
		Items:   items,
	}
}

// Next moves focus to the next item.
func (fo *FocusOrder) Next() {
	if len(fo.Items) == 0 {
		return
	}
	fo.Current = (fo.Current + 1) % len(fo.Items)
	fo.focusCurrent()
}

// Prev moves focus to the previous item.
func (fo *FocusOrder) Prev() {
	if len(fo.Items) == 0 {
		return
	}
	fo.Current--
	if fo.Current < 0 {
		fo.Current = len(fo.Items) - 1
	}
	fo.focusCurrent()
}

// Current returns the currently focused item.
func (fo *FocusOrder) CurrentItem() Focusable {
	if fo.Current >= 0 && fo.Current < len(fo.Items) {
		return fo.Items[fo.Current]
	}
	return nil
}

func (fo *FocusOrder) focusCurrent() {
	for i, item := range fo.Items {
		if i == fo.Current {
			item.Focus()
		} else {
			item.Blur()
		}
	}
}

package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// ProgressItem tracks progress for a long-running operation.
type ProgressItem struct {
	ID        string
	Name      string
	Total     float64
	Completed float64
	Started   int64 // Unix timestamp
}

// ProgressTracker manages multiple progress items.
type ProgressTracker struct {
	items   map[string]*ProgressItem
	isDark  bool
	styles  ProgressStyles
}

// ProgressStyles holds styling for progress display.
type ProgressStyles struct {
	Bar       lipgloss.Style
	Label     lipgloss.Style
	Percent   lipgloss.Style
	Completed lipgloss.Style
	Empty     lipgloss.Style
}

// DefaultProgressStyles returns default styles.
func DefaultProgressStyles(isDark bool) ProgressStyles {
	if isDark {
		return ProgressStyles{
			Bar:       lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0")),
			Label:     lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			Percent:   lipgloss.NewStyle().Foreground(lipgloss.Color("#81a1c1")),
			Completed: lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c")),
			Empty:     lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		}
	}
	return ProgressStyles{
		Bar:       lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f8f")),
		Label:     lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Percent:   lipgloss.NewStyle().Foreground(lipgloss.Color("#5e81ac")),
		Completed: lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f38")),
		Empty:     lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
	}
}

// NewProgressTracker creates a new progress tracker.
func NewProgressTracker(isDark bool) *ProgressTracker {
	return &ProgressTracker{
		items:  make(map[string]*ProgressItem),
		isDark: isDark,
		styles: DefaultProgressStyles(isDark),
	}
}

// SetDark updates theme.
func (pt *ProgressTracker) SetDark(isDark bool) {
	pt.isDark = isDark
	pt.styles = DefaultProgressStyles(isDark)
}

// Start begins tracking a new progress item.
func (pt *ProgressTracker) Start(id, name string, total float64) {
	pt.items[id] = &ProgressItem{
		ID:      id,
		Name:    name,
		Total:   total,
		Started: time.Now().Unix(),
	}
}

// Update sets the current progress.
func (pt *ProgressTracker) Update(id string, completed float64) {
	if item, ok := pt.items[id]; ok {
		item.Completed = completed
	}
}

// Complete marks an item as done.
func (pt *ProgressTracker) Complete(id string) {
	if item, ok := pt.items[id]; ok {
		item.Completed = item.Total
	}
}

// Remove stops tracking an item.
func (pt *ProgressTracker) Remove(id string) {
	delete(pt.items, id)
}

// Get returns a progress item by ID.
func (pt *ProgressTracker) Get(id string) (*ProgressItem, bool) {
	item, ok := pt.items[id]
	return item, ok
}

// All returns all progress items.
func (pt *ProgressTracker) All() []*ProgressItem {
	result := make([]*ProgressItem, 0, len(pt.items))
	for _, item := range pt.items {
		result = append(result, item)
	}
	return result
}

// Render returns the progress bar view for an item.
func (pt *ProgressTracker) Render(id string, width int) string {
	item, ok := pt.items[id]
	if !ok || item.Total == 0 {
		return ""
	}

	percent := int((item.Completed / item.Total) * 100)
	barWidth := width - 20 // Leave room for label and percentage
	if barWidth < 5 {
		barWidth = 5
	}

	filled := int((item.Completed / item.Total) * float64(barWidth))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	label := pt.styles.Label.Render(item.Name)
	percentStr := pt.styles.Percent.Render(fmt.Sprintf("%d%%", percent))

	return fmt.Sprintf("%s [%s] %s", label, bar, percentStr)
}

// RenderSimple returns a simple progress bar without the label.
func (pt *ProgressTracker) RenderSimple(id string, width int) string {
	item, ok := pt.items[id]
	if !ok || item.Total == 0 {
		return ""
	}

	percent := int((item.Completed / item.Total) * 100)
	barWidth := width - 8 // Leave room for percentage
	if barWidth < 5 {
		barWidth = 5
	}

	filled := int((item.Completed / item.Total) * float64(barWidth))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	percentStr := pt.styles.Percent.Render(fmt.Sprintf("%d%%", percent))

	return fmt.Sprintf("[%s] %s", bar, percentStr)
}

// HasItems returns true if there are any progress items.
func (pt *ProgressTracker) HasItems() bool {
	return len(pt.items) > 0
}

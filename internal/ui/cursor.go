package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	// renderPickerFrame uses a one-cell border and one-cell horizontal
	// padding. Keep cursor translation next to that frame contract so modal
	// inputs cannot drift independently of their rendered controls.
	pickerFrameCursorX = 2
	pickerFrameCursorY = 1
)

// offsetCursor returns a translated copy without mutating the child-owned
// cursor. A nil child cursor means that surface does not currently own focus.
func offsetCursor(cursor *tea.Cursor, x, y int) *tea.Cursor {
	if cursor == nil {
		return nil
	}

	translated := *cursor
	translated.X += x
	translated.Y += y
	return &translated
}

func pickerFrameCursor(cursor *tea.Cursor) *tea.Cursor {
	return offsetCursor(cursor, pickerFrameCursorX, pickerFrameCursorY)
}

// centeredOverlayStartY mirrors overlayOnContent's vertical placement.
func centeredOverlayStartY(base, overlay string) int {
	startY := (len(strings.Split(base, "\n")) - len(strings.Split(overlay, "\n"))) / 2
	return max(0, startY)
}

// centeredOverlayLineX mirrors overlayOnContent's per-line horizontal
// placement. Lip Gloss width keeps ANSI styling and wide runes coordinate-safe.
func centeredOverlayLineX(width int, line string) int {
	return max(0, (width-lipgloss.Width(line))/2)
}

// overlayCursor translates a cursor local to a rendered overlay into the
// parent terminal coordinate space. Horizontal centering is calculated from
// the cursor's actual row because styled modal lines can have different widths.
func overlayCursor(base, overlay string, width int, cursor *tea.Cursor) *tea.Cursor {
	if cursor == nil {
		return nil
	}

	overlayLines := strings.Split(overlay, "\n")
	if cursor.Y < 0 || cursor.Y >= len(overlayLines) {
		return nil
	}

	return offsetCursor(
		cursor,
		centeredOverlayLineX(width, overlayLines[cursor.Y]),
		centeredOverlayStartY(base, overlay),
	)
}

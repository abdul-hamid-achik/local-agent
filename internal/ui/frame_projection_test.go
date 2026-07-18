package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestFrameProjectionPartitionsSafeScreen(t *testing.T) {
	sizes := []struct {
		width  int
		height int
	}{
		{width: 30, height: 12},
		{width: 40, height: 16},
		{width: 72, height: 24},
		{width: 80, height: 24},
		{width: 112, height: 40},
		{width: 160, height: 48},
		{width: 200, height: 60},
	}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = updated.(*Model)
			frame := m.projectFrame()

			wantScreen := NewCellRect(0, 0, size.width, size.height)
			if frame.Screen != wantScreen {
				t.Fatalf("screen = %#v, want %#v", frame.Screen, wantScreen)
			}
			wantSafe := Inset(wantScreen, Insets{Right: 1, Bottom: 1})
			if frame.SafeScreen != wantSafe {
				t.Fatalf("safe screen = %#v, want %#v", frame.SafeScreen, wantSafe)
			}
			if !rectWithin(frame.Transcript.Rect, frame.SafeScreen) ||
				!rectWithin(frame.Footer.Rect, frame.SafeScreen) {
				t.Fatalf("surface escaped safe screen: %#v", frame)
			}
			if !intersection(frame.Transcript.Rect, frame.Footer.Rect).Empty() {
				t.Fatalf("transcript and footer overlap: transcript=%#v footer=%#v",
					frame.Transcript.Rect, frame.Footer.Rect)
			}
			if cellArea(frame.Transcript.Rect)+cellArea(frame.Footer.Rect) != cellArea(frame.SafeScreen) {
				t.Fatalf("surfaces do not partition safe screen: %#v", frame)
			}
			if got := m.viewport.Height(); got != max(1, frame.Transcript.Rect.Height()) {
				t.Fatalf("viewport height = %d, projected transcript height = %d", got, frame.Transcript.Rect.Height())
			}
			if frame.Cursor != nil && !frame.Footer.Rect.Contains(frame.Cursor.X, frame.Cursor.Y) {
				t.Fatalf("cursor %#v is outside footer %#v", frame.Cursor, frame.Footer.Rect)
			}
		})
	}
}

func TestFrameProjectionPaintsTheMeasuredFooter(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("first line\nsecond line\nthird line")
	m.syncInputHeight()
	frame := m.projectFrame()
	view := m.View()

	if frame.Footer.Content == "" || !strings.Contains(view.Content, frame.Footer.Content) {
		t.Fatal("View did not paint the footer content produced by FrameProjection")
	}
	if got := lipgloss.Height(view.Content); got > m.height {
		t.Fatalf("rendered height = %d, terminal height = %d", got, m.height)
	}
	for _, line := range strings.Split(view.Content, "\n") {
		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("rendered line width = %d, terminal width = %d", got, m.width)
		}
	}
}

func TestFrameProjectionIsMonotonicWithoutExplicitPanelAction(t *testing.T) {
	m := newTestModel(t)

	previousWidth := -1
	for width := minTerminalWidth; width <= 200; width++ {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		m = updated.(*Model)
		got := m.projectFrame().Transcript.Rect.Width()
		if got < previousWidth {
			t.Fatalf("transcript width decreased at %d: %d -> %d", width, previousWidth, got)
		}
		previousWidth = got
	}

	previousHeight := -1
	for height := minTerminalHeight; height <= 100; height++ {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: height})
		m = updated.(*Model)
		got := m.projectFrame().Transcript.Rect.Height()
		if got < previousHeight {
			t.Fatalf("transcript height decreased at %d: %d -> %d", height, previousHeight, got)
		}
		previousHeight = got
	}
}

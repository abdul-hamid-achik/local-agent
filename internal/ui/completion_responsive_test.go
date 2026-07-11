package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCompletionModalFitsSupportedTerminalSizes(t *testing.T) {
	items := make([]Completion, 14)
	for i := range items {
		items[i] = Completion{
			Label:    "@非常に長い_unicode_workspace_file_name_" + strings.Repeat("x", 30),
			Insert:   "@file",
			Category: "file",
		}
	}

	for _, size := range []struct {
		width  int
		height int
	}{{80, 24}, {60, 20}, {40, 20}, {30, 12}} {
		t.Run(strings.Repeat("w", size.width/10), func(t *testing.T) {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = updated.(*Model)
			m.completionState = newCompletionState("attachments", items, true)
			m.overlay = OverlayCompletion
			m.resizePickerOverlays()

			rendered := m.renderCompletionModal()
			assertRenderedLinesFit(t, rendered, size.width)
			assertRenderedHeightFits(t, rendered, size.height)
			if !strings.Contains(rendered, "Esc cancel") {
				t.Fatalf("completion footer lost cancel affordance:\n%s", rendered)
			}
		})
	}
}

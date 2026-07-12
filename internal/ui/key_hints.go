package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// keyHint is presentation-only. Matching remains owned by Bubbles key.Binding;
// callers should source Key from Binding.Help whenever one exists.
type keyHint struct {
	Key    string
	Action string
}

// renderKeyHints applies one footer grammar everywhere: alternative keys use
// '/', keys and actions use one space, and peer hints use ' · '. Callers order
// hints by importance, with dismissal/safety first. Narrow layouts preserve
// every essential key with the leading action before dropping lower-priority
// controls from the right.
func (m *Model) renderKeyHints(width int, hints ...keyHint) string {
	if width <= 0 || len(hints) == 0 {
		return ""
	}
	// Compact progressively: keep as many controls as possible, then retain as
	// many leading action labels as fit. Trying every intermediate action count
	// avoids needlessly turning a clear primary hint such as "enter select" into
	// an unlabeled "enter" when only a lower-priority action needs to yield.
	for keep := len(hints); keep > 0; keep-- {
		for actionLimit := keep; actionLimit > 0; actionLimit-- {
			if rendered := m.renderKeyHintSet(hints[:keep], actionLimit); lipgloss.Width(rendered) <= width {
				return rendered
			}
		}
	}
	return truncateDisplay(m.renderKeyHintSet(hints[:1], 0), width)
}

// actionLimit is -1 for every action, 0 for none, or a positive count of
// leading actions. Since callers place dismissal first, 1 preserves the
// critical close/back/cancel verb while compacting lower-priority hints.
func (m *Model) renderKeyHintSet(hints []keyHint, actionLimit int) string {
	parts := make([]string, 0, len(hints))
	for index, hint := range hints {
		keyLabel := strings.ToLower(strings.TrimSpace(hint.Key))
		action := strings.ToLower(strings.TrimSpace(hint.Action))
		if keyLabel == "" && action == "" {
			continue
		}
		part := ""
		if keyLabel != "" {
			part = m.styles.FocusIndicator.Render(keyLabel)
		}
		if (actionLimit < 0 || index < actionLimit) && action != "" {
			if part != "" {
				part += " "
			}
			part += m.styles.OverlayDim.Render(action)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, m.styles.OverlayDim.Render(" · "))
}

package ui

import "strings"

// optionComposedKeys maps what a stock macOS terminal INSERTS to the chord the
// user was reaching for. Option is not Meta unless the terminal is told so —
// until then Option+M composes "µ" into the composer instead of toggling mouse
// capture.
var optionComposedKeys = map[string]string{
	"µ": "alt+m",
	"∂": "alt+d",
	"ø": "alt+o",
	"®": "alt+r",
	"†": "alt+t",
}

// noticeForOptionComposedKey returns the hint for a character that is really a
// swallowed chord, or "" for ordinary text. Only for an empty composer — someone
// mid-sentence typing the symbol means the symbol.
func (m *Model) noticeForOptionComposedKey(typed string) string {
	if m == nil || strings.TrimSpace(m.input.Value()) != "" {
		return ""
	}
	chord, composed := optionComposedKeys[typed]
	if !composed {
		return ""
	}
	if chord == "alt+m" {
		return "That is Option+M — this terminal composes µ instead of sending alt+m. " +
			"Run /mouse to toggle select mode, or enable Option as Meta in the terminal."
	}
	return "That is Option — this terminal composes a character instead of sending " +
		chord + ". Set it to use Option as Meta, or use the matching slash command."
}

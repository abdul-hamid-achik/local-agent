package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

type helpRow struct {
	key  string
	desc string
}

// helpContentWidth returns the inner width for the help modal content.
func (m *Model) helpContentWidth() int {
	return pickerListWidth(m.width, 60)
}

// helpViewportHeight returns the viewport height for the help modal.
func (m *Model) helpViewportHeight() int {
	// Shared frame: border (2), title + gap (2), and footer (1).
	h := m.height - 6
	if m.helpContentWidth() < 30 {
		// The minimum-width footer uses a second row so close, page navigation,
		// and endpoint navigation all remain discoverable.
		h--
	}
	if h < 1 {
		h = 1
	}
	return h
}

// buildHelpContent builds the raw help text (without border/viewport wrapper).
func (m *Model) buildHelpContent(innerW int) string {
	var b strings.Builder

	// Keyboard shortcuts section.
	b.WriteString(m.styles.OverlayAccent.Render("Keyboard Shortcuts"))
	b.WriteString("\n")

	m.writeHelpRows(&b, m.keyHelpRows(), innerW)

	b.WriteString("\n")
	b.WriteString(m.styles.OverlayAccent.Render("Input Shortcuts"))
	b.WriteString("\n")

	inputShortcuts := []helpRow{
		{"@file / @agent", "Insert file or agent mention text"},
		{"~/… or /…", "Review temporary read-only access; MCP tools require separate approval"},
		{"#skill", "Insert skill mention text"},
		{"/cmd", "Run slash command"},
		{"team / swarm / MoE", "Ask naturally for a read-only expert consultation when enabled"},
		{"enter (running)", "Queue one follow-up; it sends after the current turn settles successfully"},
		{"esc (running)", "Cancel the turn; a queued follow-up returns to the composer"},
	}

	m.writeHelpRows(&b, inputShortcuts, innerW)

	b.WriteString("\n")
	b.WriteString(m.styles.OverlayAccent.Render("Slash Commands"))
	b.WriteString("\n")

	// Slash commands.
	if m.cmdRegistry != nil {
		commands := make([]helpRow, 0, len(m.cmdRegistry.All()))
		ctx := m.buildCommandContext()
		for _, cmd := range m.cmdRegistry.All() {
			commands = append(commands, helpRow{key: "/" + cmd.Name, desc: cmd.Description})
			for _, action := range m.cmdRegistry.Actions(cmd.Name, ctx) {
				description := action.Spec.Description
				if !action.Enabled {
					description = "Unavailable · " + action.DisabledReason
				}
				commands = append(commands, helpRow{key: action.Spec.CommandText(), desc: description})
			}
		}
		m.writeHelpRows(&b, commands, innerW)
	}

	return b.String()
}

func (m *Model) keyHelpRows() []helpRow {
	var rows []helpRow
	for _, group := range m.keys.FullHelp() {
		for _, binding := range group {
			help := binding.Help()
			if strings.TrimSpace(help.Key) == "" || strings.TrimSpace(help.Desc) == "" {
				continue
			}
			description := strings.TrimSpace(help.Desc)
			if len(description) > 0 {
				description = strings.ToUpper(description[:1]) + description[1:]
			}
			rows = append(rows, helpRow{key: strings.ToLower(help.Key), desc: description})
		}
	}
	return rows
}

// writeHelpRows renders aligned rows on normal terminals and stacked rows on
// narrow ones. Descriptions wrap instead of being silently clipped.
func (m *Model) writeHelpRows(b *strings.Builder, rows []helpRow, innerW int) {
	if innerW < 28 {
		for _, row := range rows {
			b.WriteString("  ")
			b.WriteString(m.styles.FocusIndicator.Render(truncateDisplay(row.key, max(1, innerW-3))))
			b.WriteString("\n")
			for _, line := range strings.Split(wrapText(row.desc, max(1, innerW-5)), "\n") {
				b.WriteString("    ")
				b.WriteString(m.styles.OverlayDim.Render(line))
				b.WriteString("\n")
			}
		}
		return
	}

	keyW := 16
	if innerW < 44 {
		keyW = 10
		for _, row := range rows {
			keyW = min(16, max(keyW, lipgloss.Width(row.key)))
		}
	}
	// Leave the terminal's final cell unused. Writing exactly to the edge can
	// trigger an implicit wrap before the explicit newline in some PTYs.
	descW := max(1, innerW-keyW-5)
	for _, row := range rows {
		descLines := strings.Split(wrapText(row.desc, descW), "\n")
		for i, line := range descLines {
			if i == 0 {
				fmt.Fprintf(b, "  %s  %s\n",
					m.styles.FocusIndicator.Width(keyW).Render(truncateDisplay(row.key, keyW)),
					m.styles.OverlayDim.Render(line),
				)
				continue
			}
			b.WriteString(strings.Repeat(" ", keyW+4))
			b.WriteString(m.styles.OverlayDim.Render(line))
			b.WriteString("\n")
		}
	}
}

// initHelpViewport creates and populates the help viewport for scrolling.
func (m *Model) initHelpViewport() {
	m.resizeHelpViewport(false)
}

func (m *Model) resizeHelpViewport(preserveOffset bool) {
	offset := 0
	if preserveOffset {
		offset = m.helpViewport.YOffset()
	}
	innerW := m.helpContentWidth()
	vpH := m.helpViewportHeight()

	m.helpViewport = viewport.New(
		viewport.WithWidth(innerW),
		viewport.WithHeight(vpH),
	)
	// Disable default arrow key bindings (we handle j/k/up/down ourselves via parent)
	m.helpViewport.KeyMap.Up.SetEnabled(false)
	m.helpViewport.KeyMap.Down.SetEnabled(false)
	m.helpViewport.KeyMap.PageUp.SetEnabled(false)
	m.helpViewport.KeyMap.PageDown.SetEnabled(false)
	m.helpViewport.KeyMap.HalfPageUp.SetEnabled(false)
	m.helpViewport.KeyMap.HalfPageDown.SetEnabled(false)

	content := m.buildHelpContent(innerW)
	m.helpViewport.SetContent(content)
	m.helpViewport.SetYOffset(offset)
}

// renderHelpOverlay builds a centered, scrollable help modal.
func (m *Model) renderHelpOverlay(_ int) string {
	innerW := m.helpContentWidth()

	var b strings.Builder

	// Title.
	b.WriteString(m.styles.OverlayTitle.Render("Help"))
	b.WriteString("\n\n")

	// Viewport content (scrollable).
	b.WriteString(m.helpViewport.View())
	b.WriteString("\n")

	// Scroll indicator / footer.
	pct := m.helpViewport.ScrollPercent()
	hints := []keyHint{{Key: "esc/q", Action: m.overlayCloseLabel()}}
	if pct <= 0 {
		hints = append(hints,
			keyHint{Key: "pgdn", Action: "more"},
			keyHint{Key: "g/shift+g", Action: "ends"},
		)
	} else if pct >= 1.0 {
		hints = append(hints,
			keyHint{Key: "j/k", Action: "scroll"},
			keyHint{Key: "g", Action: "top"},
		)
	} else {
		hints = append(hints,
			keyHint{Key: "j/k", Action: "scroll"},
			keyHint{Key: "pgup/pgdn", Action: "page"},
			keyHint{Key: fmt.Sprintf("%.0f%%", pct*100)},
		)
	}

	footer := m.renderKeyHints(innerW, hints...)
	if innerW < 30 {
		var navigation []keyHint
		switch {
		case pct <= 0:
			navigation = []keyHint{{Key: "pgdn", Action: "more"}, {Key: "g/⇧g", Action: "ends"}}
		case pct >= 1.0:
			navigation = []keyHint{{Key: "j/k", Action: "scroll"}, {Key: "g", Action: "top"}}
		default:
			navigation = []keyHint{{Key: "pgup/dn", Action: "page"}, {Key: "g/⇧g", Action: "ends"}}
		}
		footer = m.renderKeyHints(innerW, keyHint{Key: "esc/q", Action: m.overlayCloseLabel()}) + "\n" +
			m.renderKeyHints(innerW, navigation...)
	}

	return m.renderPickerFrame(b.String(), 60, footer)
}

// overlayOnContent renders the overlay centered on the viewport area.
func (m *Model) overlayOnContent(base, overlay string) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	startY := centeredOverlayStartY(base, overlay)
	canvasWidth := max(m.width, lipgloss.Width(base), lipgloss.Width(overlay))
	canvas := lipgloss.NewCanvas(canvasWidth, len(baseLines))
	overlayWidth := lipgloss.Width(overlay)
	// Preserve transcript context when the modal has a meaningful outside
	// gutter. If only a few cells remain, those cells become chopped words and
	// false-looking prompts, so the modal owns its intersecting rows instead.
	compactMask := m.width > 0 && (m.width <= 40 || m.width-overlayWidth < 16)
	layerCapacity := len(overlayLines) + 1
	if compactMask {
		layerCapacity += len(overlayLines)
	}
	layers := make([]*lipgloss.Layer, 0, layerCapacity)
	layers = append(layers, lipgloss.NewLayer(base).Z(0))

	for i, ol := range overlayLines {
		row := startY + i
		if row >= len(baseLines) {
			break
		}
		overlayZ := 1
		if compactMask {
			// One- and two-cell transcript fragments around a narrow modal read
			// like broken controls. Compact overlays therefore own their complete
			// rows; wider terminals keep the transparent transcript context.
			layers = append(layers, lipgloss.NewLayer(strings.Repeat(" ", m.width)).
				Y(row).
				Z(overlayZ))
			overlayZ++
		}
		layers = append(layers, lipgloss.NewLayer(ol).
			X(centeredOverlayLineX(m.width, ol)).
			Y(row).
			Z(overlayZ))
	}

	// Lip Gloss' cell compositor keeps ANSI styles and grapheme widths intact.
	// Wide overlay rows replace only their own bounds; compact rows use the
	// explicit mask above so tiny base fragments never resemble active controls.
	canvas.Compose(lipgloss.NewCompositor(layers...))
	return canvas.Render()
}

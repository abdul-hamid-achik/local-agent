package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *Model) View() tea.View {
	if !m.ready {
		return tea.NewView("  initializing...")
	}

	var b strings.Builder

	// Header bar
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// Message viewport (scrollable)
	vpContent := m.viewport.View()

	// If overlay is active, render it on top of the viewport.
	if m.overlay == OverlayHelp {
		helpOverlay := m.renderHelpOverlay(m.width)
		vpContent = m.overlayOnContent(vpContent, helpOverlay)
	} else if m.overlay == OverlayCompletion && m.completionActive {
		completionModal := m.renderCompletionModal()
		vpContent = m.overlayOnContent(vpContent, completionModal)
	}

	b.WriteString(vpContent)
	b.WriteString("\n")

	// Divider line.
	b.WriteString(m.styles.Divider.Render(rule(m.width)))
	b.WriteString("\n")

	// Status line.
	b.WriteString(m.renderStatusLine())
	b.WriteString("\n")

	// Input or streaming hint
	if m.state == StateIdle {
		b.WriteString(m.input.View())
	} else if m.state == StateWaiting {
		b.WriteString(m.styles.StreamHint.Render("  " + m.spin.View() + " thinking... press Esc to cancel"))
	} else {
		b.WriteString(m.styles.StreamHint.Render("  " + m.spin.View() + " streaming... press Esc to cancel"))
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m *Model) renderCompletionPopup() string {
	var b strings.Builder

	if len(m.completionItems) == 0 {
		return ""
	}

	maxItems := 5
	if len(m.completionItems) < maxItems {
		maxItems = len(m.completionItems)
	}

	// Calculate width
	maxWidth := 10 // minimum width
	for i := 0; i < maxItems; i++ {
		if len(m.completionItems[i].Label) > maxWidth {
			maxWidth = len(m.completionItems[i].Label)
		}
	}
	maxWidth += 4

	// Draw popup border
	border := "┌" + strings.Repeat("─", maxWidth) + "┐"
	b.WriteString(m.styles.CompletionBorder.Render(border))
	b.WriteString("\n")

	for i := 0; i < maxItems; i++ {
		item := m.completionItems[i]
		line := "│ "

		if i == m.completionIndex {
			line += "▶ "
			line += m.styles.CompletionSelected.Render(truncate(item.Label, maxWidth-2))
		} else {
			line += "  "
			line += item.Label
		}

		// Pad to max width (ensure non-negative)
		padding := maxWidth - len(line) + 1
		if padding < 0 {
			padding = 0
		}
		line += strings.Repeat(" ", padding) + "│"

		if i == m.completionIndex {
			b.WriteString(m.styles.CompletionSelected.Render(line))
		} else {
			b.WriteString(m.styles.CompletionBorder.Render(line))
		}
		b.WriteString("\n")
	}

	border = "└" + strings.Repeat("─", maxWidth) + "┘"
	b.WriteString(m.styles.CompletionBorder.Render(border))

	return b.String()
}

func (m *Model) renderCompletionModal() string {
	if len(m.completionItems) == 0 {
		return ""
	}

	return m.listModel.View()
}

// renderHeader builds:
//
//	local-agent                        qwen3:8b · 5 tools
//	━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
func (m *Model) renderHeader() string {
	title := m.styles.HeaderTitle.Render("local-agent")

	var infoStr string
	if m.model != "" {
		parts := []string{m.model}
		if m.toolCount > 0 {
			parts = append(parts, fmt.Sprintf("%d tools", m.toolCount))
		}
		if m.serverCount > 0 {
			parts = append(parts, fmt.Sprintf("%d servers", m.serverCount))
		}
		if m.loadedFile != "" {
			parts = append(parts, "ctx")
		}
		if m.iceEnabled {
			parts = append(parts, "ICE")
		}
		infoStr = m.styles.HeaderInfo.Render(strings.Join(parts, " · "))
	}

	// Arrange title left, info right.
	titleW := lipgloss.Width(title)
	infoW := lipgloss.Width(infoStr)
	gap := m.width - titleW - infoW
	if gap < 1 {
		gap = 1
	}

	line := title + strings.Repeat(" ", gap) + infoStr
	ruler := m.styles.HeaderRule.Render(thickRule(m.width))

	return line + "\n" + ruler
}

// renderFooter builds the divider + status + input area.
func (m *Model) renderFooter() string {
	var b strings.Builder

	// Divider line.
	b.WriteString(m.styles.Divider.Render(rule(m.width)))
	b.WriteString("\n")

	// Status line.
	b.WriteString(m.renderStatusLine())
	b.WriteString("\n")

	// Input or streaming hint.
	if m.state == StateIdle {
		b.WriteString(m.input.View())
	} else if m.state == StateWaiting {
		b.WriteString(m.styles.StreamHint.Render("  " + m.spin.View() + " thinking... press Esc to cancel"))
	} else {
		b.WriteString(m.styles.StreamHint.Render("  " + m.spin.View() + " streaming... press Esc to cancel"))
	}

	return b.String()
}

// renderStatusLine builds the status bar above the input/hint area.
func (m *Model) renderStatusLine() string {
	var parts []string

	switch m.state {
	case StateWaiting:
		// No status line content — the hint line below shows "thinking..."
	case StateStreaming:
		if m.streamBuf.Len() > 0 {
			parts = append(parts, m.styles.StatusText.Render(
				fmt.Sprintf("%d chars", m.streamBuf.Len()),
			))
		}
		if m.toolsPending > 0 {
			parts = append(parts, m.styles.StatusText.Render(
				fmt.Sprintf("%d tool(s) pending", m.toolsPending),
			))
		}
	case StateIdle:
		dot := m.styles.StatusDot.Render("○")
		label := m.styles.StatusText.Render(" ready")
		parts = append(parts, dot+label)
		if m.promptTokens > 0 && m.numCtx > 0 {
			parts = append(parts, m.styles.StatusText.Render(
				fmt.Sprintf("~%s / %s ctx", formatTokens(m.promptTokens), formatTokens(m.numCtx)),
			))
		}
		if m.evalCount > 0 {
			parts = append(parts, m.styles.StatusText.Render(
				fmt.Sprintf("%d tokens", m.evalCount),
			))
		}
	}

	return strings.Join(parts, m.styles.StatusText.Render(" · "))
}

// formatTokens formats a token count as "1.2k" or "8192".
func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// renderEntries builds the full chat content for the viewport.
func (m *Model) renderEntries() string {
	var b strings.Builder
	contentW := m.width - 4

	// Welcome message when empty.
	if len(m.entries) == 0 && m.streamBuf.Len() == 0 {
		m.renderWelcome(&b)
		return b.String()
	}

	for i, entry := range m.entries {
		switch entry.Kind {
		case "user":
			m.renderUserMsg(&b, entry.Content, contentW)
		case "assistant":
			m.renderAssistantMsg(&b, entry, contentW)
		case "tool_group":
			m.renderToolGroup(&b, entry.ToolIndex, i)
		case "error":
			b.WriteString(m.styles.ErrorText.Render("error: " + entry.Content))
			b.WriteString("\n\n")
		case "system":
			b.WriteString(m.styles.SystemText.Render(entry.Content))
			b.WriteString("\n\n")
		}

		// Add spacing between message groups.
		if i < len(m.entries)-1 {
			next := m.entries[i+1]
			curr := entry.Kind
			nextK := next.Kind

			// Tight grouping between consecutive tool_group entries.
			if curr == "tool_group" && nextK == "tool_group" {
				continue
			}
			// Extra spacing between different message groups.
			if curr != nextK {
				b.WriteString("\n")
			}
		}
	}

	// Render current streaming content (plain text, no Glamour).
	if m.streamBuf.Len() > 0 {
		if len(m.entries) > 0 {
			last := m.entries[len(m.entries)-1]
			if last.Kind != "tool_group" {
				b.WriteString("\n")
			}
		}
		m.renderStreamingMsg(&b, m.streamBuf.String(), contentW)
	}

	return b.String()
}

// renderWelcome renders the empty-state welcome message.
func (m *Model) renderWelcome(b *strings.Builder) {
	title := m.styles.HeaderTitle.Render("Welcome to local-agent")
	b.WriteString("\n")
	b.WriteString("  " + title)
	b.WriteString("\n\n")

	var infoParts []string
	if m.model != "" {
		infoParts = append(infoParts, m.model)
	}
	if m.toolCount > 0 {
		infoParts = append(infoParts, fmt.Sprintf("%d tools", m.toolCount))
	}
	if m.serverCount > 0 {
		infoParts = append(infoParts, fmt.Sprintf("%d servers", m.serverCount))
	}
	if len(infoParts) > 0 {
		b.WriteString(m.styles.StatusText.Render("  Model: " + strings.Join(infoParts, " · ")))
		b.WriteString("\n\n")
	}

	b.WriteString(m.styles.SystemText.Render("  Type a message to start, or try:"))
	b.WriteString("\n")
	b.WriteString(m.styles.WelcomeHint.Render("    /help    "))
	b.WriteString(m.styles.SystemText.Render("— keyboard shortcuts & commands"))
	b.WriteString("\n")
	b.WriteString(m.styles.WelcomeHint.Render("    /servers "))
	b.WriteString(m.styles.SystemText.Render("— list connected tools"))
	b.WriteString("\n")
	b.WriteString(m.styles.WelcomeHint.Render("    /load    "))
	b.WriteString(m.styles.SystemText.Render("— add context from a file"))
	b.WriteString("\n")
}

// renderUserMsg renders a user message block.
func (m *Model) renderUserMsg(b *strings.Builder, content string, contentW int) {
	label := m.styles.UserLabel.Render("you")
	labelW := lipgloss.Width(label)
	ruleW := m.width - labelW - 3
	if ruleW < 4 {
		ruleW = 4
	}
	b.WriteString(label + " " + m.styles.RoleRule.Render(rule(ruleW)))
	b.WriteString("\n")
	b.WriteString(m.styles.UserContent.Render(wrapText(content, contentW)))
	b.WriteString("\n")
}

// renderAssistantMsg renders a completed assistant message block.
// Uses cached RenderedContent if available (snap-into-place pattern).
func (m *Model) renderAssistantMsg(b *strings.Builder, entry ChatEntry, contentW int) {
	label := m.styles.AsstLabel.Render("assistant")
	labelW := lipgloss.Width(label)
	ruleW := m.width - labelW - 3
	if ruleW < 4 {
		ruleW = 4
	}
	b.WriteString(label + " " + m.styles.RoleRule.Render(rule(ruleW)))
	b.WriteString("\n")

	// Use cached rendered content if available.
	rendered := entry.RenderedContent
	if rendered == "" {
		rendered = entry.Content
		if m.md != nil {
			rendered = m.md.RenderFull(rendered)
		}
	}
	// Trim excessive trailing whitespace from Glamour output.
	rendered = strings.TrimRight(rendered, " \t\n")
	rendered = indentBlock(rendered, "  ")
	b.WriteString(rendered)
	b.WriteString("\n")
}

// renderStreamingMsg renders the in-progress assistant message (plain text).
func (m *Model) renderStreamingMsg(b *strings.Builder, content string, contentW int) {
	label := m.styles.AsstLabel.Render("assistant")
	cursor := m.styles.StreamCursor.Render(" " + m.spin.View())
	labelW := lipgloss.Width(label) + lipgloss.Width(cursor)
	ruleW := m.width - labelW - 3
	if ruleW < 4 {
		ruleW = 4
	}
	b.WriteString(label + cursor + " " + m.styles.RoleRule.Render(rule(ruleW)))
	b.WriteString("\n")

	// During streaming: plain text only (no Glamour) for zero jitter.
	rendered := indentBlock(content, "  ")
	b.WriteString(rendered)
	b.WriteString("\n")
}

// renderToolGroup renders a tool entry based on its status and collapse state.
func (m *Model) renderToolGroup(b *strings.Builder, toolIdx, entryIdx int) {
	if toolIdx < 0 || toolIdx >= len(m.toolEntries) {
		return
	}
	te := m.toolEntries[toolIdx]

	// Add spacing before the first tool_group in a sequence.
	if entryIdx > 0 && m.entries[entryIdx-1].Kind != "tool_group" {
		b.WriteString("\n")
	}

	switch te.Status {
	case ToolStatusRunning:
		// Running: show spinner
		icon := m.styles.ToolCallIcon.Render("⚙")
		spinView := m.spin.View()
		text := m.styles.ToolCallText.Render(fmt.Sprintf(" %s ", te.Name))
		hint := m.styles.ToolRunningText.Render(spinView + " running...")
		b.WriteString(icon + text + hint)
		b.WriteString("\n")

	case ToolStatusDone:
		dur := formatDuration(te.Duration)
		if m.toolsCollapsed {
			// Collapsed: single dim line
			icon := m.styles.ToolDoneIcon.Render("✓")
			text := m.styles.ToolDoneText.Render(fmt.Sprintf(" %s (%s)", te.Name, dur))
			b.WriteString(icon + text)
			b.WriteString("\n")
		} else {
			// Expanded: show args + result
			icon := m.styles.ToolDoneIcon.Render("✓")
			text := m.styles.ToolDoneText.Render(fmt.Sprintf(" %s (%s)", te.Name, dur))
			b.WriteString(icon + text)
			b.WriteString("\n")
			// Args
			args := truncate(te.Args, 200)
			b.WriteString(m.styles.ToolDetailText.Render("      args: " + args))
			b.WriteString("\n")
			// Result
			result := truncate(te.Result, 300)
			b.WriteString(m.styles.ToolDetailText.Render("      result: " + result))
			b.WriteString("\n")
		}

	case ToolStatusError:
		// Error: always expanded regardless of collapse state
		dur := formatDuration(te.Duration)
		icon := m.styles.ToolErrorIcon.Render("✗")
		text := m.styles.ToolErrorText.Render(fmt.Sprintf(" %s (%s)", te.Name, dur))
		b.WriteString(icon + text)
		b.WriteString("\n")
		// Error result always shown
		result := truncate(te.Result, 300)
		b.WriteString(m.styles.ToolErrorText.Render("      " + result))
		b.WriteString("\n")
	}

	// Add spacing after the last tool_group in a sequence.
	if entryIdx < len(m.entries)-1 && m.entries[entryIdx+1].Kind != "tool_group" {
		b.WriteString("\n")
	}
}

// formatDuration formats a duration as "42ms" or "1.3s".
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// wrapText wraps text to the given width.
func wrapText(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	var result strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if len(line) <= width {
			if result.Len() > 0 {
				result.WriteString("\n")
			}
			result.WriteString(line)
			continue
		}
		words := strings.Fields(line)
		current := ""
		for _, w := range words {
			if current == "" {
				current = w
			} else if len(current)+1+len(w) <= width {
				current += " " + w
			} else {
				if result.Len() > 0 {
					result.WriteString("\n")
				}
				result.WriteString(current)
				current = w
			}
		}
		if current != "" {
			if result.Len() > 0 {
				result.WriteString("\n")
			}
			result.WriteString(current)
		}
	}
	return result.String()
}

// indentBlock adds a prefix to each line of a multi-line string.
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

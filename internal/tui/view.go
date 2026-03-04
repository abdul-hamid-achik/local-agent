package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
)

func (m *Model) View() tea.View {
	if !m.ready {
		return tea.NewView("  initializing...")
	}

	var content string

	// Build the right side: viewport + footer as one unit
	var rightSide strings.Builder
	rightSide.WriteString(m.viewport.View())
	rightSide.WriteString("\n")

	// Divider line (only in chat area, not under sidebar)
	rightSide.WriteString(m.styles.Divider.Render(rule(m.width - m.sidePanel.width - 1)))
	rightSide.WriteString("\n")

	// Status line.
	rightSide.WriteString(m.renderStatusLine())
	rightSide.WriteString("\n")

	// Input or streaming hint - clean Crush-style
	if m.state == StateIdle {
		rightSide.WriteString(m.input.View())
	} else if m.state == StateWaiting {
		rightSide.WriteString(m.styles.StreamHint.Render("  " + m.scramble.View() + " thinking... press Esc to cancel"))
	} else {
		rightSide.WriteString(m.styles.StreamHint.Render("  " + m.spin.View() + " streaming... press Esc to cancel"))
	}

	// Render side panel + right side horizontally using lipgloss
	if m.sidePanel.IsVisible() {
		panelView := m.sidePanel.View()
		rightContent := rightSide.String()

		// Calculate widths
		panelW := m.sidePanel.width
		rightW := m.width - panelW - 1 // -1 for separator

		// Create left panel with fixed width and FULL HEIGHT
		leftStyle := lipgloss.NewStyle().
			Width(panelW).
			Height(m.height)
		left := leftStyle.Render(panelView)

		// Create right side with fixed width
		rightStyle := lipgloss.NewStyle().
			Width(rightW).
			Height(m.height)
		right := rightStyle.Render(rightContent)

		// Join horizontally with separator
		// Create a full-height divider
		dividerChars := ""
		for i := 0; i < m.height; i++ {
			dividerChars += "│\n"
		}
		divider := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6c7a89")).
			Render(dividerChars)
		
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
	} else {
		// No panel - full width with footer
		content = rightSide.String()
	}

	// Render overlays on top (centered modal) using overlayOnContent
	if m.overlay != OverlayNone {
		var overlay string
		switch m.overlay {
		case OverlayHelp:
			overlay = m.renderHelpOverlay(m.width)
		case OverlayCompletion:
			if m.isCompletionActive() {
				overlay = m.renderCompletionModal()
			}
		case OverlayModelPicker:
			if m.modelPickerState != nil {
				overlay = m.renderModelPicker()
			}
		case OverlayPlanForm:
			if m.planFormState != nil {
				overlay = m.renderPlanForm()
			}
		case OverlaySessionsPicker:
			if m.sessionsPickerState != nil {
				overlay = m.renderSessionsPicker()
			}
		}
		if overlay != "" {
			content = m.overlayOnContent(content, overlay)
		}
	}

	var b strings.Builder
	b.WriteString(content)
	b.WriteString("\n")

	// Toast notifications (bottom-right corner)
	if m.toastMgr != nil && m.toastMgr.HasToasts() {
		m.toastMgr.Update()
		toastStr := m.toastMgr.Render(m.width)
		if toastStr != "" {
			b.WriteString("\n")
			b.WriteString(toastStr)
		}
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	// Terminal title progress.
	switch m.state {
	case StateWaiting:
		v.WindowTitle = "LOCAL AGENT \u00b7 thinking..."
	case StateStreaming:
		v.WindowTitle = "LOCAL AGENT \u00b7 streaming..."
	default:
		if m.doneFlash {
			v.WindowTitle = "LOCAL AGENT \u00b7 done"
		} else {
			v.WindowTitle = "LOCAL AGENT"
		}
	}

	return v
}

func (m *Model) renderCompletionModal() string {
	cs := m.completionState
	if cs == nil {
		return ""
	}

	var b strings.Builder

	// Title
	var title string
	switch cs.Kind {
	case "command":
		title = "Commands"
	case "attachments":
		title = "Attach Files & Agents"
	case "skills":
		title = "Skills"
	default:
		title = "Complete"
	}
	b.WriteString(m.styles.OverlayTitle.Render(title))
	b.WriteString("\n")

	// Filter input
	b.WriteString(m.styles.CompletionFilter.Render("> " + cs.Filter.View()))
	b.WriteString("\n")

	// Breadcrumb for @ file browsing
	if cs.Kind == "attachments" && cs.CurrentPath != "" {
		b.WriteString(m.styles.CompletionCategory.Render(cs.CurrentPath + "/"))
		b.WriteString("\n")
	}

	// Divider
	maxW := 40
	if m.width-8 > maxW {
		maxW = m.width - 8
	}
	if maxW > 60 {
		maxW = 60
	}
	b.WriteString(m.styles.FocusIndicator.Render(strings.Repeat("─", maxW)))
	b.WriteString("\n")

	// Scrollable items (max 10 visible)
	maxVisible := 10
	items := cs.FilteredItems
	if len(items) == 0 {
		b.WriteString(m.styles.CompletionCategory.Render("  (no matches)"))
		b.WriteString("\n")
	} else {
		// Calculate scroll window
		start := 0
		if cs.Index >= maxVisible {
			start = cs.Index - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(items) {
			end = len(items)
		}

		for i := start; i < end; i++ {
			item := items[i]
			prefix := "  "
			if i == cs.Index {
				prefix = m.styles.FocusIndicator.Render("▸ ")
			}

			// Check if selected (for multi-select)
			selectedMark := ""
			if cs.Selected != nil {
				// Find original index
				for oi, orig := range cs.AllItems {
					if orig.Label == item.Label && orig.Insert == item.Insert {
						if cs.Selected[oi] {
							selectedMark = m.styles.FocusIndicator.Render(" ✓")
						}
						break
					}
				}
			}

			label := item.Label
			cat := m.styles.CompletionCategory.Render("  " + item.Category)

			if i == cs.Index {
				b.WriteString(prefix + m.styles.FocusIndicator.Render(label) + cat + selectedMark)
			} else {
				b.WriteString(prefix + label + cat + selectedMark)
			}
			b.WriteString("\n")
		}
	}

	// Searching indicator
	if cs.Searching {
		b.WriteString(m.styles.CompletionSearching.Render("  searching..."))
		b.WriteString("\n")
	}

	// Footer hints
	hints := "Enter=select  Esc=cancel"
	if cs.Kind == "attachments" && cs.CurrentPath != "" {
		hints += "  ←=back"
	}
	if cs.Selected != nil {
		hints += "  Tab=toggle"
	}
	b.WriteString(m.styles.CompletionFooter.Render(hints))

	// Wrap in a box
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.styles.OverlayBorder)).
		Padding(1, 2).
		Width(maxW + 4)

	return box.Render(b.String())
}

// renderHeader builds:
//
//	local-agent                        qwen3:8b · 5 tools
//	━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
func (m *Model) renderHeader() string {
	title := m.styles.HeaderTitle.Render("LOCAL AGENT")

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
		if m.promptTokens > 0 && m.numCtx > 0 {
			pct := m.promptTokens * 100 / m.numCtx
			var pctStyle lipgloss.Style
			switch {
			case pct > 85:
				pctStyle = m.styles.ContextPctHigh
			case pct > 60:
				pctStyle = m.styles.ContextPctMid
			default:
				pctStyle = m.styles.ContextPctLow
			}
			parts = append(parts, pctStyle.Render(contextProgressBar(pct)))
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
	ruler := m.styles.HeaderRule.Render(rule(m.width))

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
		b.WriteString(m.styles.StreamHint.Render("  " + m.scramble.View() + " thinking... press Esc to cancel"))
	} else {
		b.WriteString(m.styles.StreamHint.Render("  " + m.spin.View() + " streaming... press Esc to cancel"))
	}

	return b.String()
}

// renderStatusLine builds the status bar above the input/hint area.
func (m *Model) renderStatusLine() string {
	// Pending tool approval prompt overrides normal status.
	if m.pendingApproval != nil {
		args := agent.FormatToolArgs(m.pendingApproval.Args)
		if len(args) > 60 {
			args = args[:57] + "..."
		}
		return m.styles.StatusText.Render(
			fmt.Sprintf("  Allow %s %s? [y]es / [n]o / [a]lways", m.pendingApproval.ToolName, args),
		)
	}

	// Pending paste prompt overrides normal status.
	if m.pendingPaste != "" {
		lines := strings.Count(m.pendingPaste, "\n") + 1
		return m.styles.StatusText.Render(
			fmt.Sprintf("  Large paste (%d lines). Wrap as code block? [y/n/esc]", lines),
		)
	}

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
		// Mode badge.
		cfg := m.modeConfigs[m.mode]
		var modeStyle lipgloss.Style
		switch m.mode {
		case ModeAsk:
			modeStyle = m.styles.ModeAsk
		case ModePlan:
			modeStyle = m.styles.ModePlan
		case ModeBuild:
			modeStyle = m.styles.ModeBuild
		}
		parts = append(parts, modeStyle.Render("[ "+cfg.Label+" ]"))
		dot := m.styles.StatusDot.Render("○")
		label := m.styles.StatusText.Render(" ready")
		parts = append(parts, dot+label)
		if m.promptTokens > 0 && m.numCtx > 0 {
			parts = append(parts, m.styles.StatusText.Render(
				fmt.Sprintf("~%s / %s ctx", formatTokens(m.promptTokens), formatTokens(m.numCtx)),
			))
		}
		if m.sessionEvalTotal > 0 {
			parts = append(parts, m.styles.StatusText.Render(
				fmt.Sprintf("%s out (%d turns)", formatTokens(m.sessionEvalTotal), m.sessionTurnCount),
			))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, m.styles.StatusText.Render(" · "))
}

// formatTokens formats a token count as "1.2k" or "8192".
func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// renderEntries builds the full chat content for the viewport.
// Uses an incremental cache: during streaming, only the streaming tail is
// re-rendered while the entries prefix is reused from cache.
func (m *Model) renderEntries() string {
	contentW := m.width - 4

	// Startup progress screen.
	if m.initializing {
		var b strings.Builder
		m.renderStartup(&b)
		return b.String()
	}

	// Welcome message when no user messages yet
	hasUserMsg := false
	for _, e := range m.entries {
		if e.Kind == "user" || e.Kind == "assistant" {
			hasUserMsg = true
			break
		}
	}
	if !hasUserMsg && m.streamBuf.Len() == 0 {
		var b strings.Builder
		m.renderWelcome(&b)
		// Append any system entries (e.g. failed server notices) below welcome
		for _, e := range m.entries {
			if e.Kind == "system" {
				b.WriteString(m.styles.SystemText.Render(e.Content))
				b.WriteString("\n\n")
			} else if e.Kind == "error" {
				b.WriteString(m.styles.ErrorText.Render("error: " + e.Content))
				b.WriteString("\n\n")
			}
		}
		return b.String()
	}

	// Fast path: if entry cache is valid and entry count matches, reuse
	// the cached prefix and only re-render the streaming tail.
	if m.entryCacheValid && len(m.entries) == m.cachedEntryCount {
		m.toolEntryRows = m.cachedToolEntryRows
		if m.streamBuf.Len() > 0 {
			var b strings.Builder
			b.WriteString(m.cachedEntriesRender)
			if len(m.entries) > 0 {
				last := m.entries[len(m.entries)-1]
				if last.Kind != "tool_group" {
					b.WriteString("\n")
				}
			}
			m.renderStreamingMsg(&b, m.streamBuf.String(), contentW)
			return b.String()
		}
		return m.cachedEntriesRender
	}

	// Full render: iterate all entries.
	var b strings.Builder
	m.toolEntryRows = make(map[int]int)

	for i, entry := range m.entries {
		switch entry.Kind {
		case "user":
			m.renderUserMsg(&b, entry.Content, contentW)
		case "assistant":
			m.renderAssistantMsg(&b, entry, contentW)
		case "tool_group":
			m.toolEntryRows[entry.ToolIndex] = strings.Count(b.String(), "\n")
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

	// Cache the rendered entries prefix and toolEntryRows.
	m.cachedEntriesRender = b.String()
	m.cachedEntryCount = len(m.entries)
	m.cachedToolEntryRows = make(map[int]int, len(m.toolEntryRows))
	for k, v := range m.toolEntryRows {
		m.cachedToolEntryRows[k] = v
	}
	m.entryCacheValid = true

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

// renderWelcome renders the empty-state welcome message, centered horizontally.
func (m *Model) renderWelcome(b *strings.Builder) {
	var wb strings.Builder

	// ASCII art logo
	for _, line := range logoLines() {
		if line == "" {
			wb.WriteString("\n")
		} else {
			wb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#88c0d0")).
				Bold(true).
				Render(line))
			wb.WriteString("\n")
		}
	}

	// Show enhanced welcome
	title := gradientText("Welcome to LOCAL AGENT", []string{"#88c0d0", "#81a1c1", "#b48ead"})
	wb.WriteString("  " + m.styles.OverlayTitle.Render(title))
	wb.WriteString("\n")

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
		wb.WriteString(m.styles.StatusText.Render("  " + strings.Join(infoParts, " · ")))
		wb.WriteString("\n")
	}

	wb.WriteString("\n")

	// Mode hints with icons
	modes := []struct {
		key   string
		desc  string
		color string
	}{
		{"ASK", "Quick answers", "#81a1c1"},
		{"PLAN", "Design & reasoning", "#ebcb8b"},
		{"BUILD", "Full execution", "#a3be8c"},
	}

	for _, mode := range modes {
		modeStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(mode.color)).
			Bold(true)
		wb.WriteString("  ")
		wb.WriteString(modeStyle.Render(mode.key))
		wb.WriteString(m.styles.StatusText.Render(" — " + mode.desc))
		wb.WriteString("\n")
	}

	wb.WriteString("\n")
	wb.WriteString(m.styles.SystemText.Render("  Type a message to start · Press ? for help"))
	wb.WriteString("\n")

	// Center the welcome content horizontally in the available viewport width.
	contentWidth := m.width
	if m.sidePanel.IsVisible() {
		contentWidth = m.width - m.sidePanel.width - 1
	}
	centered := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, wb.String())
	b.WriteString(centered)
}

// renderUserMsg renders a user message block.
func (m *Model) renderUserMsg(b *strings.Builder, content string, contentW int) {
	label := m.styles.UserLabel.Render("you")
	labelW := lipgloss.Width(label)
	ruleW := contentW - labelW - 3
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
	// Render thinking box if present.
	if entry.ThinkingContent != "" {
		thinkBox := m.renderThinkingBox(entry.ThinkingContent, entry.ThinkingCollapsed)
		b.WriteString(indentBlock(thinkBox, "  "))
		b.WriteString("\n")
	}

	label := m.styles.AsstLabel.Render("assistant")
	labelW := lipgloss.Width(label)
	ruleW := contentW - labelW - 3
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
	// Show thinking indicator during streaming.
	if m.thinkBuf.Len() > 0 {
		thinkHint := m.styles.ThinkingHeader.Render(
			fmt.Sprintf("  thinking: %d chars...", m.thinkBuf.Len()),
		)
		b.WriteString(thinkHint)
		b.WriteString("\n")
	}

	label := m.styles.AsstLabel.Render("assistant")
	cursor := m.styles.StreamCursor.Render(" " + m.spin.View())
	labelW := lipgloss.Width(label) + lipgloss.Width(cursor)
	ruleW := contentW - labelW - 3
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

// renderToolGroup renders a tool entry using the fancy tool card component.
func (m *Model) renderToolGroup(b *strings.Builder, toolIdx, entryIdx int) {
	if toolIdx < 0 || toolIdx >= len(m.toolEntries) {
		return
	}
	te := m.toolEntries[toolIdx]
	layout := m.currentLayout()

	// Add spacing before the first tool_group in a sequence.
	if entryIdx > 0 && m.entries[entryIdx-1].Kind != "tool_group" {
		b.WriteString("\n")
	}

	// Find corresponding tool card
	var card *ToolCard
	for i := range m.toolCardMgr.Cards {
		if m.toolCardMgr.Cards[i].Name == te.Name {
			card = &m.toolCardMgr.Cards[i]
			break
		}
	}

	if card != nil {
		// Use fancy tool card rendering
		card.Expanded = !te.Collapsed
		b.WriteString(card.View(m.width - 4))
	} else {
		// Fallback to basic rendering if no card exists
		tt := classifyTool(te.Name)

		switch te.Status {
		case ToolStatusRunning:
			// Running: show spinner with type-specific icon
			icon := m.styles.ToolCallIcon.Render(toolIcon(tt, te.Status))
			spinView := m.spin.View()
			text := m.styles.ToolCallText.Render(fmt.Sprintf(" %s ", te.Name))
			hint := m.styles.ToolRunningText.Render(spinView + " running...")
			b.WriteString(icon + text + hint)
			// For running bash tools, show command inline
			if tt == ToolTypeBash {
				if summary := toolSummary(tt, te); summary != "" {
					b.WriteString("\n")
					b.WriteString(m.styles.ToolBashCmd.Render(layout.ToolIndent + "$ " + summary))
				}
			}
			b.WriteString("\n")

		case ToolStatusDone:
			dur := formatDuration(te.Duration)
			icon := m.styles.ToolDoneIcon.Render(toolIcon(tt, te.Status))
			if te.Collapsed {
				// Collapsed: single line with type-specific summary
				text := m.styles.ToolDoneText.Render(fmt.Sprintf(" %s (%s)", te.Name, dur))
				b.WriteString(icon + text)
				if summary := toolSummary(tt, te); summary != "" {
					summ := truncate(summary, layout.ToolSummaryMax)
					b.WriteString(m.styles.ToolBashCmd.Render(" " + summ))
				}
				b.WriteString("\n")
			} else {
				// Expanded: show args + result (or diff for file writes)
				text := m.styles.ToolDoneText.Render(fmt.Sprintf(" %s (%s)", te.Name, dur))
				b.WriteString(icon + text)
				b.WriteString("\n")
				// Args
				args := truncate(te.Args, layout.ArgsTruncMax)
				b.WriteString(m.styles.ToolDetailText.Render(layout.ToolIndent + "args: " + args))
				b.WriteString("\n")
				// Diff or result
				if te.DiffLines != nil {
					b.WriteString(renderDiff(te.DiffLines, m.styles, 30))
				} else {
					// Use smart result formatting with truncation
					result := formatToolResult(te.Result, 20, layout.ResultTruncMax)
					resultLines := strings.Count(result, "\n") + 1
					if resultLines > 20 {
						b.WriteString(m.styles.ToolDetailText.Render(layout.ToolIndent + "result (truncated, expand to see more):\n"))
						b.WriteString(m.styles.ToolDetailText.Render(indentBlock(truncate(result, layout.ResultTruncMax), layout.ToolIndent)))
					} else {
						b.WriteString(m.styles.ToolDetailText.Render(layout.ToolIndent + "result:\n"))
						b.WriteString(m.styles.ToolDetailText.Render(indentBlock(result, layout.ToolIndent)))
					}
					b.WriteString("\n")
				}
			}

		case ToolStatusError:
			// Error: always expanded regardless of collapse state
			dur := formatDuration(te.Duration)
			icon := m.styles.ToolErrorIcon.Render(toolIcon(tt, te.Status))
			text := m.styles.ToolErrorText.Render(fmt.Sprintf(" %s (%s)", te.Name, dur))
			b.WriteString(icon + text)
			b.WriteString("\n")
			// Error result always shown
			result := truncate(te.Result, layout.ResultTruncMax)
			b.WriteString(m.styles.ToolErrorText.Render(layout.ToolIndent + result))
			b.WriteString("\n")
		}
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

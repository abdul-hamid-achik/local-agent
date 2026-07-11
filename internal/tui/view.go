package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func (m *Model) View() tea.View {
	if !m.ready {
		return tea.NewView("  initializing...")
	}
	if hint := m.narrowTerminalHint(); hint != "" {
		return m.renderNarrowTerminalView(hint)
	}

	var content string

	// Calculate right side width to match viewport
	rightWidth := m.renderedRightPaneWidth()

	// Build the right side: viewport + footer as one unit
	var rightSide strings.Builder
	rightSide.WriteString(m.viewport.View())
	rightSide.WriteString("\n")

	// Divider line (only in chat area, not under sidebar)
	rightSide.WriteString(m.styles.Divider.Render(rule(rightWidth)))
	rightSide.WriteString("\n")

	// Status line.
	rightSide.WriteString(m.renderStatusLine())
	rightSide.WriteString("\n")

	// Input or streaming hint - clean Crush-style
	if m.sessionLoading {
		rightSide.WriteString(m.styles.StreamHint.Render("  " + m.spin.View() + " loading session... press Esc to cancel"))
	} else {
		switch m.state {
		case StateIdle:
			rightSide.WriteString(m.input.View())
		case StateWaiting:
			rightSide.WriteString(m.styles.StreamHint.Render("  " + m.scramble.View() + " thinking... press Esc to cancel"))
		default:
			rightSide.WriteString(m.styles.StreamHint.Render("  " + m.spin.View() + " streaming... press Esc to cancel"))
		}
	}

	// Render side panel + right side horizontally using lipgloss
	if m.sidePanel.IsVisible() {
		panelView := m.sidePanel.View()
		rightContent := rightSide.String()

		// Calculate widths
		panelW := m.sidePanel.width
		rightW := rightWidth

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
		dividerChars := strings.Repeat("│\n", max(0, m.height))
		divider := m.styles.Divider.Render(dividerChars)

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

// renderNarrowTerminalView keeps tiny terminals recoverable. In particular,
// users can hide the sidebar without having to guess why the chat pane vanished.
func (m *Model) renderNarrowTerminalView(hint string) tea.View {
	contentW := max(1, m.width-4)
	title := truncateDisplay("TERMINAL TOO NARROW", contentW)
	body := m.styles.OverlayTitle.Render(title) + "\n\n" +
		m.styles.StatusText.Render(wrapText(hint, contentW))
	if m.sidePanel.IsVisible() && m.width < minTerminalWidthWithSidebar {
		keyHint := "Ctrl+B"
		keyAction := "  hide sidebar"
		if m.state != StateIdle {
			keyHint = "Esc, then Ctrl+B"
			keyAction = "  cancel and hide sidebar"
		}
		body += "\n\n" + m.styles.FocusIndicator.Render(keyHint) +
			m.styles.StatusText.Render(keyAction)
	}
	content := lipgloss.PlaceHorizontal(max(1, m.width), lipgloss.Center, body)
	lineCount := strings.Count(content, "\n") + 1
	top := (m.height - lineCount) / 2
	if top > 0 {
		content = strings.Repeat("\n", top) + content
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "LOCAL AGENT · resize terminal"
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

// renderStatusLine builds the status bar above the input/hint area.
func (m *Model) renderStatusLine() string {
	paneW := m.chatPaneWidth()
	if m.shuttingDown {
		return m.styles.StatusText.Render(truncateDisplay(
			"  Stopping safely… waiting for the active operation receipt", paneW,
		))
	}
	// Pending tool approval prompt overrides normal status.
	if m.pendingApproval != nil {
		args := agent.FormatToolArgs(m.pendingApproval.Args)
		promptText := m.pendingApproval.ToolName
		if args != "" {
			promptText += " " + args
		}
		actions := "? · [y] allow · [n] deny · [a] always · [esc] cancel"
		switch {
		case paneW < 34:
			actions = "? · y/n/a"
		case paneW < 52:
			actions = "? · y/n/a · esc cancel"
		case paneW < 80:
			actions = "? · y allow · n deny · a always"
		}
		fixed := "  ⚡ Allow " + actions
		promptBudget := max(1, paneW-lipgloss.Width(fixed)-1)
		promptText = truncateDisplay(promptText, promptBudget)
		line := fmt.Sprintf("  ⚡ Allow %s%s", promptText, actions)
		return m.styles.ApprovalPrompt.Render(truncateDisplay(line, paneW))
	}

	// Pending paste prompt overrides normal status.
	if m.pendingPaste != "" {
		lines := strings.Count(m.pendingPaste, "\n") + 1
		return m.styles.StatusText.Render(truncateDisplay(
			fmt.Sprintf("  Large paste (%d lines). Wrap as code block? [y/n/esc]", lines), paneW,
		))
	}
	if m.sessionLoading {
		return m.styles.StatusText.Render(truncateDisplay("  Loading saved session…", paneW))
	}
	if m.sessionListing {
		return m.styles.StatusText.Render(truncateDisplay("  Loading saved session list…", paneW))
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
	separator := m.styles.StatusText.Render(" · ")
	line := " " + strings.Join(parts, separator)
	// Optional telemetry drops from the right on compact panes; the mode and
	// readiness state remain discoverable.
	for lipgloss.Width(line) > paneW && len(parts) > 2 {
		parts = parts[:len(parts)-1]
		line = " " + strings.Join(parts, separator)
	}
	return line
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
// maxChatContentWidth caps how wide chat text wraps, so prose stays readable
// on very wide terminals instead of spanning the whole screen.
const maxChatContentWidth = 120

// entriesFromMessages rebuilds the visible chat transcript from a restored
// agent message history. User and assistant text become chat entries; tool
// messages are omitted from the visual (they remain in the agent's context for
// the model) since re-rendering them as cards would need the tool-entry state
// that the snapshot doesn't carry.
func entriesFromMessages(msgs []llm.Message) []ChatEntry {
	var entries []ChatEntry
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			if msg.Content != "" {
				entries = append(entries, ChatEntry{Kind: "user", Content: msg.Content})
			}
		case "assistant":
			if msg.Content != "" {
				entries = append(entries, ChatEntry{Kind: "assistant", Content: msg.Content})
			}
		}
	}
	return entries
}

func (m *Model) renderEntries() string {
	// Calculate content width for text wrapping
	// CRITICAL: This must be <= viewport width to prevent horizontal overflow
	viewportW := m.chatPaneWidth()

	// Content width is viewport width minus padding for margins/borders,
	// capped so lines stay readable on very wide terminals (Crush-style).
	contentW := viewportW - 6 // More conservative padding to prevent overflow
	if contentW < 8 {
		contentW = 8
	}
	if maxAllowed := max(1, viewportW-4); contentW > maxAllowed {
		contentW = maxAllowed
	}
	if contentW > maxChatContentWidth {
		contentW = maxChatContentWidth
	}

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
			switch e.Kind {
			case "system":
				b.WriteString(m.styles.SystemText.Render(wrapText(e.Content, contentW)))
				b.WriteString("\n\n")
			case "error":
				m.renderEntryError(&b, e.Content, contentW)
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
			m.renderEntryError(&b, entry.Content, contentW)
		case "system":
			b.WriteString(m.styles.SystemText.Render(wrapText(entry.Content, contentW)))
			b.WriteString("\n\n")
		}

		// Add spacing between message groups.
		if i < len(m.entries)-1 {
			next := m.entries[i+1]
			curr := entry.Kind
			nextK := next.Kind

			// Consistent spacing between all tool_group entries.
			if curr == "tool_group" {
				// Already added spacing in renderToolGroup, skip here to avoid double spacing
				continue
			} else if curr != nextK {
				b.WriteString("\n")
			}
		}
	}

	// Cache the rendered entries prefix and toolEntryRows.
	m.cachedEntriesRender = b.String()
	m.cachedEntryCount = len(m.entries)
	if m.cachedToolEntryRows == nil {
		m.cachedToolEntryRows = make(map[int]int, 8)
	} else {
		clear(m.cachedToolEntryRows)
	}
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

func (m *Model) renderEntryError(b *strings.Builder, content string, contentW int) {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "The operation failed without an error message."
	}
	b.WriteString(m.styles.ErrorText.Render("✗ error"))
	b.WriteString("\n")
	b.WriteString(m.styles.ToolErrorText.Render(indentBlock(wrapText(content, max(1, contentW-2)), "  ")))
	b.WriteString("\n\n")
}

// renderWelcome renders the empty-state welcome message, centered horizontally.
func (m *Model) renderWelcome(b *strings.Builder) {
	var wb strings.Builder
	contentWidth := m.chatPaneWidth()
	compact := m.currentLayout().HeaderMode == "compact" || contentWidth < 64

	if !compact {
		for _, line := range logoLines() {
			if line == "" {
				wb.WriteString("\n")
				continue
			}
			wb.WriteString(m.styles.OverlayTitle.Render(line))
			wb.WriteString("\n")
		}
	}

	title := "Welcome to LOCAL AGENT"
	if compact {
		title = "LOCAL AGENT"
	}
	wb.WriteString(m.styles.OverlayTitle.Render(title))
	wb.WriteString("\n")
	wb.WriteString(m.styles.StatusText.Render(truncateDisplay(
		"Local-first · Ollama · tool effects ask first", max(1, contentWidth-4),
	)))
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
	} else {
		infoParts = append(infoParts, "no MCP servers")
	}
	if len(infoParts) > 0 {
		wb.WriteString(m.styles.StatusText.Render(truncateDisplay(
			strings.Join(infoParts, " · "), max(1, contentWidth-4),
		)))
		wb.WriteString("\n")
	}

	wb.WriteString("\n")

	if compact {
		modeLabel := m.modeConfigs[m.mode].Label
		wb.WriteString(m.styles.StatusText.Render("Mode "))
		switch m.mode {
		case ModePlan:
			wb.WriteString(m.styles.ModePlan.Render(modeLabel))
		case ModeBuild:
			wb.WriteString(m.styles.ModeBuild.Render(modeLabel))
		default:
			wb.WriteString(m.styles.ModeAsk.Render(modeLabel))
		}
		wb.WriteString("\n")
	} else {
		modes := []struct {
			key   string
			desc  string
			style lipgloss.Style
		}{
			{"ASK", "Quick answers", m.styles.ModeAsk},
			{"PLAN", "Design & reasoning", m.styles.ModePlan},
			{"BUILD", "Full execution", m.styles.ModeBuild},
		}
		for _, mode := range modes {
			wb.WriteString(mode.style.Render(mode.key))
			wb.WriteString(m.styles.StatusText.Render(" — " + mode.desc))
			wb.WriteString("\n")
		}
		wb.WriteString("\n")
	}

	if compact {
		wb.WriteString(m.styles.WelcomeHint.Render("Enter send · ? help"))
		wb.WriteString("\n")
		wb.WriteString(m.styles.StatusText.Render("Shift+Tab mode · Ctrl+B panel"))
		wb.WriteString("\n")
		wb.WriteString(m.styles.StatusText.Render("/ commands · @ files · # skills"))
		wb.WriteString("\n")
	} else {
		wb.WriteString(m.styles.WelcomeHint.Render("Enter send · Shift+Tab mode · ? help"))
		wb.WriteString("\n")
		wb.WriteString(m.styles.StatusText.Render("/ commands · @ files · # skills · Ctrl+B sidebar"))
		wb.WriteString("\n")
	}

	// Center the welcome content horizontally in the available viewport width.
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

	// During streaming: render the stable markdown prefix with Glamour (cached)
	// and only the trailing partial paragraph as plain wrapped text. This shows
	// formatted output live instead of popping into shape on completion, while
	// avoiding the jitter of re-rendering incomplete markdown.
	wrapWidth := contentW - 2
	if wrapWidth < 10 {
		wrapWidth = 10
	}

	var formatted, tail string
	if m.md != nil {
		formatted, tail = m.md.RenderStreamingFormatted(content)
	} else {
		tail = content
	}

	if formatted != "" {
		b.WriteString(indentBlock(strings.TrimRight(formatted, " \t\n"), "  "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(tail) != "" {
		b.WriteString(indentBlock(wrapText(tail, wrapWidth), "  "))
		b.WriteString("\n")
	}
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
	for i := len(m.toolCardMgr.Cards) - 1; i >= 0; i-- {
		if toolCallMatches(te.ID, te.Name, m.toolCardMgr.Cards[i].ID, m.toolCardMgr.Cards[i].Name) {
			card = &m.toolCardMgr.Cards[i]
			break
		}
	}

	if card != nil {
		// Use fancy tool card rendering
		card.Expanded = !te.Collapsed
		// Keep the card inside the actual viewport; the two-column left indent is
		// applied immediately below.
		availableWidth := max(4, m.chatPaneWidth()-4)
		cardView := card.View(availableWidth)
		if card.Expanded && card.State != ToolCardRunning && len(te.DiffLines) > 0 {
			diffView := strings.TrimRight(renderDiffAtWidth(te.DiffLines, m.styles, 30, availableWidth), "\n")
			if diffView != "" {
				cardView += "\n" + diffView
			}
		}
		// Add left padding to align with message content
		cardView = indentBlock(cardView, "  ")
		b.WriteString(cardView)
		b.WriteString("\n\n") // Add vertical spacing between tool cards
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
					b.WriteString(renderDiffAtWidth(te.DiffLines, m.styles, 30, max(1, m.chatPaneWidth()-4)))
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

// truncateDisplay truncates plain text by terminal cell width instead of byte
// count, so model names, paths, and tool output containing Unicode stay valid.
func truncateDisplay(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	const ellipsis = "…"
	if maxWidth <= lipgloss.Width(ellipsis) {
		return ellipsis
	}

	budget := maxWidth - lipgloss.Width(ellipsis)
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > budget {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + ellipsis
}

// wrapText wraps text to the given width, breaking long words if needed.
func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	// Fast path: if a single line fits in terminal cells, return as-is.
	if !strings.Contains(s, "\n") && lipgloss.Width(s) <= width {
		return s
	}
	var result strings.Builder
	for _, line := range strings.Split(s, "\n") {
		result.WriteString(wrapLine(line, width))
		result.WriteString("\n")
	}
	// Trim trailing newline
	return strings.TrimSuffix(result.String(), "\n")
}

// wrapLine wraps a single line to the given width, breaking long words if needed.
func wrapLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return ""
	}

	lines := make([]string, 0, len(words))
	current := ""
	for _, w := range words {
		if current != "" && lipgloss.Width(current)+1+lipgloss.Width(w) <= width {
			current += " " + w
			continue
		}
		if current != "" {
			lines = append(lines, current)
			current = ""
		}

		chunks := splitDisplayChunks(w, width)
		if len(chunks) == 0 {
			continue
		}
		if len(chunks) > 1 {
			lines = append(lines, chunks[:len(chunks)-1]...)
		}
		current = chunks[len(chunks)-1]
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

// splitDisplayChunks splits one long word without slicing through UTF-8 and
// measures terminal cells, which matters for CJK and emoji model output.
func splitDisplayChunks(word string, width int) []string {
	if word == "" || width <= 0 {
		return nil
	}
	var chunks []string
	var chunk strings.Builder
	used := 0
	for _, r := range word {
		rw := lipgloss.Width(string(r))
		if used > 0 && used+rw > width {
			chunks = append(chunks, chunk.String())
			chunk.Reset()
			used = 0
		}
		chunk.WriteRune(r)
		used += rw
		if used >= width {
			chunks = append(chunks, chunk.String())
			chunk.Reset()
			used = 0
		}
	}
	if chunk.Len() > 0 {
		chunks = append(chunks, chunk.String())
	}
	return chunks
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

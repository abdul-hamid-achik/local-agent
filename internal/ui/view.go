package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func (m *Model) View() tea.View {
	if !m.ready {
		return tea.NewView("  initializing...")
	}
	if hint := m.narrowTerminalHint(); hint != "" {
		return m.renderNarrowTerminalView(hint)
	}

	// The conversation, status, and composer own the complete terminal width.
	// Infrequent controls are exposed through overlays instead of persistent
	// chrome that competes with code and tool output.
	paneWidth := m.chatPaneWidth()
	var content strings.Builder
	var viewCursor *tea.Cursor
	content.WriteString(m.viewport.View())
	content.WriteString("\n")
	content.WriteString(m.styles.Divider.Render(rule(paneWidth)))
	content.WriteString("\n")

	if status := m.renderStatusLine(); status != "" {
		content.WriteString(status)
		content.WriteString("\n")
	}

	// The composer is replaced by one width-tiered liveness line while work is
	// active. Idle gets the complete textarea back without a blank status row.
	if m.pendingApproval != nil || m.pendingPaste != nil {
		// The status prompt above owns the footer until the user answers.
	} else if m.composerIsBusy() {
		content.WriteString(m.renderWorkingLine())
	} else {
		// Render a local copy with Bubbles' virtual cursor disabled. The same
		// copy supplies the one real cursor owned by this top-level view.
		input := m.input
		input.SetVirtualCursor(false)
		composerY := strings.Count(content.String(), "\n")
		content.WriteString(input.View())
		viewCursor = offsetCursor(input.Cursor(), 0, composerY)
	}

	// Render overlays on top (centered modal) using overlayOnContent
	if m.overlay != OverlayNone {
		var overlay string
		var localCursor *tea.Cursor
		// Every overlay suppresses the underlying composer cursor. Text-entry
		// overlays may replace it with their own translated child cursor below.
		viewCursor = nil
		switch m.overlay {
		case OverlayHelp:
			overlay = m.renderHelpOverlay(m.width)
		case OverlayCompletion:
			if m.isCompletionActive() {
				overlay, localCursor = m.renderCompletionModalView()
			}
		case OverlayModelPicker:
			if m.modelPickerState != nil {
				overlay = m.renderModelPicker()
			}
		case OverlayPlanForm:
			if m.planFormState != nil {
				overlay, localCursor = m.renderPlanFormView()
			}
		case OverlaySessionsPicker:
			if m.sessionsPickerState != nil {
				overlay = m.renderSessionsPicker()
			}
		case OverlaySettings:
			overlay = m.renderSettingsPicker()
		case OverlayAgentPicker:
			overlay = m.renderAgentPicker()
		case OverlayModePicker:
			overlay = m.renderModePicker()
		case OverlayRuntimeStatus:
			overlay = m.renderRuntimeStatus()
		}
		if overlay != "" {
			base := content.String()
			content.Reset()
			content.WriteString(m.overlayOnContent(base, overlay))
			viewCursor = overlayCursor(base, overlay, m.width, localCursor)
		}
	}

	v := tea.NewView(content.String() + "\n")
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.Cursor = viewCursor

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

// renderNarrowTerminalView keeps tiny terminals recoverable.
func (m *Model) renderNarrowTerminalView(hint string) tea.View {
	contentW := max(1, m.width-4)
	title := truncateDisplay("TERMINAL TOO NARROW", contentW)
	body := m.styles.OverlayTitle.Render(title) + "\n\n" +
		m.styles.StatusText.Render(wrapText(hint, contentW))
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
	view, _ := m.renderCompletionModalView()
	return view
}

func (m *Model) renderCompletionModalView() (string, *tea.Cursor) {
	cs := m.completionState
	if cs == nil {
		return "", nil
	}
	contentW := pickerListWidth(m.width, 60)
	filter := cs.Filter
	filter.SetVirtualCursor(false)
	filter.SetWidth(completionFilterInputWidth(m.width))

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
	b.WriteString(m.styles.OverlayTitle.Render(truncateDisplay(title, contentW)))
	b.WriteString("\n")

	// Filter input
	filterY := strings.Count(b.String(), "\n")
	b.WriteString(m.styles.FocusIndicator.Render(completionFilterPrompt))
	filterX := lipgloss.Width(completionFilterPrompt)
	b.WriteString(filter.View())
	b.WriteString("\n")
	filterCursor := offsetCursor(filter.Cursor(), filterX, filterY)

	// Breadcrumb for @ file browsing
	if cs.Kind == "attachments" && cs.CurrentPath != "" {
		b.WriteString(m.styles.CompletionCategory.Render(truncateDisplay(cs.CurrentPath+"/", contentW)))
		b.WriteString("\n")
	}

	// Divider
	b.WriteString(m.styles.FocusIndicator.Render(strings.Repeat("─", contentW)))
	b.WriteString("\n")

	// Scrollable items (max 10 visible)
	fixedRows := 6 // title, filter, divider, footer, and border rows
	if cs.Kind == "attachments" && cs.CurrentPath != "" {
		fixedRows++
	}
	if cs.Searching {
		fixedRows++
	}
	maxVisible := min(10, max(1, m.height-fixedRows))
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

			category := ""
			if cs.Kind == "attachments" {
				category = "  " + item.Category
			}
			labelWidth := max(1, contentW-2-lipgloss.Width(category)-lipgloss.Width(selectedMark))
			label := truncateDisplay(item.Label, labelWidth)
			description := ""
			if cs.Kind == "command" && strings.TrimSpace(item.Description) != "" {
				remaining := labelWidth - lipgloss.Width(label)
				if remaining >= 6 {
					description = " · " + truncateDisplay(strings.TrimSpace(item.Description), remaining-3)
				}
			}
			cat := m.styles.CompletionCategory.Render(category)
			desc := m.styles.CompletionCategory.Render(description)

			if i == cs.Index {
				b.WriteString(prefix + m.styles.FocusIndicator.Render(label) + desc + cat + selectedMark)
			} else {
				b.WriteString(prefix + label + desc + cat + selectedMark)
			}
			b.WriteString("\n")
		}
	}

	// Searching indicator
	if cs.Searching {
		b.WriteString(m.styles.CompletionSearching.Render(truncateDisplay("  searching...", contentW)))
		b.WriteString("\n")
	}

	// Footer hints use the same priority grammar as every other modal.
	hints := []keyHint{
		{Key: m.keys.Cancel.Help().Key, Action: "cancel"},
		{Key: m.keys.CompleteSelect.Help().Key, Action: "select"},
		{Key: "↑/↓", Action: "move"},
	}
	if cs.Kind == "attachments" && cs.CurrentPath != "" {
		hints = append(hints, keyHint{Key: "backspace", Action: "up"})
	}
	if cs.Selected != nil {
		hints = append(hints, keyHint{Key: m.keys.CompleteToggle.Help().Key, Action: "toggle"})
	}
	return m.renderPickerFrame(b.String(), 60, m.renderKeyHints(contentW, hints...)), pickerFrameCursor(filterCursor)
}

// renderStatusLine builds the status bar above the input/hint area.
func (m *Model) renderStatusLine() string {
	paneW := m.chatPaneWidth()
	// Pending tool approval prompt overrides normal status.
	if m.pendingApproval != nil {
		args := FormatToolArgs(m.pendingApproval.Args)
		promptText := m.pendingApproval.ToolName
		if args != "" {
			promptText += " " + args
		}
		return m.renderDecisionPrompt(
			"Approve", promptText,
			keyHint{Key: "esc", Action: "cancel"},
			keyHint{Key: "y", Action: "allow"},
			keyHint{Key: "n", Action: "deny"},
			keyHint{Key: "a", Action: "always"},
		)
	}

	// Pending paste prompt overrides normal status.
	if m.pendingPaste != nil {
		pending := m.pendingPaste
		switch {
		case !pending.PlainFits:
			return m.renderDecisionPrompt(
				"Paste too large", pending.descriptor(),
				keyHint{Key: "esc", Action: "dismiss"},
				keyHint{Action: "use @file or /load"},
			)
		case !pending.FencedFits:
			return m.renderDecisionPrompt(
				"Large paste", pending.descriptor()+" · plain only",
				keyHint{Key: "esc", Action: "cancel"},
				keyHint{Key: "y", Action: "plain"},
			)
		default:
			return m.renderDecisionPrompt(
				"Large paste", pending.descriptor(),
				keyHint{Key: "esc", Action: "cancel"},
				keyHint{Key: "y", Action: "code"},
				keyHint{Key: "n", Action: "plain"},
			)
		}
	}
	if m.followPaused() && m.state == StateIdle && !m.composerIsBusy() {
		return m.renderFollowPausedStatus(paneW)
	}
	if m.state != StateIdle || m.composerIsBusy() {
		return ""
	}
	conversationStarted := m.conversationStarted()
	hasNotice := m.hasTranscriptNotice()
	noticeNeedsRecovery := hasNotice && (paneW < 36 || m.height < 16)
	if !conversationStarted && !noticeNeedsRecovery && len(m.failedServers) == 0 && !m.doneFlash && (m.promptTokens <= 0 || m.numCtx <= 0) {
		// The empty-state orientation already carries mode, model, and Settings.
		// Repeating them immediately above the composer only adds visual noise.
		return ""
	}

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
	modeLabel := cfg.Label
	if paneW >= 40 {
		modeLabel = "[ " + modeLabel + " ]"
	}
	parts := []string{modeStyle.Render(modeLabel)}
	if !conversationStarted && noticeNeedsRecovery {
		// Startup and recovery notices can push the empty-state hints out of a
		// minimum-height viewport. Keep the Settings recovery path in the fixed
		// footer until a real conversation begins.
		parts = append(parts, m.styles.FocusIndicator.Render("ctrl+p settings"))
	}

	if failures := len(m.failedServers); failures > 0 {
		label := "⚠ connection failed"
		if failures > 1 {
			label = fmt.Sprintf("⚠ %d connections failed", failures)
		}
		parts = append(parts, m.styles.ErrorText.UnsetPaddingLeft().Render(label))
	}
	if m.doneFlash {
		done := "✓ Done"
		if m.lastTurnDuration > 0 {
			done += " · " + formatWorkingElapsed(m.lastTurnDuration)
		}
		parts = append(parts, m.styles.StatusCheck.UnsetPaddingLeft().Render(done))
	}

	contextStatus := m.renderContextStatus(paneW < 80)
	contextHigh := m.numCtx > 0 && m.promptTokens*100/m.numCtx >= 75
	if contextHigh && contextStatus != "" {
		parts = append(parts, contextStatus)
	}
	if m.model != "" {
		parts = append(parts, m.styles.StatusText.Render(m.model))
	}
	if paneW >= 80 && m.agentProfile != "" {
		parts = append(parts, m.styles.StatusText.Render("@"+m.agentProfile))
	}
	if !contextHigh && contextStatus != "" {
		parts = append(parts, contextStatus)
	}
	if paneW >= 58 && conversationStarted {
		parts = append(parts, m.styles.FocusIndicator.Render("ctrl+p settings"))
	}

	separator := m.styles.StatusText.Render(" · ")
	line := " " + strings.Join(parts, separator)
	// Drop optional metadata from the right. Mode and operational failure are
	// first, so they survive every supported width tier.
	for lipgloss.Width(line) > paneW && len(parts) > 2 {
		parts = parts[:len(parts)-1]
		line = " " + strings.Join(parts, separator)
	}
	if lipgloss.Width(line) > paneW {
		// The remaining two semantic parts use bounded short labels, so this is
		// only a final guard for unusually wide mode/profile glyphs.
		plain := cfg.Label
		if len(m.failedServers) > 0 {
			plain += fmt.Sprintf(" · ⚠ %d failed", len(m.failedServers))
		}
		return m.styles.StatusText.Render(truncateDisplay(" "+plain, paneW))
	}
	return line
}

func (m *Model) conversationStarted() bool {
	for _, entry := range m.entries {
		switch entry.Kind {
		case "user", "assistant", "tool_group":
			return true
		}
	}
	return false
}

func (m *Model) hasTranscriptNotice() bool {
	for _, entry := range m.entries {
		if entry.Kind == "system" || entry.Kind == "error" {
			return true
		}
	}
	return false
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
	contentW := m.chatContentWidth()

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
	if !hasUserMsg && !m.hasLiveTurn() {
		var b strings.Builder
		m.renderWelcome(&b)
		hasNotice := false
		for _, entry := range m.entries {
			if entry.Kind == "system" || entry.Kind == "error" {
				hasNotice = true
				break
			}
		}
		if !hasNotice {
			welcome := strings.TrimRight(b.String(), "\n")
			top := max(0, (m.viewport.Height()-lipgloss.Height(welcome))/2)
			return strings.Repeat("\n", top) + welcome
		}
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
		m.toolHitRegions = append(m.toolHitRegions[:0], m.cachedToolHitRegions...)
		if m.hasLiveTurn() {
			var b strings.Builder
			b.WriteString(m.cachedEntriesRender)
			if m.cachedEntriesRender != "" && len(m.entries) > 0 {
				last := m.entries[len(m.entries)-1]
				b.WriteString(transcriptEntrySeparator(last.Kind, "assistant"))
			}
			m.renderStreamingMsg(&b, m.streamBuf.String(), contentW)
			return b.String()
		}
		return m.cachedEntriesRender
	}

	// Full render: iterate all entries.
	var b strings.Builder
	m.toolHitRegions = m.toolHitRegions[:0]
	renderedLines := 0
	previousKind := ""
	renderedAny := false

	for _, entry := range m.entries {
		var entryView strings.Builder
		switch entry.Kind {
		case "user":
			m.renderUserMsg(&entryView, entry.Content, contentW)
		case "assistant":
			m.renderAssistantMsg(&entryView, entry, contentW)
		case "tool_group":
			m.renderToolGroup(&entryView, entry.ToolIndex)
		case "error":
			m.renderEntryError(&entryView, entry.Content, contentW)
		case "system":
			entryView.WriteString(m.styles.SystemText.Render(wrapText(entry.Content, contentW)))
			entryView.WriteString("\n")
		}
		chunk := strings.TrimRight(entryView.String(), "\n")
		if chunk == "" {
			continue
		}

		if renderedAny {
			separator := transcriptEntrySeparator(previousKind, entry.Kind)
			b.WriteString(separator)
			renderedLines += strings.Count(separator, "\n")
		}
		if entry.Kind == "tool_group" {
			header, _, _ := strings.Cut(chunk, "\n")
			m.toolHitRegions = append(m.toolHitRegions, toolHitRegion{
				ToolIndex: entry.ToolIndex,
				Row:       renderedLines,
				EndCol:    lipgloss.Width(header),
			})
		}
		b.WriteString(chunk)
		renderedLines += strings.Count(chunk, "\n")
		previousKind = entry.Kind
		renderedAny = true
	}

	// Cache the rendered entries prefix and exact ToolCard header targets.
	m.cachedEntriesRender = b.String()
	m.cachedEntryCount = len(m.entries)
	m.cachedToolHitRegions = append(m.cachedToolHitRegions[:0], m.toolHitRegions...)
	m.entryCacheValid = true

	// Render current streaming content (plain text, no Glamour).
	if m.hasLiveTurn() {
		if renderedAny {
			b.WriteString(transcriptEntrySeparator(previousKind, "assistant"))
		}
		m.renderStreamingMsg(&b, m.streamBuf.String(), contentW)
	}

	return b.String()
}

func (m *Model) hasLiveTurn() bool {
	return m.streamBuf.Len() > 0 || m.thinkBuf.Len() > 0
}

// transcriptEntrySeparator is the single owner of vertical rhythm between
// transcript entries. Consecutive compact receipts form a dense stack; every
// other semantic boundary gets exactly one blank row.
func transcriptEntrySeparator(previous, current string) string {
	if previous == "tool_group" && current == "tool_group" {
		return "\n"
	}
	if previous == "system" && current == "system" {
		return "\n"
	}
	return "\n\n"
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

// renderWelcome renders a compact empty-state orientation surface. Persistent
// runtime detail belongs in Settings; this view teaches only the active mode,
// model, safety boundary, and the shortest paths into work.
func (m *Model) renderWelcome(b *strings.Builder) {
	var wb strings.Builder
	contentWidth := m.chatPaneWidth()
	micro := contentWidth < 36
	compact := contentWidth < 58
	lineWidth := max(1, contentWidth-2)
	writeLine := func(style lipgloss.Style, text string) {
		wb.WriteString(style.Render(truncateDisplay(text, lineWidth)))
		wb.WriteByte('\n')
	}

	writeLine(m.styles.OverlayTitle, "LOCAL AGENT")
	trust := "Local-first · Ollama · tool effects ask first"
	if micro {
		trust = "Local-first · approvals on"
	} else if compact {
		trust = "Local-first · tool effects ask"
	}
	writeLine(m.styles.StatusText, trust)

	var infoParts []string
	if m.initializing {
		infoParts = append(infoParts, "Starting local services…")
	} else {
		infoParts = append(infoParts, m.modeConfigs[m.mode].Label)
		if m.model != "" {
			infoParts = append(infoParts, m.model)
		}
	}
	if len(infoParts) > 0 {
		writeLine(m.styles.StatusText, strings.Join(infoParts, " · "))
	}
	if m.initializing && len(m.startupItems) > 0 {
		for _, item := range m.startupItems {
			icon := m.spin.View()
			switch item.Status {
			case "connected":
				icon = m.styles.StatusDot.Render("✓")
			case "failed":
				icon = m.styles.ErrorText.Render("!")
			}
			line := icon + " " + sanitizeStartupDetail(item.Label)
			if detail := sanitizeStartupDetail(item.Detail); detail != "" {
				line += " · " + detail
			}
			writeLine(m.styles.StatusText, line)
		}
	}

	if micro {
		writeLine(m.styles.WelcomeHint, "enter · ctrl+p settings")
		writeLine(m.styles.StatusText, "? help · / @ #")
	} else if compact {
		writeLine(m.styles.WelcomeHint, "enter send · ctrl+p settings · ? help")
		writeLine(m.styles.StatusText, "/ commands · @ files · # skills")
	} else {
		writeLine(m.styles.WelcomeHint, "enter send · / commands · ctrl+p settings · ? help")
		writeLine(m.styles.StatusText, "shift+tab mode · ctrl+m model · @ files · # skills")
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
	label := m.styles.AsstLabel.Render("assistant")
	labelW := lipgloss.Width(label)
	ruleW := contentW - labelW - 3
	if ruleW < 4 {
		ruleW = 4
	}
	b.WriteString(label + " " + m.styles.RoleRule.Render(rule(ruleW)))
	b.WriteString("\n")

	// Reasoning belongs to this assistant turn, so its disclosure follows the
	// role header instead of appearing as an unowned block above it.
	if entry.ThinkingContent != "" {
		thinkBox := m.renderThinkingBox(entry.ThinkingContent, entry.ThinkingCollapsed)
		b.WriteString(indentBlock(thinkBox, "  "))
		b.WriteString("\n")
	}

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
	activity := "•"
	if !m.reducedMotion {
		activity = m.spin.View()
	}
	cursor := m.styles.StreamCursor.Render(" " + activity)
	labelW := lipgloss.Width(label) + lipgloss.Width(cursor)
	ruleW := contentW - labelW - 3
	if ruleW < 4 {
		ruleW = 4
	}
	b.WriteString(label + cursor + " " + m.styles.RoleRule.Render(rule(ruleW)))
	b.WriteString("\n")

	// Live reasoning uses the same assistant-owned hierarchy as the completed
	// disclosure. Keeping it compact prevents token-by-token height jitter; the
	// full receipt becomes expandable only after the turn settles.
	if m.thinkBuf.Len() > 0 {
		b.WriteString(indentBlock(m.renderLiveThinkingBox(), "  "))
		b.WriteString("\n")
	}

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

// renderToolGroup renders one tight tool receipt. The parent transcript owns
// all spacing between this block and its neighbors.
func (m *Model) renderToolGroup(b *strings.Builder, toolIdx int) {
	if toolIdx < 0 || toolIdx >= len(m.toolEntries) {
		return
	}
	te := m.toolEntries[toolIdx]
	layout := m.currentLayout()

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
		if card.State == ToolCardRunning {
			glyph := "•"
			if !m.reducedMotion {
				glyph = m.spin.View()
			}
			elapsed := m.nowTime().Sub(card.StartTime)
			if elapsed < 0 {
				elapsed = 0
			}
			cardView = card.ViewWithActivity(availableWidth, glyph, elapsed)
		}
		if card.Expanded && card.State != ToolCardRunning && len(te.DiffLines) > 0 {
			diffView := strings.TrimRight(renderDiffAtWidth(te.DiffLines, m.styles, 30, availableWidth), "\n")
			if diffView != "" {
				cardView += "\n" + diffView
			}
		}
		// Add left padding to align with message content
		cardView = indentBlock(cardView, "  ")
		b.WriteString(cardView)
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

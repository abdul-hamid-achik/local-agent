package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// renderStatusLine builds the status bar above the input/hint area.
func (m *Model) renderStatusLine() string {
	paneW := m.chatPaneWidth()
	// Approval owns the complete inline composer surface and is rendered by
	// View, so the ordinary status line stays quiet behind it.
	if m.pendingApproval != nil {
		return ""
	}
	if m.readScopePrompt != nil {
		return ""
	}
	// Completion uses the full composer-owned footer budget for the popup and
	// the still-visible draft; its own key footer carries the active guidance.
	if m.overlay == OverlayCompletion && m.isCompletionActive() {
		return ""
	}
	// The Cortex decision frame carries its own state and key guidance, including
	// in-flight liveness, so no competing working/status footer is rendered.
	if m.cortexDecisionActive() {
		return ""
	}
	// Structured Plan and Goal editing own the same footer region. Their field
	// labels and key footer are the complete active status while open.
	if m.inlineFormActive() {
		return ""
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
	if m.pendingSessionSwitch != nil && m.pendingSessionSwitch.Choice == sessionSwitchUndecided {
		return m.renderSessionSwitchPrompt(paneW)
	}
	if m.followPaused() && m.state == StateIdle && !m.composerIsBusy() {
		return m.renderFollowPausedStatus(paneW)
	}
	if m.state != StateIdle || m.composerIsBusy() {
		return m.renderWorkingLine()
	}
	if m.standaloneRecovery != nil {
		titleLimit := 0
		if paneW >= 72 {
			titleLimit = 24
		}
		return m.renderDecisionPrompt(
			"Recovery paused", sessionDisplayLabel(m.sessionID, m.activeSessionTitle, titleLimit),
			keyHint{Key: "/recover", Action: "inspect"},
		)
	}
	if len(m.pendingImages) > 0 {
		return m.renderPendingImagesStatus(paneW)
	}
	if summary, ok := m.goalStatusSummary(); ok {
		return m.renderGoalFooterStatus(summary, paneW)
	}
	conversationStarted := m.conversationStarted()
	hasNotice := m.hasTranscriptNotice()
	noticeNeedsRecovery := hasNotice && (paneW < 36 || m.height < 16)
	if !conversationStarted && !noticeNeedsRecovery && len(m.failedServers) == 0 && !m.skipApprovalsEnabled() && m.footerNotice == nil && (m.promptTokens <= 0 || m.numCtx <= 0) {
		// The empty-state orientation already carries mode, model, and Settings.
		// Repeating them immediately above the composer only adds visual noise.
		return ""
	}

	presentedMode := m.presentedMode()
	cfg := m.modeConfigs[presentedMode]
	var modeStyle lipgloss.Style
	switch presentedMode {
	case ModeNormal:
		modeStyle = m.styles.ModeAsk
	case ModePlan:
		modeStyle = m.styles.ModePlan
	case ModeAuto:
		modeStyle = m.styles.ModeBuild
	}
	modeLabel := cfg.Label
	if paneW >= 40 {
		modeLabel = "[ " + modeLabel + " ]"
	}
	parts := make([]string, 0, 7)
	if presentedMode != ModeNormal {
		parts = append(parts, modeStyle.Render(modeLabel))
	}
	if m.skipApprovalsEnabled() {
		parts = append(parts, m.styles.StatusWarning.Render("approval prompts skipped"))
	}
	if !conversationStarted && noticeNeedsRecovery {
		// Startup and recovery notices can push the empty-state hints out of a
		// minimum-height viewport. Keep the Settings recovery path in the fixed
		// footer until a real conversation begins.
		parts = append(parts, m.styles.FocusIndicator.Render("ctrl+p settings"))
	}

	if failures := len(m.failedServers); failures > 0 {
		label := mcpUnavailableStatusLabel(failures)
		parts = append(parts, m.styles.ErrorText.UnsetPaddingLeft().Render(label))
	}
	if notice := m.footerNotice; notice != nil {
		parts = append(parts, m.footerNoticeStyle(notice.severity).Render(notice.text))
		if receiptAction, ok := m.inspectableToolReceiptAction(); notice.severity == noticeSuccess &&
			paneW >= 58 && strings.TrimSpace(m.input.Value()) == "" && ok {
			parts = append(parts,
				m.styles.FocusIndicator.Render(m.keys.ToggleFocusedTool.Help().Key)+
					" "+m.styles.StatusText.Render(receiptAction),
			)
		}
	}
	if session := sessionDisplayLabel(m.sessionID, m.activeSessionTitle, sessionStatusTitleLimit(paneW)); session != "" {
		parts = append(parts, m.styles.StatusText.Render(session))
	}

	contextStatus := m.renderContextStatus(paneW < 80)
	contextHigh := m.numCtx > 0 && m.promptTokens*100/m.numCtx >= 75
	if contextHigh && contextStatus != "" {
		parts = append(parts, contextStatus)
	}
	if model := m.currentModelSurfaceLabel(paneW < 58); model != "" {
		parts = append(parts, m.styles.StatusText.Render(model))
	}
	if profile := sanitizeTerminalSingleLine(m.agentProfile); paneW >= 80 && profile != "" {
		parts = append(parts, m.styles.StatusText.Render("@"+profile))
	}
	if !contextHigh && contextStatus != "" {
		parts = append(parts, contextStatus)
	}
	if paneW >= 58 && conversationStarted {
		// Persistent, compact discoverability: the welcome hints vanish after
		// the first turn, so the idle footer keeps the highest-value controls
		// visible. Width-tier packing below drops these before safety posture.
		parts = append(parts, m.styles.FocusIndicator.Render("ctrl+p")+" "+m.styles.StatusText.Render("settings"))
		if paneW >= 88 {
			parts = append(parts, m.styles.FocusIndicator.Render("/")+" "+m.styles.StatusText.Render("commands"))
		}
		if paneW >= 100 {
			parts = append(parts, m.styles.FocusIndicator.Render("?")+" "+m.styles.StatusText.Render("help"))
		}
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
		// Preserve every compact safety boundary instead of truncating a single
		// concatenated string from the right. Combined Cloud, MCP, and approval
		// posture can legitimately need a second row at the minimum width.
		compact := make([]string, 0, 4)
		if presentedMode != ModeNormal {
			compact = append(compact, modeStyle.Render(cfg.Label))
		}
		if m.skipApprovalsEnabled() {
			compact = append(compact, m.styles.StatusWarning.Render("no prompts"))
		}
		if len(m.failedServers) > 0 {
			compact = append(compact, m.styles.ErrorText.UnsetPaddingLeft().Render("MCP unavailable"))
		}
		if m.currentModelIsNonLocal() {
			boundary := strings.Fields(m.currentModelSurfaceLabel(true))[0]
			compact = append(compact, m.styles.StatusText.Render(boundary))
		}
		if session := sessionDisplayLabel(m.sessionID, "", 0); session != "" {
			compact = append(compact, m.styles.StatusText.Render(session))
		}
		return renderPackedStatusRows(paneW, compact, separator)
	}
	return line
}

func sessionStatusTitleLimit(paneW int) int {
	if paneW < 72 {
		return 0
	}
	return min(32, max(16, paneW/3))
}

// renderGoalFooterStatus keeps Goal Runtime additive: progress joins the
// normal mode/model/context grammar instead of replacing it. Optional metadata
// yields from the right while mode and a useful goal label survive every
// supported width tier.
func (m *Model) renderGoalFooterStatus(summary GoalSummary, paneW int) string {
	if paneW <= 1 {
		return ""
	}
	available := paneW - 1 // preserve the status row's leading breathing cell
	// An attached Goal Runtime always dispatches with AUTO authority. m.mode is
	// only the ambient selection for future non-goal turns, so it must not tint
	// or label this active-goal status row.
	cfg := m.modeConfigs[ModeAuto]
	modeStyle := m.styles.ModeBuild
	modeLabel := cfg.Label
	if paneW >= 48 {
		modeLabel = "[ " + modeLabel + " ]"
	}
	modePart := modeStyle.Render(modeLabel)
	separator := m.styles.StatusText.Render(" · ")

	type metadataPart struct {
		view string
	}
	required := make([]metadataPart, 0, 3)
	if m.skipApprovalsEnabled() {
		label := "approval prompts skipped"
		if paneW < 58 {
			label = "no prompts"
		}
		required = append(required, metadataPart{view: m.styles.StatusWarning.Render(label)})
	}
	contextStatus := m.renderContextStatus(paneW < 80)
	contextHigh := m.numCtx > 0 && m.promptTokens*100/m.numCtx >= 75
	if failures := len(m.failedServers); failures > 0 {
		label := "MCP unavailable"
		if paneW >= 58 {
			label = mcpUnavailableStatusLabel(failures)
		}
		required = append(required, metadataPart{view: m.styles.ErrorText.UnsetPaddingLeft().Render(label)})
	}
	if m.currentModelIsNonLocal() {
		boundary := m.currentModelSurfaceLabel(true)
		if paneW < 58 {
			boundary = strings.Fields(boundary)[0]
		}
		required = append(required, metadataPart{view: m.styles.StatusText.Render(boundary)})
	}
	if session := sessionDisplayLabel(m.sessionID, "", 0); session != "" {
		required = append(required, metadataPart{view: m.styles.StatusText.Render(session)})
	}

	optional := make([]metadataPart, 0, 5)
	// The goal label already names the work. At roomy widths add only the
	// compact durable handle; repeating the session title would squeeze the
	// goal phase, budget, and objective that matter more here.
	if contextHigh && contextStatus != "" {
		optional = append(optional, metadataPart{view: contextStatus})
	}
	if model := m.currentModelSurfaceLabel(false); model != "" && !m.currentModelIsNonLocal() {
		optional = append(optional, metadataPart{view: m.styles.StatusText.Render(truncateDisplay(model, 20))})
	}
	if !contextHigh && contextStatus != "" {
		optional = append(optional, metadataPart{view: contextStatus})
	}
	if notice := m.footerNotice; notice != nil {
		optional = append(optional, metadataPart{view: m.footerNoticeStyle(notice.severity).Render(notice.text)})
	}

	const minimumGoalWidth = 12
	fixedWidth := lipgloss.Width(modePart)
	if modePart != "" {
		fixedWidth += lipgloss.Width(separator)
	}
	requiredWidth := 0
	for _, candidate := range required {
		requiredWidth += lipgloss.Width(separator) + lipgloss.Width(candidate.view)
	}
	if len(required) > 0 && available-fixedWidth-requiredWidth < minimumGoalWidth {
		goalWidth := max(1, available-fixedWidth)
		core := " " + strings.Join([]string{modePart, RenderGoalStatusLine(summary, goalWidth, m.isDark)}, separator)
		safety := make([]string, 0, len(required))
		for _, candidate := range required {
			safety = append(safety, candidate.view)
		}
		return truncateDisplay(core, paneW) + "\n" + renderPackedStatusRows(paneW, safety, separator)
	}

	selected := make([]string, 0, len(required)+len(optional))
	for _, candidate := range required {
		selected = append(selected, candidate.view)
		fixedWidth += lipgloss.Width(separator) + lipgloss.Width(candidate.view)
	}
	for _, candidate := range optional {
		cost := lipgloss.Width(separator) + lipgloss.Width(candidate.view)
		if available-fixedWidth-cost < minimumGoalWidth {
			continue
		}
		selected = append(selected, candidate.view)
		fixedWidth += cost
	}
	goalWidth := max(1, available-fixedWidth)
	goalPart := RenderGoalStatusLine(summary, goalWidth, m.isDark)
	parts := make([]string, 0, 2+len(selected))
	if modePart != "" {
		parts = append(parts, modePart)
	}
	parts = append(parts, goalPart)
	parts = append(parts, selected...)
	line := " " + strings.Join(parts, separator)
	return truncateDisplay(line, paneW)
}

// renderPackedStatusRows keeps short host-authored status tokens intact while
// packing them into the fewest width-safe rows. It is reserved for compact
// safety fallbacks where dropping a rightmost token would hide active authority
// or a remote-execution boundary.
func renderPackedStatusRows(width int, parts []string, separator string) string {
	if width <= 0 || len(parts) == 0 {
		return ""
	}
	available := max(1, width-1)
	rows := make([]string, 0, 2)
	current := ""
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		part = truncateDisplay(part, available)
		candidate := part
		if current != "" {
			candidate = current + separator + part
		}
		if current != "" && lipgloss.Width(candidate) > available {
			rows = append(rows, " "+current)
			current = part
			continue
		}
		current = candidate
	}
	if current != "" {
		rows = append(rows, " "+current)
	}
	return strings.Join(rows, "\n")
}

func mcpUnavailableStatusLabel(count int) string {
	return fmt.Sprintf("⚠ %d MCP %s unavailable", count, pluralizeServer(count))
}

func (m *Model) hasTranscriptNotice() bool {
	for _, entry := range m.entries {
		if entry.Kind == "system" || entry.Kind == "error" {
			return true
		}
	}
	return false
}

package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/local-agent/internal/imageasset"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func (m *Model) View() tea.View {
	if !m.ready {
		// Bubble Tea has not delivered terminal dimensions yet, so centering would
		// be guesswork. Keep the same product identity and startup language as the
		// full shell instead of flashing an unrelated debug placeholder.
		v := tea.NewView("LOCAL AGENT\nStarting…")
		v.AltScreen = true
		v.WindowTitle = m.windowTitleBase()
		return v
	}
	if hint := m.narrowTerminalHint(); hint != "" {
		return m.renderNarrowTerminalView(hint)
	}
	if m.terminalInputResumeActive() {
		return m.renderTerminalInputResumeView()
	}

	// The conversation, status, and composer own the complete terminal width.
	// Infrequent controls are exposed through overlays instead of persistent
	// chrome that competes with code and tool output.
	paneWidth := m.chatPaneWidth()
	var content strings.Builder
	var viewCursor *tea.Cursor
	content.WriteString(m.viewport.View())
	content.WriteString("\n")
	if !m.compactCompletionOwnsDivider() {
		content.WriteString(m.styles.Divider.Render(rule(paneWidth)))
		content.WriteString("\n")
	}

	// Permission requests replace the composer in-flow. They deliberately do
	// not use overlayOnContent: the transcript stays visible, code keeps the
	// terminal width, and the decision reads as the next conversational action.
	if m.pendingApproval != nil {
		content.WriteString(m.renderApproval())
	} else if m.readScopePrompt != nil {
		content.WriteString(m.renderReadScopePrompt())
	} else {
		if plan := m.renderGoalPlan(); plan != "" {
			content.WriteString(plan)
			content.WriteString("\n")
		}
		if bob := m.renderBobWorkspaceContext(); bob != "" {
			content.WriteString(bob)
			content.WriteString("\n")
		}
		if action := m.renderContinuationAction(); action != "" && !m.goalPlanOwnsContinuation() {
			content.WriteString(action)
			content.WriteString("\n")
		}
		if status := m.renderStatusLine(); status != "" {
			content.WriteString(status)
			content.WriteString("\n")
			if m.activityComposerGap() {
				content.WriteString("\n")
			}
		}

		// Ordinary turns keep the composer available so the next instruction can be
		// drafted while work continues. Owned operations and a queued follow-up use
		// the compact liveness line until authority returns to the textarea.
		if m.pendingPaste != nil || m.pendingSessionSwitch != nil {
			// The status prompt above owns the footer until the user answers.
		} else if m.overlay == OverlayCortexDecision && m.cortexDecision != nil {
			// Human decisions are full-width composer owners. They remain below the
			// visible transcript and never mask it with an overlay.
			content.WriteString(m.cortexDecision.View(m.cortexDecisionBusyMarker()))
		} else if m.overlay == OverlayCompletion && m.isCompletionActive() {
			// Completion is owned by the composer: the popup sits immediately above
			// the unchanged textarea, and only its Bubbles filter owns the terminal
			// cursor. Keeping the draft visible makes the replacement span legible.
			popupY := strings.Count(content.String(), "\n")
			popup, popupCursor := m.renderCompletionModalView()
			content.WriteString(popup)
			content.WriteString("\n")
			input := m.input
			input.SetVirtualCursor(false)
			content.WriteString(input.View())
			viewCursor = offsetCursor(popupCursor, 0, popupY)
		} else if m.overlay == OverlayPlanForm && m.planFormState != nil {
			// Structured planning temporarily owns the composer rows. The transcript
			// remains immediately above this full-width inline form.
			formY := strings.Count(content.String(), "\n")
			form, formCursor := m.renderPlanFormView()
			content.WriteString(form)
			viewCursor = offsetCursor(formCursor, 0, formY)
		} else if m.overlay == OverlayGoalForm && m.goalFormState != nil {
			// Goal definition follows the same composer contract as Plan: one typed
			// inline owner, no transcript mask, and one translated Bubbles cursor.
			formY := strings.Count(content.String(), "\n")
			form, formCursor := m.goalFormState.ViewWithCursor()
			content.WriteString(form)
			viewCursor = offsetCursor(formCursor, 0, formY)
		} else if m.overlay != OverlayNone {
			// Preserve the composer's row allocation behind centered overlays so the
			// transcript does not shift when transient navigation opens.
			input := m.input
			input.SetVirtualCursor(false)
			content.WriteString(strings.Repeat("\n", max(0, lipgloss.Height(input.View())-1)))
		} else if m.queuedFollowUp != nil && (!m.queuedFollowUpHeld() || !m.composerEditable()) {
			// The queue owns one stable footer row until it dispatches, is restored
			// after failure, or the user edits/clears it. Never hide pending input in
			// model state with no visible recovery path.
			content.WriteString(m.renderQueuedFollowUp())
		} else if m.composerEditable() {
			if m.queuedFollowUpHeld() {
				// A rejected active turn and the next queued instruction remain two
				// visible owners. Up swaps them atomically instead of concatenating
				// prompts or attachment sets behind the user's back.
				content.WriteString(m.renderQueuedFollowUp())
				content.WriteString("\n")
			}
			if cue := m.renderComposerOverflowCue(); cue != "" {
				content.WriteString(cue)
				content.WriteString("\n")
			}
			// Render a local copy with Bubbles' virtual cursor disabled. The same
			// copy supplies the one real cursor owned by this top-level view.
			input := m.input
			if m.state != StateIdle {
				input.Placeholder = "Write a follow-up · enter queue"
			}
			input.SetVirtualCursor(false)
			inputView := input.View()
			composerY := strings.Count(content.String(), "\n")
			content.WriteString(inputView)
			viewCursor = offsetCursor(input.Cursor(), 0, composerY)
		}
	}

	// Infrequent controls remain centered overlays. Composer-owned completion,
	// Plan, and Goal surfaces were already rendered in the normal footer flow.
	if m.overlay != OverlayNone && m.overlay != OverlayCompletion &&
		m.overlay != OverlayCortexDecision &&
		m.overlay != OverlayPlanForm && m.overlay != OverlayGoalForm {
		var overlay string
		var localCursor *tea.Cursor
		// Every overlay suppresses the underlying composer cursor. Text-entry
		// overlays may replace it with their own translated child cursor below.
		viewCursor = nil
		switch m.overlay {
		case OverlayHelp:
			overlay = m.renderHelpOverlay(m.width)
		case OverlayModelPicker:
			if m.modelPickerState != nil {
				overlay = m.renderModelPicker()
			}
		case OverlayCloudConsent:
			overlay = m.renderCloudConsent()
		case OverlayModelDetails:
			overlay = m.renderModelDetails()
		case OverlayModelPull:
			overlay, localCursor = m.renderModelPull()
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
		case OverlayGoalInspector:
			if m.goalInspectorState != nil {
				overlay = m.goalInspectorState.View()
			}
		case OverlayGoalRecovery:
			if m.goalRecoveryState != nil {
				overlay, localCursor = m.goalRecoveryState.ViewWithCursor()
			}
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
	// Cell-motion reporting is the smallest mouse mode that delivers wheel
	// events. Without it, terminals commonly translate wheel input in the alt
	// screen into arrow keys, which moves the focused composer instead of the
	// transcript. Native selection remains available through the terminal's
	// mouse-reporting override (commonly Shift-drag), and Ctrl+Y remains the
	// application-level copy path.
	v.MouseMode = tea.MouseModeCellMotion
	v.Cursor = viewCursor

	// Terminal title progress. The workspace basename differentiates several
	// Local Agent tabs without exposing a full private path through terminal
	// title integrations or window-manager history.
	windowTitle := m.windowTitleBase()
	switch m.state {
	case StateWaiting:
		v.WindowTitle = windowTitle + " \u00b7 thinking..."
	case StateStreaming:
		v.WindowTitle = windowTitle + " \u00b7 streaming..."
	default:
		if m.doneFlash {
			v.WindowTitle = windowTitle + " \u00b7 done"
		} else {
			v.WindowTitle = windowTitle
		}
	}

	return v
}

// activityComposerGap gives the live activity rail one row of breathing room
// before the draft surface. Prompt/form owners remain dense because their
// controls already provide their own framing and exact height contract.
func (m *Model) activityComposerGap() bool {
	if _, active := m.currentWorkingActivity(); !active {
		return false
	}
	if m.pendingApproval != nil || m.readScopePrompt != nil || m.pendingPaste != nil || m.overlay != OverlayNone {
		return false
	}
	return m.queuedFollowUp != nil || (m.composerEditable() && m.renderComposerOverflowCue() == "")
}

// renderNarrowTerminalView keeps tiny terminals recoverable.
func (m *Model) renderNarrowTerminalView(hint string) tea.View {
	titleText := "TERMINAL TOO SMALL"
	if m.width < minTerminalWidth && m.height >= minTerminalHeight {
		titleText = "TERMINAL TOO NARROW"
	} else if m.height < minTerminalHeight && m.width >= minTerminalWidth {
		titleText = "TERMINAL TOO SHORT"
	}
	return m.renderTerminalPauseView(
		titleText,
		hint,
		[]string{"Input paused · ctrl+c quit", "Paused · ctrl+c", "ctrl+c"},
		"resize terminal",
	)
}

func (m *Model) renderTerminalInputResumeView() tea.View {
	title := "INPUT PAUSED"
	hint := "Restoring input after resize; input received here is ignored."
	controls := []string{"Waiting for quiet · ctrl+c quit", "Waiting · ctrl+c", "ctrl+c"}
	switch m.terminalInputResumePhase {
	case terminalInputResumeAwaitGesture:
		hint = "Input is quiet · press enter to resume."
		controls = []string{"enter resume · ctrl+c quit", "enter · ctrl+c", "ctrl+c"}
	case terminalInputResumeConfirmationQuiet:
		hint = "Confirming the input boundary; input received here is ignored."
		controls = []string{"Resuming · ctrl+c quit", "Resuming · ctrl+c", "ctrl+c"}
	}
	return m.renderTerminalPauseView(
		title,
		hint,
		controls,
		"restoring input",
	)
}

func (m *Model) renderTerminalPauseView(titleText, hint string, controlCandidates []string, titleSuffix string) tea.View {
	terminalWidth := max(1, m.width)
	terminalHeight := max(1, m.height)
	contentW := max(1, terminalWidth-2)

	rows := []string{m.styles.OverlayTitle.Render(truncateDisplay(titleText, contentW))}
	if terminalHeight > 2 {
		hintRows := strings.Split(wrapText(hint, contentW), "\n")
		maximumHintRows := max(0, terminalHeight-2)
		if len(hintRows) > maximumHintRows {
			hintRows = hintRows[:maximumHintRows]
		}
		for _, row := range hintRows {
			rows = append(rows, m.styles.StatusText.Render(truncateDisplay(row, contentW)))
		}
	}
	if terminalHeight > 1 {
		controlHint := "ctrl+c"
		for _, candidate := range controlCandidates {
			controlHint = candidate
			if lipgloss.Width(candidate) <= contentW {
				break
			}
		}
		rows = append(rows, m.styles.FocusIndicator.Render(truncateDisplay(controlHint, contentW)))
	}
	if len(rows) > terminalHeight {
		rows = rows[:terminalHeight]
	}
	for index, row := range rows {
		rows[index] = lipgloss.PlaceHorizontal(terminalWidth, lipgloss.Center, row)
	}
	content := strings.Join(rows, "\n")
	top := (terminalHeight - len(rows)) / 2
	if top > 0 {
		content = strings.Repeat("\n", top) + content
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = m.windowTitleBase() + " · " + titleSuffix
	return v
}

func (m *Model) windowTitleBase() string {
	const product = "LOCAL AGENT"
	workspace := ""
	if m != nil && m.agent != nil {
		workspace = strings.TrimSpace(m.agent.WorkDir())
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	workspace = filepath.Clean(workspace)
	if workspace == "." || filepath.Dir(workspace) == workspace {
		return product
	}
	name := sanitizeTerminalSingleLine(filepath.Base(workspace))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return product
	}
	return truncateDisplay(product+" · "+name, 72)
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
	popupRows := completionPopupHeight(m.height, m.inputLines)
	// Border rows plus the one-line key footer live outside the content body.
	contentRows := max(2, popupRows-3)
	showTitle := contentRows >= 3
	showDivider := contentRows >= 5
	fixedRows := 1 // filter
	if showTitle {
		fixedRows++
	}
	if showDivider {
		fixedRows++
	}
	remainingRows := max(1, contentRows-fixedRows)
	previewRows := 0
	if cs.Kind == "attachments" && remainingRows >= 2 {
		previewRows = min(6, max(1, (remainingRows+1)/2))
	}
	itemRows := max(1, remainingRows-previewRows)

	var b strings.Builder

	if showTitle {
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
		if cs.Kind == "attachments" && cs.CurrentPath != "" {
			title += " · " + sanitizeTerminalSingleLine(cs.CurrentPath) + "/"
		}
		if cs.Searching {
			title += " · searching…"
		}
		b.WriteString(m.styles.OverlayTitle.Render(truncateDisplay(title, contentW)))
		b.WriteString("\n")
	}

	filterY := strings.Count(b.String(), "\n")
	b.WriteString(m.styles.FocusIndicator.Render(completionFilterPrompt))
	filterX := lipgloss.Width(completionFilterPrompt)
	b.WriteString(filter.View())
	b.WriteString("\n")
	filterCursor := offsetCursor(filter.Cursor(), filterX, filterY)

	if showDivider {
		b.WriteString(m.styles.FocusIndicator.Render(strings.Repeat("─", contentW)))
		b.WriteString("\n")
	}

	items := cs.FilteredItems
	if len(items) == 0 {
		empty := "  (no matches)"
		if cs.Searching {
			empty = "  (searching…)"
		}
		b.WriteString(m.styles.CompletionCategory.Render(truncateDisplay(empty, contentW)))
		b.WriteString("\n")
		for row := 1; row < itemRows; row++ {
			b.WriteString(strings.Repeat(" ", contentW))
			b.WriteString("\n")
		}
	} else {
		start := 0
		if cs.Index >= itemRows {
			start = cs.Index - itemRows + 1
		}
		end := start + itemRows
		if end > len(items) {
			end = len(items)
			start = max(0, end-itemRows)
		}

		for i := start; i < end; i++ {
			item := items[i]
			displayLabel := sanitizeTerminalSingleLine(item.Label)
			displayCategory := sanitizeTerminalSingleLine(item.Category)
			displayDescription := sanitizeTerminalSingleLine(item.Description)
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
				category = "  " + displayCategory
			}
			labelWidth := max(1, contentW-2-lipgloss.Width(category)-lipgloss.Width(selectedMark))
			label := truncateDisplay(displayLabel, labelWidth)
			description := ""
			if cs.Kind == "command" && displayDescription != "" {
				remaining := labelWidth - lipgloss.Width(label)
				if remaining >= 6 {
					description = " · " + truncateDisplay(displayDescription, remaining-3)
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
		for row := end - start; row < itemRows; row++ {
			b.WriteString(strings.Repeat(" ", contentW))
			b.WriteString("\n")
		}
	}

	if previewRows > 0 {
		preview := m.renderCompletionPreview(contentW, previewRows)
		lines := strings.Split(preview, "\n")
		if preview == "" {
			lines = nil
		}
		for row := 0; row < previewRows; row++ {
			if row < len(lines) {
				b.WriteString(lines[row])
			} else {
				b.WriteString(strings.Repeat(" ", contentW))
			}
			b.WriteString("\n")
		}
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

func completionPopupHeight(terminalHeight, inputLines int) int {
	if terminalHeight <= 0 {
		terminalHeight = 24
	}
	inputLines = max(1, inputLines)
	// Prefer two visible transcript rows. Multiline drafts may borrow one of
	// them at the supported minimum size so the popup and full textarea remain
	// visible together.
	available := terminalHeight - 1 - inputLines - 2 // divider + transcript
	if available < 5 {
		available = terminalHeight - 1 - inputLines - 1
	}
	return min(15, max(5, available))
}

// At the minimum height with a five-line draft, the popup's top border is the
// transcript/composer boundary. Omitting the redundant rule preserves one
// transcript row plus the top-level terminal safety row without hiding draft
// lines or completion controls.
func (m *Model) compactCompletionOwnsDivider() bool {
	return m.overlay == OverlayCompletion &&
		m.isCompletionActive() &&
		completionPopupHeight(m.height, m.inputLines) == 5
}

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
	if !conversationStarted && !noticeNeedsRecovery && len(m.failedServers) == 0 && !m.skipApprovalsEnabled() && !m.doneFlash && (m.promptTokens <= 0 || m.numCtx <= 0) {
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
	if m.doneFlash {
		done := "✓ Done"
		if m.lastTurnDuration > 0 {
			done += " · " + formatWorkingElapsed(m.lastTurnDuration)
		}
		parts = append(parts, m.styles.StatusCheck.UnsetPaddingLeft().Render(done))
		if receiptAction, ok := m.inspectableToolReceiptAction(); paneW >= 58 &&
			strings.TrimSpace(m.input.Value()) == "" && ok {
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
	if m.doneFlash {
		done := "✓ Done"
		if m.lastTurnDuration > 0 {
			done += " · " + formatWorkingElapsed(m.lastTurnDuration)
		}
		optional = append(optional, metadataPart{view: m.styles.StatusCheck.UnsetPaddingLeft().Render(done)})
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

func (m *Model) conversationStarted() bool {
	for _, entry := range m.entries {
		switch entry.Kind {
		case "user", "assistant", "tool_group":
			return true
		}
	}
	return false
}

func (m *Model) inspectableToolReceiptAction() (string, bool) {
	if m.lastTurnToolIndex < 0 || m.lastTurnToolIndex >= len(m.toolEntries) ||
		m.toolEntries[m.lastTurnToolIndex].Status == ToolStatusRunning {
		return "", false
	}
	if m.toolEntries[m.lastTurnToolIndex].Collapsed {
		return "inspect receipt", true
	}
	return "hide receipt", true
}

func (m *Model) hasTranscriptNotice() bool {
	for _, entry := range m.entries {
		if entry.Kind == "system" || entry.Kind == "error" {
			return true
		}
	}
	return false
}

func (m *Model) renderSystemNotice(content string, contentW int) string {
	const label = "notice · "
	available := max(1, contentW-m.styles.SystemText.GetPaddingLeft())
	plain := label + sanitizeTerminalMultiline(content)
	return m.styles.SystemText.Render(wrapText(plain, available))
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
				entries = append(entries, ChatEntry{Kind: "user", Content: msg.Content, Attachments: imageRefsFromMessages(msg.Images)})
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

	// Welcome message when no user messages yet
	hasUserMsg := false
	for _, e := range m.entries {
		if e.Kind == "user" || e.Kind == "assistant" {
			hasUserMsg = true
			break
		}
	}
	if !hasUserMsg && !m.hasVisibleLiveTurn() {
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
		// PlaceHorizontal owns a rectangular block and does not retain the
		// welcome builder's trailing newline. Start notices on a real row so a
		// long left padding cannot push their first line beyond the viewport.
		b.WriteByte('\n')
		// Append any system entries (e.g. failed server notices) below welcome
		for _, e := range m.entries {
			switch e.Kind {
			case "system":
				b.WriteString(m.renderSystemNotice(e.Content, contentW))
				b.WriteString("\n\n")
			case "error":
				if notice, ok := compactOllamaStartupNotice(e.Content, contentW, m.ollamaOffline); ok {
					// At the supported 30-column tier the generic error frame can
					// consume the whole viewport and hide the empty-state recovery
					// paths. Keep the raw ChatEntry unchanged and project only this
					// host-authored startup diagnostic into a bounded notice.
					b.WriteString(m.styles.ErrorText.Render(notice))
					b.WriteByte('\n')
				} else if isOllamaStartupRecovery(e.Content, m.ollamaOffline) {
					// Missing startup inventory is an actionable empty state, not a
					// failed user operation. Preserve the detailed host recovery copy
					// at ordinary widths without adding the generic red error label.
					b.WriteString(m.renderSystemNotice(e.Content, contentW))
					b.WriteString("\n\n")
				} else {
					m.renderEntryError(&b, e.Content, contentW)
				}
			}
		}
		return b.String()
	}

	// Fast path: the cached stable prefix is current. Only live entries (a
	// still-running tool group and anything after it) and the streaming tail
	// render per frame, so spinner ticks and stream chunks never walk the
	// whole transcript again.
	if m.entryCacheValid && len(m.entries) == m.cachedEntryCount {
		m.toolHitRegions = append(m.toolHitRegions[:0], m.cachedToolHitRegions...)
		m.thinkingHitRegions = append(m.thinkingHitRegions[:0], m.cachedThinkingHitRegions...)
		var b strings.Builder
		b.WriteString(m.cachedEntriesRender)
		state := m.cachedPrefixState
		for index := m.cachedStableCount; index < len(m.entries); index++ {
			m.renderEntryInto(&b, index, contentW, &state)
		}
		m.renderLiveTail(&b, contentW, &state)
		return b.String()
	}

	// Full render: cache the stable prefix (everything before the first
	// still-running tool group, whose card animates a glyph and elapsed time),
	// then render live entries and the streaming tail outside the cache.
	stableCount := m.stableEntryPrefixLen()
	var b strings.Builder
	m.toolHitRegions = m.toolHitRegions[:0]
	m.thinkingHitRegions = m.thinkingHitRegions[:0]
	var state entryRenderState
	snapshotPrefix := func() {
		m.cachedEntriesRender = b.String()
		m.cachedEntryCount = len(m.entries)
		m.cachedStableCount = stableCount
		m.cachedPrefixState = state
		m.cachedToolHitRegions = append(m.cachedToolHitRegions[:0], m.toolHitRegions...)
		m.cachedThinkingHitRegions = append(m.cachedThinkingHitRegions[:0], m.thinkingHitRegions...)
		m.entryCacheValid = true
	}
	for index := range m.entries {
		if index == stableCount {
			snapshotPrefix()
		}
		m.renderEntryInto(&b, index, contentW, &state)
	}
	if stableCount == len(m.entries) {
		snapshotPrefix()
	}
	m.renderLiveTail(&b, contentW, &state)
	return b.String()
}

// entryRenderState carries the transcript loop state across the cached stable
// prefix so live entries and the streaming tail continue the exact separator,
// role-header, and hit-region arithmetic of a full render.
type entryRenderState struct {
	renderedLines    int
	previousKind     string
	renderedAny      bool
	assistantStarted bool
}

// stableEntryPrefixLen returns how many leading entries render identically
// between appends. Everything from the first still-running tool group on is
// live: its card animates every spinner tick and must stay out of the cache.
func (m *Model) stableEntryPrefixLen() int {
	for index, entry := range m.entries {
		if entry.Kind == "tool_group" && m.toolGroupLive(entry.ToolIndex) {
			return index
		}
	}
	return len(m.entries)
}

func (m *Model) toolGroupLive(toolIdx int) bool {
	if toolIdx < 0 || toolIdx >= len(m.toolEntries) {
		return false
	}
	te := m.toolEntries[toolIdx]
	if te.Status == ToolStatusRunning {
		return true
	}
	for i := len(m.toolCardMgr.Cards) - 1; i >= 0; i-- {
		card := &m.toolCardMgr.Cards[i]
		if toolCallMatches(te.ID, te.Name, card.ID, card.Name) {
			return card.State == ToolCardRunning
		}
	}
	return false
}

func (m *Model) renderEntryInto(b *strings.Builder, entryIndex, contentW int, state *entryRenderState) {
	entry := m.entries[entryIndex]
	var entryView strings.Builder
	switch entry.Kind {
	case "user":
		state.assistantStarted = false
		m.renderUserMsg(&entryView, entry.Content, entry.Attachments, contentW)
	case "assistant":
		m.renderAssistantMsg(&entryView, entry, contentW, !state.assistantStarted)
	case "tool_group":
		m.renderToolGroup(&entryView, entry.ToolIndex)
	case "error":
		m.renderEntryError(&entryView, entry.Content, contentW)
	case "system":
		entryView.WriteString(m.renderSystemNotice(entry.Content, contentW))
		entryView.WriteString("\n")
	}
	chunk := strings.TrimRight(entryView.String(), "\n")
	if chunk == "" {
		return
	}
	if entry.Kind == "assistant" {
		state.assistantStarted = true
	}

	if state.renderedAny {
		separator := transcriptEntrySeparator(state.previousKind, entry.Kind)
		b.WriteString(separator)
		state.renderedLines += strings.Count(separator, "\n")
	}
	if entry.Kind == "tool_group" {
		header, _, _ := strings.Cut(chunk, "\n")
		m.toolHitRegions = append(m.toolHitRegions, toolHitRegion{
			ToolIndex: entry.ToolIndex,
			Row:       state.renderedLines,
			EndCol:    lipgloss.Width(header),
		})
	}
	if entry.Kind == "assistant" && strings.TrimSpace(entry.ThinkingContent) != "" {
		if rowOffset, endCol, ok := completedThinkingHeaderRegion(chunk); ok {
			m.thinkingHitRegions = append(m.thinkingHitRegions, thinkingHitRegion{
				EntryIndex: entryIndex,
				Row:        state.renderedLines + rowOffset,
				EndCol:     endCol,
				Digest:     reasoningReceiptDigest(entry.ThinkingContent),
			})
		}
	}
	b.WriteString(chunk)
	state.renderedLines += strings.Count(chunk, "\n")
	state.previousKind = entry.Kind
	state.renderedAny = true
}

// renderLiveTail renders the in-flight provider turn (streaming text or the
// inline waiting label). Provider calls can legitimately produce no tokens
// while compacting, awaiting permission, or continuing after a tool receipt;
// keeping that phase next to the last transcript event avoids a tall blank
// viewport above the footer.
func (m *Model) renderLiveTail(b *strings.Builder, contentW int, state *entryRenderState) {
	if m.hasLiveTurnContent() {
		if state.renderedAny {
			b.WriteString(transcriptEntrySeparator(state.previousKind, "assistant"))
		}
		m.renderStreamingMsg(b, m.streamBuf.String(), contentW, !state.assistantStarted)
	} else if label := m.inlineTurnActivity(); label != "" {
		if state.renderedAny {
			b.WriteString(transcriptEntrySeparator(state.previousKind, "assistant"))
		}
		m.renderInlineTurnActivity(b, label, contentW, !state.assistantStarted)
	}
}

func completedThinkingHeaderRegion(rendered string) (rowOffset, endCol int, ok bool) {
	for row, line := range strings.Split(rendered, "\n") {
		plain := strings.TrimLeft(ansi.Strip(line), " ")
		if strings.HasPrefix(plain, "│ ▸") || strings.HasPrefix(plain, "│ ▾") {
			return row, lipgloss.Width(line), true
		}
	}
	return 0, 0, false
}

func (m *Model) hasLiveTurn() bool {
	return m.hasLiveTurnContent()
}

func (m *Model) hasVisibleLiveTurn() bool {
	return m.hasLiveTurnContent() || m.inlineTurnActivity() != ""
}

func (m *Model) hasLiveTurnContent() bool {
	return strings.TrimSpace(m.streamBuf.String()) != "" || strings.TrimSpace(m.thinkBuf.String()) != ""
}

func (m *Model) inlineTurnActivity() string {
	if m == nil || m.hasLiveTurnContent() || m.toolsPending > 0 {
		return ""
	}
	if m.pendingApproval != nil {
		return "Waiting for permission below…"
	}
	if m.compactingContext {
		return "Preparing context…"
	}
	if m.state == StateWaiting || m.state == StateStreaming {
		return "Waiting for model…"
	}
	return ""
}

func (m *Model) renderInlineTurnActivity(b *strings.Builder, label string, contentW int, showHeader bool) {
	label = sanitizeTerminalSingleLine(label)
	if label == "" {
		return
	}
	if showHeader {
		m.renderAssistantHeader(b, contentW)
	}
	b.WriteString(indentBlock(m.styles.StreamHint.Render(label), "  "))
	b.WriteString("\n")
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
	content = strings.TrimSpace(sanitizeTerminalMultiline(content))
	if content == "" {
		content = "The operation failed without an error message."
	}
	b.WriteString("  " + m.styles.ErrorChip.Render("✗ error"))
	b.WriteString("\n")
	b.WriteString(m.styles.ToolErrorText.Render(indentBlock(wrapText(content, max(1, contentW-2)), "  ")))
	b.WriteString("\n\n")
}

// compactOllamaStartupNotice is deliberately narrow: only the fixed startup
// recovery message authored by the host is eligible, and only when the chat
// pane would otherwise hide the welcome surface. Arbitrary provider/tool
// errors retain the complete generic error presentation.
func compactOllamaStartupNotice(content string, width int, unavailable bool) (string, bool) {
	if width >= 28 || !isOllamaStartupRecovery(content, unavailable) {
		return "", false
	}
	normalized := strings.ToLower(sanitizeTerminalSingleLine(content))
	if strings.Contains(normalized, "no model selected") {
		return truncateDisplay("Ollama model · ctrl+o", max(1, width)), true
	}
	return truncateDisplay("Ollama setup · Runtime", max(1, width)), true
}

func isOllamaStartupRecovery(content string, unavailable bool) bool {
	if !unavailable {
		return false
	}
	normalized := strings.ToLower(sanitizeTerminalSingleLine(content))
	return strings.HasPrefix(normalized, "ollama:") && strings.Contains(normalized, "try: ollama serve")
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
	if micro {
		// The 30-column contract still has room for the complete safety label;
		// use the full row instead of truncating one semantic word for padding.
		lineWidth = contentWidth
	}
	writeLine := func(style lipgloss.Style, text string) {
		wb.WriteString(style.Render(truncateDisplay(text, lineWidth)))
		wb.WriteByte('\n')
	}

	writeLine(m.styles.OverlayTitle, "LOCAL AGENT")
	trust := "Local-first · Ollama · " + m.approvalPostureWelcomeLabel(false)
	if micro {
		trust = "Local-first · " + m.approvalPostureWelcomeMicroLabel()
	} else if compact {
		trust = "Local-first · " + m.approvalPostureWelcomeLabel(true)
	}
	writeLine(m.styles.StatusText, trust)

	var infoParts []string
	presentedMode := m.presentedMode()
	modelLabel := m.currentModelSurfaceLabel(compact)
	if m.currentModelIsNonLocal() && modelLabel != "" {
		// The execution boundary precedes ordinary mode/model metadata so a
		// narrow welcome surface cannot imply that Cloud prompts remain local.
		infoParts = append(infoParts, modelLabel)
	}
	if presentedMode != ModeNormal {
		infoParts = append(infoParts, m.modeConfigs[presentedMode].Label)
	}
	if !m.currentModelIsNonLocal() && modelLabel != "" {
		infoParts = append(infoParts, modelLabel)
	}
	if m.ollamaOffline {
		infoParts = append(infoParts, "offline")
	}
	if len(infoParts) > 0 {
		writeLine(m.styles.StatusText, strings.Join(infoParts, " · "))
	}

	if micro {
		writeLine(m.styles.WelcomeHint, "enter · ctrl+p settings")
		writeLine(m.styles.StatusText, "? help · / @ #")
	} else if compact {
		writeLine(m.styles.WelcomeHint, "enter send · ctrl+p settings · ? help")
		writeLine(m.styles.StatusText, "/ commands · @ files · # skills")
	} else {
		writeLine(m.styles.WelcomeHint, "enter send · / commands · ctrl+p settings · ? help")
		writeLine(m.styles.StatusText, "shift+tab mode · ctrl+o models · @ files · # skills")
	}

	// Center the welcome content horizontally in the available viewport width.
	centered := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, wb.String())
	b.WriteString(centered)
}

// renderUserMsg renders a user message block: a compact role label above an
// accent-guttered content block. The gutter, not a full-width rule, carries
// the visual identity so the transcript keeps a calm vertical rhythm.
func (m *Model) renderUserMsg(b *strings.Builder, content string, attachments []imageasset.Ref, contentW int) {
	content = sanitizeTerminalMultiline(content)
	b.WriteString(m.styles.UserLabel.Render("you"))
	b.WriteString("\n")
	gutter := "  " + m.styles.UserGutter.Render("▌") + " "
	text := m.styles.UserContent.UnsetPaddingLeft()
	for _, line := range strings.Split(wrapText(content, max(10, contentW-4)), "\n") {
		b.WriteString(gutter + text.Render(line))
		b.WriteString("\n")
	}
	if len(attachments) > 0 {
		b.WriteString(m.renderImageAttachmentSummary(attachments, contentW))
		b.WriteString("\n")
	}
}

// renderAssistantMsg renders a completed assistant message block.
// Uses cached RenderedContent if available (snap-into-place pattern).
func (m *Model) renderAssistantMsg(b *strings.Builder, entry ChatEntry, contentW int, showHeader bool) {
	content := sanitizeTerminalMultiline(entry.Content)
	hasContent := strings.TrimSpace(content) != ""
	hasThinking := strings.TrimSpace(entry.ThinkingContent) != ""
	if !hasContent && !hasThinking {
		return
	}

	if showHeader {
		m.renderAssistantHeader(b, contentW)
	}

	// Reasoning belongs to this assistant turn, so its disclosure follows the
	// role header instead of appearing as an unowned block above it.
	if hasThinking {
		thinkBox := m.renderThinkingBox(entry.ThinkingContent, entry.ThinkingCollapsed)
		b.WriteString(indentBlock(thinkBox, "  "))
		b.WriteString("\n")
	}
	if !hasContent {
		return
	}

	// Use cached rendered content if available.
	rendered := entry.RenderedContent
	if content != entry.Content {
		// Cached Glamour output is trusted only when it was derived from the same
		// sanitized source. Restored or synthetic entries must be rendered again.
		rendered = ""
	}
	if rendered == "" {
		rendered = content
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
func (m *Model) renderStreamingMsg(b *strings.Builder, content string, contentW int, showHeader bool) {
	content = sanitizeTerminalMultiline(content)
	hasContent := strings.TrimSpace(content) != ""
	hasThinking := strings.TrimSpace(m.thinkBuf.String()) != ""
	if !hasContent && !hasThinking {
		return
	}

	if showHeader {
		m.renderAssistantHeader(b, contentW)
	}

	// Live reasoning uses the same assistant-owned hierarchy as the completed
	// disclosure. Keeping it compact prevents token-by-token height jitter; the
	// full receipt becomes expandable only after the turn settles.
	if hasThinking {
		b.WriteString(indentBlock(m.renderLiveThinkingBox(m.thinkBuf.String()), "  "))
		b.WriteString("\n")
	}
	if !hasContent {
		return
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

func (m *Model) renderAssistantHeader(b *strings.Builder, _ int) {
	// The operational footer owns the one active animation. Keeping the role
	// header static makes streamed reasoning feel like transcript content rather
	// than a second competing progress indicator. A compact label without a
	// full-width rule keeps consecutive turns readable without heavy chrome.
	b.WriteString(m.styles.AsstLabel.Render("assistant"))
	b.WriteString("\n")
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
		if card.ExpertProgress != nil &&
			(card.expertProgressCacheWidth != availableWidth-2 ||
				card.expertProgressCacheSequence != card.ExpertProgress.Sequence) {
			card.setExpertProgress(card.ExpertProgress, availableWidth-2)
		}
		cardView := card.View(availableWidth)
		if card.State == ToolCardRunning {
			glyph := "…"
			if !m.reducedMotion {
				glyph = m.spin.View()
			}
			elapsed := m.nowTime().Sub(card.StartTime)
			if elapsed < 0 {
				elapsed = 0
			}
			cardView = card.ViewWithActivity(availableWidth, glyph, elapsed)
		}
		if card.Expanded && card.State != ToolCardRunning {
			var diffView string
			if te.DiffPending {
				diffView = renderDiffLoadingAtWidth(te.Summary, m.styles, availableWidth)
			} else if len(te.DiffLines) > 0 {
				diffView = strings.TrimRight(
					renderUnifiedDiffAtWidth(te.Summary, te.DiffLines, m.styles, 0, availableWidth), "\n",
				)
			}
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
		toolName := safeToolIdentifier(te.Name)
		projectedState := toolCardStateFromProjection(te.Projection)
		if te.Status == ToolStatusDone && projectedState != ToolCardSuccess {
			// A missing live/restored card must not erase the bounded semantic
			// projection. In particular, transport success with an unknown domain
			// outcome remains attention-colored instead of falling through to the
			// legacy green completion receipt.
			kind := toolCardKindForTool(te.Name)
			fallback := NewToolCard(te.Name, kind, m.isDark)
			fallback.ID = te.ID
			fallback.State = projectedState
			fallback.SetSummary(te.Summary)
			fallback.Args = te.Args
			fallback.ResultLanguage = te.ResultLanguage
			fallback.Result = te.Result
			fallback.Duration = te.Duration
			fallback.Expanded = !te.Collapsed
			fallback.Projection = te.Projection
			fallback.setExpertProgress(te.ExpertProgress, max(1, m.chatPaneWidth()-6))
			fallback.State = te.ExpertProgress.cardState(fallback.State)
			cardView := fallback.View(max(4, m.chatPaneWidth()-4))
			if fallback.Expanded {
				var diffView string
				if te.DiffPending {
					diffView = renderDiffLoadingAtWidth(te.Summary, m.styles, max(4, m.chatPaneWidth()-4))
				} else if len(te.DiffLines) > 0 {
					diffView = strings.TrimRight(renderUnifiedDiffAtWidth(
						te.Summary, te.DiffLines, m.styles, 0, max(4, m.chatPaneWidth()-4),
					), "\n")
				}
				if diffView != "" {
					cardView += "\n" + diffView
				}
			}
			b.WriteString(indentBlock(cardView, "  "))
			return
		}

		switch te.Status {
		case ToolStatusRunning:
			// Running: show spinner with type-specific icon
			icon := m.styles.ToolCallIcon.Render(toolIcon(tt, te.Status))
			spinView := "…"
			if !m.reducedMotion {
				spinView = m.spin.View()
			}
			text := m.styles.ToolCallText.Render(fmt.Sprintf(" %s ", toolName))
			hint := m.styles.ToolRunningText.Render(spinView + " running...")
			b.WriteString(icon + text + hint)
			// For running bash tools, show command inline
			if tt == ToolTypeBash {
				if summary := sanitizeTerminalSingleLine(toolSummary(tt, te)); summary != "" {
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
				text := m.styles.ToolDoneText.Render(fmt.Sprintf(" %s (%s)", toolName, dur))
				b.WriteString(icon + text)
				if summary := sanitizeTerminalSingleLine(toolSummary(tt, te)); summary != "" {
					summ := truncate(summary, layout.ToolSummaryMax)
					b.WriteString(m.styles.ToolBashCmd.Render(" " + summ))
				}
				b.WriteString("\n")
			} else {
				// Expanded: show args + result (or diff for file writes)
				text := m.styles.ToolDoneText.Render(fmt.Sprintf(" %s (%s)", toolName, dur))
				b.WriteString(icon + text)
				b.WriteString("\n")
				// Args
				args := truncate(sanitizeTerminalSingleLine(te.Args), layout.ArgsTruncMax)
				b.WriteString(m.styles.ToolDetailText.Render(layout.ToolIndent + "args: " + args))
				b.WriteString("\n")
				// Diff or result
				diffWidth := max(1, m.chatPaneWidth()-4)
				if te.DiffPending {
					b.WriteString(renderDiffLoadingAtWidth(te.Summary, m.styles, diffWidth))
					b.WriteString("\n")
				} else if len(te.DiffLines) > 0 {
					b.WriteString(renderUnifiedDiffAtWidth(te.Summary, te.DiffLines, m.styles, 0, diffWidth))
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
			text := m.styles.ToolErrorText.Render(fmt.Sprintf(" %s (%s)", toolName, dur))
			b.WriteString(icon + text)
			b.WriteString("\n")
			// Error result always shown
			result := truncate(sanitizeTerminalMultiline(te.Result), layout.ResultTruncMax)
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

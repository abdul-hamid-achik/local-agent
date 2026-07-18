package ui

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

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
	inlinePreview := cs.Kind == "attachments" && previewRows == 0

	var b strings.Builder

	if showTitle {
		var title string
		switch cs.Kind {
		case "command":
			title = "Commands"
		case "attachments":
			if inlinePreview {
				title = "Attach Files · Preview"
			} else {
				title = "Attach Files & Agents"
			}
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
	filterPrompt := completionFilterPromptForGlyphProfile(m.glyphProfile)
	b.WriteString(m.styles.FocusIndicator.Render(filterPrompt))
	filterX := lipgloss.Width(filterPrompt)
	b.WriteString(filter.View())
	b.WriteString("\n")
	filterCursor := offsetCursor(filter.Cursor(), filterX, filterY)

	if showDivider {
		b.WriteString(m.styles.Divider.Render(
			strings.Repeat(glyphSet(m.glyphProfile).Horizontal, contentW),
		))
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

		glyphs := glyphSet(m.glyphProfile)
		for i := start; i < end; i++ {
			item := items[i]
			displayLabel := sanitizeTerminalSingleLine(item.Label)
			displayCategory := sanitizeTerminalSingleLine(item.Category)
			displayDescription := sanitizeTerminalSingleLine(item.Description)
			prefix := "  "
			if i == cs.Index {
				prefix = m.styles.FocusIndicator.Render(glyphs.Collapsed + " ")
			}

			// Check if selected (for multi-select)
			selectedMark := ""
			if cs.Selected != nil {
				// Find original index
				for oi, orig := range cs.AllItems {
					if orig.Label == item.Label && orig.Insert == item.Insert {
						if cs.Selected[oi] {
							selectedMark = m.styles.FocusIndicator.Render(" " + glyphs.Success)
						}
						break
					}
				}
			}

			category := ""
			if cs.Kind == "attachments" {
				category = completionCategoryDisplay(displayCategory)
				if inlinePreview {
					category = compactCompletionPreviewState(cs.Preview, category)
				}
			}
			categoryWidth := 0
			if category != "" {
				// Right-aligned category column with a two-cell gutter.
				categoryWidth = lipgloss.Width(category) + 2
			}
			labelWidth := max(1, contentW-2-categoryWidth-lipgloss.Width(selectedMark))
			label := truncateDisplay(displayLabel, labelWidth)
			description := ""
			if cs.Kind == "command" && displayDescription != "" {
				remaining := labelWidth - lipgloss.Width(label)
				if remaining >= 6 {
					description = " · " + truncateDisplay(displayDescription, remaining-3)
				}
			}
			desc := m.styles.CompletionCategory.Render(description)

			row := prefix + label + desc + selectedMark
			if i == cs.Index {
				row = prefix + m.styles.FocusIndicator.Render(label) + desc + selectedMark
			}
			if category != "" {
				gap := max(1, contentW-lipgloss.Width(row)-lipgloss.Width(category))
				row += strings.Repeat(" ", gap) + m.styles.CompletionCategory.Render(category)
			}
			b.WriteString(row)
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

// completionCategoryDisplay keeps machine category tokens out of the UI.
func completionCategoryDisplay(category string) string {
	if category == "search_result" {
		return "search"
	}
	return category
}

// compactCompletionPreviewState keeps the selected item's preview state
// visible when the minimum-height popup has no dedicated preview row.
func compactCompletionPreviewState(preview completionPreview, fallback string) string {
	switch preview.State {
	case completionPreviewLoading:
		return "loading"
	case completionPreviewReady:
		return formatCompletionPreviewBytes(preview.Size)
	case completionPreviewBinary:
		return "binary"
	case completionPreviewError:
		return "error"
	case completionPreviewFolder:
		return "folder"
	case completionPreviewAgent:
		return "agent"
	default:
		return fallback
	}
}

func completionPopupHeight(terminalHeight, inputLines int) int {
	if terminalHeight <= 0 {
		terminalHeight = 24
	}
	inputLines = max(1, inputLines)
	// Reserve the terminal safety row, the complete composer, and the
	// transcript's four-row reading floor. Popups of six rows or fewer own
	// their top boundary and omit the redundant shared divider; roomier popups
	// reserve that divider explicitly.
	available := terminalHeight - 1 - inputLines - minTranscriptRows
	if available > 6 {
		available--
	}
	return min(15, max(5, available))
}

// The compact popup's top border is already a transcript/composer boundary.
// Omitting the redundant rule preserves one additional transcript row without
// hiding draft lines or completion controls.
func (m *Model) compactCompletionOwnsDivider() bool {
	return m.overlay == OverlayCompletion &&
		m.isCompletionActive() &&
		completionPopupHeight(m.height, m.inputLines) <= 6
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
		writeLine(m.styles.StatusText, m.keys.Help.Help().Key+" help · / @ #")
	} else if compact {
		writeLine(m.styles.WelcomeHint, "enter send · ctrl+p settings")
		writeLine(m.styles.StatusText, "/ commands · "+m.keys.Help.Help().Key+" help · @/#")
	} else {
		writeLine(m.styles.WelcomeHint,
			"enter send · / commands · ctrl+p settings · "+m.keys.Help.Help().Key+" help",
		)
		writeLine(m.styles.StatusText, "shift+tab mode · ctrl+o models · @ files · # skills")
	}

	// Center the welcome content horizontally in the available viewport width.
	centered := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, wb.String())
	b.WriteString(centered)
}

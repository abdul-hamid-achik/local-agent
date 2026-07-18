package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/permission"
)

const (
	approvalMaximumActionBytes      = 256
	approvalMaximumConsequenceBytes = 768
)

// ApprovalState owns presentation-only state for an approval request. The
// root Model remains responsible for every decision and side effect.
type ApprovalState struct {
	Viewport      viewport.Model
	ShowArguments bool
	ChoiceIndex   int
}

type approvalChoice struct {
	Decision     permission.ApprovalDecision
	Key          string
	Label        string
	CompactLabel string
}

// Keep the safe outcome first: Enter on a newly opened permission request
// denies it unless the user deliberately moves to an allow choice. Direct
// y/n/s shortcuts remain available regardless of the focused row.
var approvalChoices = [...]approvalChoice{
	{Decision: permission.DecisionUserDeny, Key: "n", Label: "deny", CompactLabel: "deny"},
	{Decision: permission.DecisionAllowOnce, Key: "y", Label: "once", CompactLabel: "once"},
	{
		Decision:     permission.DecisionAllowSession,
		Key:          "s",
		Label:        "identical request · current session",
		CompactLabel: "identical/session",
	},
}

type approvalTranscriptAnchor struct {
	valid   bool
	paused  bool
	yOffset int
}

func (m *Model) captureApprovalTranscriptAnchor() approvalTranscriptAnchor {
	if m == nil || !m.ready {
		return approvalTranscriptAnchor{}
	}
	return approvalTranscriptAnchor{
		valid:   true,
		paused:  m.followPaused(),
		yOffset: m.transcriptYOffset(),
	}
}

func (m *Model) restoreApprovalTranscriptAnchor(anchor approvalTranscriptAnchor) {
	if m == nil || !anchor.valid {
		return
	}
	m.restoreFollowPosition(anchor.paused, anchor.yOffset)
}

func (m *Model) openApproval(request ToolApprovalMsg) error {
	if strings.TrimSpace(request.ToolName) == "" {
		return fmt.Errorf("tool identity is missing")
	}
	if request.Response == nil {
		return fmt.Errorf("approval response channel is unavailable")
	}
	if _, err := json.Marshal(request.Args); err != nil {
		return fmt.Errorf("encode exact arguments: %w", err)
	}

	anchor := m.captureApprovalTranscriptAnchor()
	m.clearViewerModals(false)
	// Approval has the highest input authority. If a host request arrives while
	// a composer-owned form is visible, cancel that transient form instead of
	// retaining inaccessible state behind the decision surface.
	if m.overlay == OverlayPlanForm || m.overlay == OverlayGoalForm {
		m.planFormState = nil
		m.goalFormState = nil
		m.overlay = OverlayNone
	}
	// A Cortex decision remains presentation-only while approval temporarily
	// owns the higher-priority inline surface. Its durable blocker is unchanged,
	// and the exact request is shown again after approval settles.
	if m.overlay == OverlayCortexDecision {
		m.overlay = OverlayNone
	}
	if m.overlay == OverlayAgents {
		// Approval owns higher input authority. Agent Hub state is ephemeral and
		// must not remain hidden behind the inline decision or reappear stale
		// after it settles.
		m.agentHubState = nil
		m.overlay = OverlayNone
	}
	m.pendingApproval = &request
	m.approvalState = &ApprovalState{ChoiceIndex: 0}
	// Permission decisions are part of the active conversation flow. They own
	// the composer/footer region instead of covering the transcript with a
	// centered modal. Clear a completion defensively so an asynchronous host
	// request never leaves hidden filter state behind the inline surface.
	if m.isCompletionActive() {
		m.dismissCompletion()
	}
	m.overlayParent = OverlayNone
	m.overlay = OverlayNone
	m.input.Blur()
	m.resizeApproval(false)
	m.recalcViewportHeight()
	m.restoreApprovalTranscriptAnchor(anchor)
	return nil
}

func (m *Model) resolvePendingApproval(response permission.ApprovalResponse) {
	anchor := m.captureApprovalTranscriptAnchor()
	if m.pendingApproval != nil && m.pendingApproval.Response != nil {
		m.pendingApproval.Response <- response
	}
	m.pendingApproval = nil
	m.approvalState = nil
	if m.composerEditable() {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	m.recalcViewportHeight()
	m.restoreApprovalTranscriptAnchor(anchor)
	m.activateCortexDecision()
}

func (m *Model) toggleApprovalDetails() {
	if m.approvalState == nil || m.pendingApproval == nil {
		return
	}
	anchor := m.captureApprovalTranscriptAnchor()
	m.approvalState.ShowArguments = !m.approvalState.ShowArguments
	m.resizeApproval(false)
	m.recalcViewportHeight()
	m.restoreApprovalTranscriptAnchor(anchor)
}

func (m *Model) moveApprovalChoice(delta int) {
	if m.approvalState == nil || len(approvalChoices) == 0 || delta == 0 {
		return
	}
	index := m.approvalState.ChoiceIndex + delta
	if index < 0 {
		index = len(approvalChoices) - 1
	}
	if index >= len(approvalChoices) {
		index = 0
	}
	m.approvalState.ChoiceIndex = index
}

func (m *Model) resetHiddenApprovalChoice() {
	if m != nil && m.approvalState != nil {
		m.approvalState.ChoiceIndex = 0
	}
}

func (m *Model) selectedApprovalResponse() permission.ApprovalResponse {
	if m.approvalState == nil || m.approvalState.ChoiceIndex < 0 ||
		m.approvalState.ChoiceIndex >= len(approvalChoices) {
		return permission.Deny()
	}
	switch approvalChoices[m.approvalState.ChoiceIndex].Decision {
	case permission.DecisionAllowOnce:
		return permission.AllowOnce()
	case permission.DecisionAllowSession:
		return permission.AllowSession()
	default:
		return permission.Deny()
	}
}

// navigateApprovalViewport leaves arrows and j/k to the focused decision rows.
// Long commands, diffs, and exact arguments retain explicit paging controls.
func (m *Model) navigateApprovalViewport(keyName string) bool {
	if m.approvalState == nil {
		return false
	}
	switch keyName {
	case "pgdown":
		m.approvalState.Viewport.PageDown()
	case "pgup":
		m.approvalState.Viewport.PageUp()
	case "ctrl+d":
		m.approvalState.Viewport.HalfPageDown()
	case "ctrl+u":
		m.approvalState.Viewport.HalfPageUp()
	case "home":
		m.approvalState.Viewport.GotoTop()
	case "end":
		m.approvalState.Viewport.GotoBottom()
	default:
		return false
	}
	return true
}

func (m *Model) resizeApproval(preserveOffset bool) {
	if m.approvalState == nil || m.pendingApproval == nil {
		return
	}
	offset := 0
	if preserveOffset {
		offset = m.approvalState.Viewport.YOffset()
	}
	width := m.approvalContentWidth()
	content := m.buildApprovalContent(width)
	bodyHeight := min(max(1, lipgloss.Height(content)), m.approvalBodyHeight())
	vp := viewport.New(
		viewport.WithWidth(width),
		viewport.WithHeight(bodyHeight),
	)
	// The smart parent owns a small, consistent read-only navigation grammar.
	vp.KeyMap.Up = key.NewBinding(key.WithDisabled())
	vp.KeyMap.Down = key.NewBinding(key.WithDisabled())
	vp.KeyMap.PageUp = key.NewBinding(key.WithDisabled())
	vp.KeyMap.PageDown = key.NewBinding(key.WithDisabled())
	vp.KeyMap.HalfPageUp = key.NewBinding(key.WithDisabled())
	vp.KeyMap.HalfPageDown = key.NewBinding(key.WithDisabled())
	vp.SetContent(content)
	vp.SetYOffset(offset)
	m.approvalState.Viewport = vp
}

func (m *Model) renderApproval() string {
	if m.approvalState == nil || m.pendingApproval == nil {
		return ""
	}
	contentWidth := m.approvalContentWidth()
	toolName := boundedApprovalMetadata(m.pendingApproval.ToolName, approvalMaximumActionBytes)
	if toolName == "" {
		toolName = "unknown tool"
	}
	mode := m.modeConfigs[m.presentedMode()].Label
	// Keep the tool identity ahead of the mode so narrow terminals preserve the
	// action being authorized. The authority mode remains explicit whenever the
	// available width permits it.
	permissionGlyph := "◇"
	if m.glyphProfile == GlyphASCII {
		permissionGlyph = "?"
	}
	separator := glyphSeparator(m.glyphProfile)
	titleText := permissionGlyph + " Permission" + separator + toolName + separator + mode
	title := m.styles.ApprovalPrompt.Render(
		truncateDisplayWithGlyphProfile(titleText, contentWidth, m.glyphProfile),
	)
	sections := []string{title}
	if saved := m.renderApprovalComposerReceipt(contentWidth); saved != "" {
		sections = append(sections, saved)
	}
	sections = append(sections, m.approvalState.Viewport.View(), m.renderApprovalChoices(contentWidth))
	detailAction := "arguments"
	if m.approvalState.ShowArguments {
		detailAction = "preview"
	}
	hints := m.renderKeyHints(contentWidth,
		keyHint{Key: "esc", Action: "cancel turn"},
		keyHint{Key: "enter", Action: "select"},
		keyHint{Key: "↑/↓/j/k", Action: "move"},
		keyHint{Key: "d", Action: detailAction},
		keyHint{Key: "pgup/dn", Action: "scroll"},
	)
	if contentWidth < 40 {
		hints = m.renderKeyHints(contentWidth,
			keyHint{Key: "esc", Action: "cancel turn"},
			keyHint{Key: "enter", Action: "select"},
		)
		detailHints := []keyHint{{Key: "d", Action: detailAction}}
		switch {
		case !m.approvalState.Viewport.AtBottom():
			detailHints = append([]keyHint{{Key: "pgdn", Action: "more"}}, detailHints...)
		case m.approvalState.Viewport.YOffset() > 0:
			detailHints = append([]keyHint{{Key: "pgup", Action: "previous"}}, detailHints...)
		}
		hints += "\n" + m.renderKeyHints(contentWidth, detailHints...)
	}
	sections = append(sections, hints)
	return indentApprovalSurface(
		strings.Join(sections, "\n"),
		2,
		m.chatPaneWidth(),
		m.glyphProfile,
	)
}

func (m *Model) renderApprovalComposerReceipt(width int) string {
	hasDraft := m.input.Value() != ""
	hasQueue := m.queuedFollowUp != nil
	if !hasDraft && !hasQueue {
		return ""
	}

	var candidates []string
	switch {
	case hasDraft && hasQueue:
		candidates = []string{"Draft saved · queued follow-up saved", "Draft + queue saved", "Input saved"}
	case hasDraft:
		candidates = []string{"Composer draft saved", "Draft saved", "Input saved"}
	default:
		candidates = []string{"Queued follow-up saved", "Queue saved", "Input saved"}
	}
	chosen := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if lipgloss.Width(candidate) <= width {
			chosen = candidate
			break
		}
	}
	if m.glyphProfile == GlyphASCII {
		chosen = strings.ReplaceAll(chosen, " · ", glyphSeparator(GlyphASCII))
	}
	return m.styles.OverlayDim.Render(
		truncateDisplayWithGlyphProfile(chosen, max(1, width), m.glyphProfile),
	)
}

func (m *Model) renderApprovalChoices(width int) string {
	if m.approvalState == nil || width <= 0 {
		return ""
	}
	selected := m.approvalState.ChoiceIndex
	if selected < 0 || selected >= len(approvalChoices) {
		selected = 0
	}

	choiceView := func(choice approvalChoice, active, compact bool) string {
		indicator := "  "
		label := choice.Label
		if compact {
			label = choice.CompactLabel
		}
		if m.glyphProfile == GlyphASCII {
			label = strings.ReplaceAll(label, " · ", glyphSeparator(GlyphASCII))
		}
		if active {
			indicatorGlyph := "›"
			if m.glyphProfile == GlyphASCII {
				indicatorGlyph = glyphSet(GlyphASCII).Right
			}
			indicator = m.styles.FocusIndicator.Render(indicatorGlyph + " ")
		}
		keyView := m.styles.FocusIndicator.Render(choice.Key)
		labelView := m.styles.OverlayDim.Render(label)
		return indicator + keyView + " " + labelView
	}

	wide := make([]string, 0, len(approvalChoices)*2-1)
	for index, choice := range approvalChoices {
		if index > 0 {
			wide = append(wide, m.styles.OverlayDim.Render(glyphSeparator(m.glyphProfile)))
		}
		wide = append(wide, choiceView(choice, index == selected, false))
	}
	wideView := lipgloss.JoinHorizontal(lipgloss.Top, wide...)
	if lipgloss.Width(wideView) <= width {
		return wideView
	}
	if width < 40 {
		firstChoice := strings.TrimLeft(choiceView(approvalChoices[0], selected == 0, true), " ")
		secondChoice := strings.TrimLeft(choiceView(approvalChoices[1], selected == 1, true), " ")
		first := lipgloss.JoinHorizontal(lipgloss.Top,
			firstChoice,
			m.styles.OverlayDim.Render(glyphSeparator(m.glyphProfile)),
			secondChoice,
		)
		second := choiceView(approvalChoices[2], selected == 2, true)
		return lipgloss.JoinVertical(lipgloss.Left,
			truncateDisplayWithGlyphProfile(first, width, m.glyphProfile),
			truncateDisplayWithGlyphProfile(second, width, m.glyphProfile),
		)
	}

	rows := make([]string, 0, len(approvalChoices))
	for index, choice := range approvalChoices {
		rows = append(rows,
			truncateDisplayWithGlyphProfile(
				choiceView(choice, index == selected, true),
				width,
				m.glyphProfile,
			),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// approvalContentWidth gives commands and diffs the same horizontal room as
// the conversation. Only the small inline indent is reserved; unlike picker
// overlays this surface does not cap useful content at an arbitrary width.
func (m *Model) approvalContentWidth() int {
	return max(1, m.chatPaneWidth()-2)
}

// approvalBodyHeight keeps enough transcript visible to preserve context while
// allowing consequential requests to expose useful detail. The Bubbles
// viewport owns overflow, so minimum terminals retain the action, target, and
// complete decision grammar without pushing the surface off-screen.
func (m *Model) approvalBodyHeight() int {
	switch {
	case m.height <= 12:
		return 2
	case m.height < 20:
		return 4
	case m.height < 30:
		return 8
	default:
		return 12
	}
}

func indentApprovalSurface(value string, indent, width int, profiles ...GlyphProfile) string {
	profile := resolveGlyphProfile(profiles...)
	prefix := strings.Repeat(" ", max(0, indent))
	lineWidth := max(1, width-lipgloss.Width(prefix))
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lines[index] = prefix + truncateDisplayWithGlyphProfile(line, lineWidth, profile)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) buildApprovalContent(width int) string {
	if m.pendingApproval == nil {
		return ""
	}
	if m.approvalState != nil && m.approvalState.ShowArguments {
		return m.buildApprovalArguments(width)
	}
	return m.buildApprovalPreview(width)
}

func (m *Model) buildApprovalPreview(width int) string {
	request := m.pendingApproval
	preview := request.Preview
	var lines []string

	appendRow := func(label, value string) {
		value = sanitizeApprovalMetadata(value)
		if value == "" {
			return
		}
		labelWidth := min(10, max(6, width/5))
		available := max(1, width-labelWidth-1)
		wrapped := strings.Split(wrapText(value, available), "\n")
		lines = append(lines, m.styles.OverlayAccent.Width(labelWidth).Render(label)+" "+wrapped[0])
		for _, continuation := range wrapped[1:] {
			lines = append(lines, strings.Repeat(" ", labelWidth+1)+continuation)
		}
	}
	appendPathRow := func(label, value string) {
		value = sanitizeApprovalMetadata(value)
		if value == "" {
			return
		}
		if m.approvalBodyHeight() <= 2 {
			labelWidth := min(10, max(6, width/5))
			available := max(1, width-labelWidth-1)
			value = compactWorkspacePath(value, available)
		}
		appendRow(label, value)
	}

	actionLabel := boundedApprovalMetadata(preview.ActionLabel, approvalMaximumActionBytes)
	hasCustomAction := actionLabel != ""
	if !hasCustomAction {
		actionLabel = boundedApprovalMetadata(request.ToolName, approvalMaximumActionBytes)
	}
	switch preview.Kind {
	case permission.PreviewFileWrite:
		if !hasCustomAction {
			actionLabel = "Write " + formatApprovalBytes(preview.ByteSize)
		}
		appendRow("Action", actionLabel)
		appendPathRow("Target", preview.Path)
	case permission.PreviewFilePatch:
		if !hasCustomAction {
			actionLabel = "Patch file"
		}
		appendRow("Action", actionLabel)
		appendPathRow("Target", preview.Path)
	case permission.PreviewCommand:
		if !hasCustomAction {
			actionLabel = "Run command"
		}
		appendRow("Action", actionLabel)
	case permission.PreviewFilesystem:
		if !hasCustomAction {
			actionLabel = "Change filesystem"
		}
		appendRow("Action", actionLabel)
		appendPathRow("Path", preview.Path)
		appendPathRow("From", preview.SourcePath)
		appendPathRow("To", preview.DestinationPath)
	default:
		appendRow("Action", "Run "+actionLabel)
		appendPathRow("Target", preview.Path)
	}
	appendRow("Impact", boundedApprovalMetadata(preview.Consequence, approvalMaximumConsequenceBytes))
	appendRow("Scope", approvalScopeLabel(request.Scope))
	digest := request.ArgumentsSHA256
	if digest == "" {
		digest = preview.ArgumentsSHA256
	}
	appendRow("Request", shortApprovalDigest(digest))

	switch preview.Kind {
	case permission.PreviewCommand:
		lines = append(lines, "", m.styles.OverlayAccent.Render("Command"))
		lines = append(lines, approvalWrappedLines(preview.Command, width)...)
	case permission.PreviewFileWrite, permission.PreviewFilePatch:
		lines = append(lines, "", m.styles.OverlayAccent.Render("Proposed change"))
		switch {
		case preview.Diff != "":
			lines = append(lines, m.renderApprovalDiff(preview.Diff, width)...)
			if preview.DiffTruncated {
				lines = append(lines, m.styles.OverlayDim.Render("Diff preview truncated; press d for exact arguments."))
			}
		case preview.DiffOmittedReason != "":
			lines = append(lines, m.styles.OverlayDim.Render(wrapText(preview.DiffOmittedReason, width)))
			lines = append(lines, m.styles.OverlayDim.Render("Press d to inspect the exact arguments."))
		default:
			lines = append(lines, m.styles.OverlayDim.Render("No textual difference detected."))
		}
	case permission.PreviewGeneric:
		lines = append(lines, "", m.styles.OverlayDim.Render("Press d to inspect the exact arguments."))
	}

	return strings.Join(lines, "\n")
}

func boundedApprovalMetadata(value string, maximumBytes int) string {
	value = sanitizeApprovalMetadata(value)
	if maximumBytes <= 0 || value == "" {
		return ""
	}
	if len(value) <= maximumBytes {
		return value
	}
	marker := "..."
	limit := maximumBytes - len(marker)
	if limit <= 0 {
		return marker[:maximumBytes]
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return strings.TrimSpace(value[:limit]) + marker
}

// sanitizeApprovalMetadata treats every label supplied by a model or MCP
// server as untrusted terminal data. Exact arguments remain available in the
// JSON details view; presentation metadata must never be able to emit ANSI,
// OSC, C0/C1, newline, tab, or bidi reordering controls into the decision UI.
func sanitizeApprovalMetadata(value string) string {
	value = sanitizeApprovalPreviewLine(value)
	return strings.Join(strings.Fields(value), " ")
}

// sanitizeApprovalPreviewLine preserves ordinary spacing in commands, diffs,
// and JSON while removing sequences that can change terminal state or visual
// ordering. Callers pass one logical line at a time.
func sanitizeApprovalPreviewLine(value string) string {
	value = sanitizeTerminalMultiline(value)
	return strings.NewReplacer("\t", "    ", "\n", " ").Replace(value)
}

func (m *Model) buildApprovalArguments(width int) string {
	encoded, err := json.MarshalIndent(m.pendingApproval.Args, "", "  ")
	if err != nil {
		return m.styles.OverlayDim.Render("Exact arguments unavailable: " + err.Error())
	}
	lines := []string{
		m.styles.OverlayAccent.Render("Exact arguments"),
		m.styles.OverlayDim.Render("Bound to " + shortApprovalDigest(m.pendingApproval.ArgumentsSHA256)),
		"",
	}
	for _, line := range strings.Split(string(encoded), "\n") {
		wrapped := approvalWrappedLines(line, width)
		if len(wrapped) == 0 {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapped...)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderApprovalDiff(diff string, width int) []string {
	rawLines := strings.Split(diff, "\n")
	lines := make([]string, 0, len(rawLines))
	for index, line := range rawLines {
		// The line diff for a new file can begin with a synthetic deletion of
		// the empty preimage ("-"). It carries no reviewable material and can
		// consume the last visible preview row, hiding the actual addition.
		if line == "-" && index+1 < len(rawLines) && strings.HasPrefix(rawLines[index+1], "+") {
			continue
		}
		style := m.styles.DiffContext.PaddingLeft(0)
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			style = m.styles.DiffAdded.PaddingLeft(0)
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			style = m.styles.DiffRemoved.PaddingLeft(0)
		case strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			style = m.styles.DiffHeader.PaddingLeft(0)
		}
		wrapped := approvalWrappedLines(line, width)
		if len(wrapped) == 0 {
			lines = append(lines, "")
			continue
		}
		for _, segment := range wrapped {
			lines = append(lines, style.Render(segment))
		}
	}
	return lines
}

func approvalWrappedLines(value string, width int) []string {
	value = sanitizeApprovalPreviewLine(value)
	if value == "" {
		return nil
	}
	return strings.Split(wrapText(value, max(1, width)), "\n")
}

func approvalScopeLabel(scope permission.ApprovalScope) string {
	if scope.Kind == permission.ScopeExactRequest {
		return "Identical request only · current Agent session"
	}
	return "This request only"
}

func shortApprovalDigest(digest string) string {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return "unavailable"
	}
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func formatApprovalBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
}

// handleToolApprovalRequest opens the interactive approval prompt, or settles
// the request as host cancellation when the application is shutting down.
func (m *Model) handleToolApprovalRequest(msg ToolApprovalMsg) {
	if m.shuttingDown {
		// A callback may cross the shutdown boundary after the active turn has
		// already been cancelled. Never reopen interactive authority while the
		// host is joining receipts, and never mislabel host cancellation as a
		// human denial. Production response channels are buffered; the
		// non-blocking send also keeps malformed or already-settled adapters from
		// freezing the Bubble Tea parent during shutdown.
		if msg.Response != nil {
			select {
			case msg.Response <- permission.Cancelled("application is shutting down"):
			default:
			}
		}
		return
	}
	if err := m.openApproval(msg); err != nil {
		if msg.Response != nil {
			msg.Response <- permission.Refuse("approval_preview_unavailable", err.Error())
		}
		m.entries = append(m.entries, ChatEntry{
			Kind:    "error",
			Content: "Approval preview unavailable: " + err.Error(),
		})
		m.invalidateRenderedCache()
		m.refreshTranscript()
		m.gotoBottomIfFollowing()
	}
}

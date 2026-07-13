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
	approvalMaximumWidth            = 86
	approvalMaximumActionBytes      = 256
	approvalMaximumConsequenceBytes = 768
)

// ApprovalState owns presentation-only state for an approval request. The
// root Model remains responsible for every decision and side effect.
type ApprovalState struct {
	Viewport      viewport.Model
	ShowArguments bool
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

	m.pendingApproval = &request
	m.approvalState = &ApprovalState{}
	m.overlayParent = OverlayNone
	m.overlay = OverlayApproval
	m.input.Blur()
	m.resizeApproval(false)
	m.recalcViewportHeight()
	return nil
}

func (m *Model) resolvePendingApproval(response permission.ApprovalResponse) {
	if m.pendingApproval != nil && m.pendingApproval.Response != nil {
		m.pendingApproval.Response <- response
	}
	m.pendingApproval = nil
	m.approvalState = nil
	if m.overlay == OverlayApproval {
		m.overlay = OverlayNone
		m.overlayParent = OverlayNone
	}
	if !m.shuttingDown {
		m.input.Focus()
	}
	m.recalcViewportHeight()
}

func (m *Model) toggleApprovalDetails() {
	if m.approvalState == nil || m.pendingApproval == nil {
		return
	}
	m.approvalState.ShowArguments = !m.approvalState.ShowArguments
	m.resizeApproval(false)
}

func (m *Model) resizeApproval(preserveOffset bool) {
	if m.approvalState == nil || m.pendingApproval == nil {
		return
	}
	offset := 0
	if preserveOffset {
		offset = m.approvalState.Viewport.YOffset()
	}
	width := pickerListWidth(m.width, approvalMaximumWidth)
	content := m.buildApprovalContent(width)
	bodyHeight := min(max(1, lipgloss.Height(content)), max(2, m.height-7))
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
	contentWidth := pickerListWidth(m.width, approvalMaximumWidth)
	toolName := boundedApprovalMetadata(m.pendingApproval.ToolName, approvalMaximumActionBytes)
	if toolName == "" {
		toolName = "unknown tool"
	}
	title := m.styles.OverlayTitle.Render(truncateDisplay("Permission · "+toolName, contentWidth))
	body := title + "\n" + m.approvalState.Viewport.View()
	detailAction := "arguments"
	if m.approvalState.ShowArguments {
		detailAction = "preview"
	}
	hints := m.renderKeyHints(contentWidth,
		keyHint{Key: "esc", Action: "cancel"},
		keyHint{Key: "y", Action: "once"},
		keyHint{Key: "s", Action: "session"},
		keyHint{Key: "n", Action: "deny"},
		keyHint{Key: "d", Action: detailAction},
		keyHint{Key: "j/k", Action: "scroll"},
	)
	return m.renderPickerFrame(body, approvalMaximumWidth, hints)
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
		appendRow("Target", preview.Path)
	case permission.PreviewFilePatch:
		if !hasCustomAction {
			actionLabel = "Patch file"
		}
		appendRow("Action", actionLabel)
		appendRow("Target", preview.Path)
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
		appendRow("Path", preview.Path)
		appendRow("From", preview.SourcePath)
		appendRow("To", preview.DestinationPath)
	default:
		appendRow("Action", "Run "+actionLabel)
		appendRow("Target", preview.Path)
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
	lines := make([]string, 0, strings.Count(diff, "\n")+1)
	for _, line := range strings.Split(diff, "\n") {
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
		return "This exact request · current Agent session"
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

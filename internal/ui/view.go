package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
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

	// Session header + conversation + footer share one geometry snapshot.
	// Infrequent controls remain overlays over these stable base rectangles.
	m.syncTranscriptPaintWindow()
	frame := m.projectFrame()
	// Keep the Bubbles viewport geometry locked to the projected transcript
	// rect so footer chrome changes (framed composer, sticky header) cannot
	// leave a stale tall viewport and overflow the terminal height.
	if h := frame.Transcript.Rect.Height(); h > 0 && m.viewport.Height() != h {
		m.viewport.SetHeight(h)
	}
	if w := frame.Transcript.Rect.Width(); w > 0 && m.viewport.Width() != w {
		m.viewport.SetWidth(w)
	}
	var content strings.Builder
	if frame.Header.Visible && frame.Header.Content != "" {
		content.WriteString(frame.Header.Content)
		content.WriteString("\n")
	}
	content.WriteString(m.viewport.View())
	content.WriteString("\n")
	paintedFooterY := strings.Count(content.String(), "\n")
	content.WriteString(frame.Footer.Content)
	viewCursor := frame.Cursor
	if viewCursor != nil && paintedFooterY != frame.Footer.Rect.MinY {
		// Tests and a few setup paths can replace an owner immediately before a
		// viewport reflow. Keep the single projected local cursor, but translate
		// it to the footer's actually painted origin for that transitional frame.
		viewCursor = offsetCursor(viewCursor, 0, paintedFooterY-frame.Footer.Rect.MinY)
	}

	// Infrequent controls remain centered overlays. Composer-owned completion,
	// transcript search, Plan, and Goal surfaces were already rendered in the
	// normal footer flow.
	if m.overlay != OverlayNone && m.overlay != OverlayCompletion &&
		m.overlay != OverlayTranscriptSearch &&
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
		case OverlayPermissions:
			overlay = m.renderPermissionsPanel()
		case OverlayAgentPicker:
			overlay = m.renderAgentPicker()
		case OverlayProviderPicker:
			overlay = m.renderProviderPicker()
		case OverlayAgents:
			overlay = m.renderAgentHub()
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
	if m.viewerModalActive() {
		viewCursor = nil
		if composed, modalCursor, ok := m.composeViewerModal(content.String()); ok {
			content.Reset()
			content.WriteString(composed)
			viewCursor = modalCursor
		}
	}

	v := tea.NewView(content.String() + "\n")
	v.AltScreen = true
	// Cell-motion reports wheel + clicks for tool hit targets. Native terminal
	// selection still works via Shift-drag in most terminals (iTerm, Ghostty,
	// Kitty, WezTerm). Ctrl+Y copies the last assistant message when idle.
	// Prefer Shift-drag over disabling mouse: wheel scroll of the transcript
	// is a daily-driver affordance.
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
		if m.hasSuccessFooterNotice() {
			v.WindowTitle = windowTitle + " \u00b7 done"
		} else {
			v.WindowTitle = windowTitle
		}
	}

	return v
}

// activityComposerGap used to insert a blank row between the live activity rail
// and the composer. Grok-style density keeps them adjacent; the framed composer
// already provides its own top edge, so the gap is never requested.
func (m *Model) activityComposerGap() bool {
	return false
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

// formatTokens formats a token count as "1.0M", "1.2k", or "999". The M tier
// keeps Cloud-scale context windows readable in the absolute meter
// ("120.4k/1.0M" instead of "120.4k/1048.6k").
func formatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// formatDuration formats a duration as "42ms" or "1.3s".
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// truncateDisplay truncates plain text by terminal cell width instead of byte
// count, so model names, paths, and tool output containing Unicode stay valid.
func truncateDisplay(s string, maxWidth int) string {
	return truncateDisplayWithGlyphProfile(s, maxWidth, GlyphUnicode)
}

func truncateDisplayWithGlyphProfile(s string, maxWidth int, profile GlyphProfile) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	marker := "…"
	if resolveGlyphProfile(profile) == GlyphASCII {
		marker = "~"
	}
	if maxWidth <= lipgloss.Width(marker) {
		return marker
	}

	budget := maxWidth - lipgloss.Width(marker)
	var b strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(s)
	for graphemes.Next() {
		cluster := graphemes.Str()
		clusterWidth := lipgloss.Width(cluster)
		if used+clusterWidth > budget {
			break
		}
		b.WriteString(cluster)
		used += clusterWidth
	}
	return b.String() + marker
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
	graphemes := uniseg.NewGraphemes(word)
	for graphemes.Next() {
		cluster := graphemes.Str()
		clusterWidth := lipgloss.Width(cluster)
		if used > 0 && used+clusterWidth > width {
			chunks = append(chunks, chunk.String())
			chunk.Reset()
			used = 0
		}
		chunk.WriteString(cluster)
		used += clusterWidth
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

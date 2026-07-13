package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

type RuntimeStatusState struct {
	Viewport viewport.Model
}

func (m *Model) openRuntimeStatus() {
	m.refreshRuntimeStatus(false)
	m.overlay = OverlayRuntimeStatus
	m.input.Blur()
}

func (m *Model) closeRuntimeStatus() {
	m.runtimeStatusState = nil
	m.closeOverlayToParent()
}

func (m *Model) refreshRuntimeStatus(preserveOffset bool) {
	offset := 0
	if preserveOffset && m.runtimeStatusState != nil {
		offset = m.runtimeStatusState.Viewport.YOffset()
	}
	width := pickerListWidth(m.width, 58)
	content := m.buildRuntimeStatusContent(width)
	height := min(max(1, lipgloss.Height(content)), max(1, m.height-5))
	vp := viewport.New(
		viewport.WithWidth(width),
		viewport.WithHeight(height),
	)
	vp.KeyMap.Up.SetEnabled(false)
	vp.KeyMap.Down.SetEnabled(false)
	vp.KeyMap.PageUp.SetEnabled(false)
	vp.KeyMap.PageDown.SetEnabled(false)
	vp.KeyMap.HalfPageUp.SetEnabled(false)
	vp.KeyMap.HalfPageDown.SetEnabled(false)
	vp.SetContent(content)
	vp.SetYOffset(offset)
	m.runtimeStatusState = &RuntimeStatusState{Viewport: vp}
}

func (m *Model) buildRuntimeStatusContent(width int) string {
	profile := m.agentProfile
	if profile == "" {
		profile = "Default"
	}
	routing := "Pinned"
	if !m.modelPinned {
		routing = "Auto"
	}
	lines := make([]string, 0, 18)
	toolCount := m.toolCount
	var serverNames []string
	failedServers := append([]FailedServer(nil), m.failedServers...)
	serverToolCounts := make(map[string]int)
	if m.agent != nil {
		toolCount = m.agent.ToolCount()
		statuses := m.agent.MCPConnectionStatuses()
		if len(statuses) > 0 {
			serverNames = make([]string, 0, len(statuses))
			failedServers = make([]FailedServer, 0, len(statuses))
			for _, status := range statuses {
				serverToolCounts[strings.ToLower(status.Name)] = status.ToolCount
				if status.Connected {
					serverNames = append(serverNames, status.Name)
				} else {
					failedServers = append(failedServers, FailedServer{Name: status.Name, Reason: status.LastError})
				}
			}
		}
	}
	connections := projectEcosystemConnections(serverNames, failedServers)
	if m.agent != nil {
		if workspace := compactWorkspacePath(m.agent.WorkDir(), runtimeStatusValueWidth(width)); workspace != "" {
			lines = append(lines, m.runtimeStatusRow("Workspace", workspace, width))
		}
	}
	lines = append(lines,
		m.runtimeStatusRow("Model", routing+" · "+m.model, width),
		m.runtimeStatusRow("Profile", profile, width),
		m.runtimeStatusRow("Mode", m.modeConfigs[m.mode].Label, width),
		m.runtimeStatusRow("Tools", fmt.Sprintf("%d available", toolCount), width),
		m.runtimeStatusRow("MCP", summarizeConnectionHealth(connections), width),
	)
	if m.promptTokens > 0 && m.numCtx > 0 {
		percent := min(100, max(0, m.promptTokens*100/m.numCtx))
		lines = append(lines, m.runtimeStatusRow("Context",
			fmt.Sprintf("~%s / %s · %d%%", formatTokens(m.promptTokens), formatTokens(m.numCtx), percent),
			width,
		))
	}
	if m.sessionTurnCount > 0 {
		lines = append(lines, m.runtimeStatusRow("Session",
			fmt.Sprintf("%d turns · %s output", m.sessionTurnCount, formatTokens(m.sessionEvalTotal)),
			width,
		))
	}
	if m.iceEnabled {
		lines = append(lines, m.runtimeStatusRow("ICE", fmt.Sprintf("enabled · %d conversations", m.iceConversations), width))
	} else {
		lines = append(lines, m.runtimeStatusRow("ICE", "disabled", width))
	}
	if len(connections) > 0 {
		lines = append(lines, "", m.styles.OverlayAccent.Render("Tool connections"))
		for _, connection := range connections {
			value := connection.Health.String() + " · " + connection.Role
			if count := serverToolCounts[strings.ToLower(connection.Name)]; connection.Health == capabilityConnected && count > 0 {
				value += fmt.Sprintf(" · %d tools", count)
			}
			lines = append(lines, m.runtimeStatusRow(connection.Label, value, width))
			if connection.Health == capabilityUnavailable {
				if connection.Detail != "" {
					lines = append(lines, m.runtimeStatusRow("", connection.Detail, width))
				}
				if connection.Recovery != "" {
					lines = append(lines, m.runtimeStatusRow("", connection.Recovery, width))
				}
			}
		}
	} else {
		lines = append(lines, "", m.styles.OverlayDim.Render(
			wrapText("MCP is optional. Add a server in local-agent.yaml or the XDG config when you need ecosystem tools.", max(1, width)),
		))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) runtimeStatusRow(label, value string, width int) string {
	valueWidth := runtimeStatusValueWidth(width)
	labelWidth := max(1, width-valueWidth)
	wrapped := strings.Split(wrapText(strings.TrimSpace(value), valueWidth), "\n")
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}

	var b strings.Builder
	b.WriteString(m.styles.OverlayAccent.Width(labelWidth).Render(truncateDisplay(label, labelWidth-1)))
	b.WriteString(m.styles.OverlayDim.Render(wrapped[0]))
	for _, line := range wrapped[1:] {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat(" ", labelWidth))
		b.WriteString(m.styles.OverlayDim.Render(line))
	}
	return b.String()
}

func runtimeStatusValueWidth(width int) int {
	const normalLabelWidth = 11
	labelWidth := min(normalLabelWidth, max(1, width/3))
	return max(1, width-labelWidth)
}

func displayWorkspacePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if home, err := os.UserHomeDir(); err == nil {
		home = filepath.Clean(home)
		if clean == home {
			return "~"
		}
		if strings.HasPrefix(clean, home+string(filepath.Separator)) {
			return "~" + strings.TrimPrefix(clean, home)
		}
	}
	return clean
}

// compactWorkspacePath keeps the identifying end of a workspace visible at
// narrow widths. A leading path fragment is less useful than the repository
// name and its immediate parent.
func compactWorkspacePath(path string, width int) string {
	display := displayWorkspacePath(path)
	if display == "" || lipgloss.Width(display) <= width {
		return display
	}

	base := filepath.Base(display)
	parent := filepath.Base(filepath.Dir(display))
	if parent != "." && parent != string(filepath.Separator) {
		candidate := "…" + string(filepath.Separator) + filepath.Join(parent, base)
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	candidate := "…" + string(filepath.Separator) + base
	if lipgloss.Width(candidate) <= width {
		return candidate
	}
	return truncateDisplay(base, width)
}

func (m *Model) renderRuntimeStatus() string {
	if m.runtimeStatusState == nil {
		return ""
	}
	vp := &m.runtimeStatusState.Viewport
	content := m.styles.OverlayTitle.Render("Runtime") + "\n\n" + vp.View()
	hints := []keyHint{{Key: "esc/q", Action: m.overlayCloseLabel()}}
	if !vp.AtBottom() {
		hints = append(hints,
			keyHint{Key: "j/k", Action: "scroll"},
			keyHint{Key: "↓", Action: "more"},
		)
	} else if vp.YOffset() > 0 {
		hints = append(hints,
			keyHint{Key: "j/k", Action: "scroll"},
			keyHint{Key: fmt.Sprintf("%.0f%%", vp.ScrollPercent()*100)},
		)
	}
	return m.renderPickerFrame(content, 58, m.renderKeyHints(pickerListWidth(m.width, 58), hints...))
}

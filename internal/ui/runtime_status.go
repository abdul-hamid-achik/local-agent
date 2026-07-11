package ui

import (
	"fmt"
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
	lines := []string{
		m.styles.OverlayAccent.Render("Model") + "  " + m.styles.OverlayDim.Render(routing+" · "+m.model),
		m.styles.OverlayAccent.Render("Profile") + "  " + m.styles.OverlayDim.Render(profile),
		m.styles.OverlayAccent.Render("Mode") + "  " + m.styles.OverlayDim.Render(m.modeConfigs[m.mode].Label),
		m.styles.OverlayAccent.Render("Tools") + "  " + m.styles.OverlayDim.Render(fmt.Sprintf("%d across %d MCP servers", m.toolCount, m.serverCount)),
	}
	if m.promptTokens > 0 && m.numCtx > 0 {
		percent := min(100, max(0, m.promptTokens*100/m.numCtx))
		lines = append(lines, m.styles.OverlayAccent.Render("Context")+"  "+m.styles.OverlayDim.Render(
			fmt.Sprintf("~%s / %s · %d%%", formatTokens(m.promptTokens), formatTokens(m.numCtx), percent),
		))
	}
	if m.sessionTurnCount > 0 {
		lines = append(lines, m.styles.OverlayAccent.Render("Session")+"  "+m.styles.OverlayDim.Render(
			fmt.Sprintf("%d turns · %s output", m.sessionTurnCount, formatTokens(m.sessionEvalTotal)),
		))
	}
	if m.iceEnabled {
		lines = append(lines, m.styles.OverlayAccent.Render("ICE")+"  "+m.styles.OverlayDim.Render(fmt.Sprintf("enabled · %d conversations", m.iceConversations)))
	} else {
		lines = append(lines, m.styles.OverlayAccent.Render("ICE")+"  "+m.styles.OverlayDim.Render("disabled"))
	}
	if m.agent != nil {
		if names := m.agent.ServerNames(); len(names) > 0 {
			lines = append(lines, "", m.styles.OverlayAccent.Render("Connected"))
			lines = append(lines, m.styles.OverlayDim.Render(wrapText(strings.Join(names, " · "), max(1, width))))
		}
	}
	if len(m.failedServers) > 0 {
		lines = append(lines, "", m.styles.ErrorText.Render("Connection failures"))
		for _, failed := range m.failedServers {
			line := failed.Name
			if failed.Reason != "" {
				line += " · " + failed.Reason
			}
			lines = append(lines, m.styles.OverlayDim.Render(wrapText(line, max(1, width))))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderRuntimeStatus() string {
	if m.runtimeStatusState == nil {
		return ""
	}
	vp := &m.runtimeStatusState.Viewport
	content := m.styles.OverlayTitle.Render("Runtime Status") + "\n\n" + vp.View()
	closeHint := "Esc/q " + m.overlayCloseLabel()
	footer := closeHint
	if !vp.AtBottom() {
		footer += " · j/k · ↓ more"
	} else if vp.YOffset() > 0 {
		footer += fmt.Sprintf(" · %.0f%% · j/k", vp.ScrollPercent()*100)
	}
	return m.renderPickerFrame(content, 58, footer)
}

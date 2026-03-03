package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// processStreamChunk processes a streaming chunk, extracting <think>...</think> tags.
// It handles tag boundaries that may be split across chunks.
func processStreamChunk(chunk string, inThinking bool, searchBuf string) (mainText, thinkText string, outInThinking bool, outSearchBuf string) {
	combined := searchBuf + chunk
	outInThinking = inThinking

	var mainBuf, thinkBuf strings.Builder

	for len(combined) > 0 {
		if outInThinking {
			idx := strings.Index(combined, "</think>")
			if idx >= 0 {
				thinkBuf.WriteString(combined[:idx])
				combined = combined[idx+len("</think>"):]
				outInThinking = false
				continue
			}
			partial := hasPartialTagSuffix(combined, "</think>")
			if partial > 0 {
				thinkBuf.WriteString(combined[:len(combined)-partial])
				outSearchBuf = combined[len(combined)-partial:]
				return mainBuf.String(), thinkBuf.String(), outInThinking, outSearchBuf
			}
			thinkBuf.WriteString(combined)
			combined = ""
		} else {
			idx := strings.Index(combined, "<think>")
			if idx >= 0 {
				mainBuf.WriteString(combined[:idx])
				combined = combined[idx+len("<think>"):]
				outInThinking = true
				continue
			}
			partial := hasPartialTagSuffix(combined, "<think>")
			if partial > 0 {
				mainBuf.WriteString(combined[:len(combined)-partial])
				outSearchBuf = combined[len(combined)-partial:]
				return mainBuf.String(), thinkBuf.String(), outInThinking, outSearchBuf
			}
			mainBuf.WriteString(combined)
			combined = ""
		}
	}

	return mainBuf.String(), thinkBuf.String(), outInThinking, outSearchBuf
}

// hasPartialTagSuffix returns the length of the longest suffix of s
// that is a proper prefix of tag (not the full tag).
func hasPartialTagSuffix(s, tag string) int {
	maxCheck := len(tag) - 1
	if maxCheck > len(s) {
		maxCheck = len(s)
	}
	for i := maxCheck; i > 0; i-- {
		if strings.HasSuffix(s, tag[:i]) {
			return i
		}
	}
	return 0
}

// renderThinkingBox renders a collapsible thinking content box.
func (m *Model) renderThinkingBox(content string, collapsed bool) string {
	if content == "" {
		return ""
	}

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")

	var b strings.Builder

	if collapsed {
		hidden := len(lines) - 3
		if hidden < 0 {
			hidden = 0
		}
		header := fmt.Sprintf("▸ thinking (%d lines)", len(lines))
		if hidden > 0 {
			header += fmt.Sprintf(" — %d hidden, ctrl+t to expand", hidden)
		}
		b.WriteString(m.styles.ThinkingHeader.Render(header))
		b.WriteString("\n")

		start := len(lines) - 3
		if start < 0 {
			start = 0
		}
		for _, line := range lines[start:] {
			b.WriteString(m.styles.ThinkingContent.Render(line))
			b.WriteString("\n")
		}
	} else {
		header := fmt.Sprintf("▾ thinking (%d lines) — ctrl+t to collapse", len(lines))
		b.WriteString(m.styles.ThinkingHeader.Render(header))
		b.WriteString("\n")
		for _, line := range lines {
			b.WriteString(m.styles.ThinkingContent.Render(line))
			b.WriteString("\n")
		}
	}

	boxWidth := m.width - 8
	if boxWidth < 20 {
		boxWidth = 20
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.OverlayBorder).
		Padding(0, 2).
		Width(boxWidth)

	return box.Render(strings.TrimRight(b.String(), "\n"))
}

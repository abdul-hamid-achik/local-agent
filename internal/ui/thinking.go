package ui

import (
	"crypto/sha256"
	"strings"

	"charm.land/lipgloss/v2"
)

func reasoningReceiptDigest(content string) [sha256.Size]byte {
	return sha256.Sum256([]byte(content))
}

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
	content = strings.Trim(sanitizeTerminalMultiline(content), "\r\n")
	if strings.TrimSpace(content) == "" {
		return ""
	}

	// The caller indents this block by two cells. Bound it to the same readable
	// transcript width as assistant prose instead of expanding to the terminal
	// edge on wide screens.
	width := max(4, m.chatContentWidth()-2)
	inner := max(1, width-2) // left rail plus one separating space
	direction := "▸"
	if !collapsed {
		direction = "▾"
	}
	header := thinkingHeader(direction, inner)

	bar := m.styles.ThinkingBorder.Render("│")
	var b strings.Builder
	b.WriteString(bar)
	b.WriteByte(' ')
	b.WriteString(m.styles.ThinkingHeader.Render(header))

	// Keep the settled receipt intentionally quiet. The global help surface owns
	// the disclosure shortcut; repeating it on every assistant turn makes the
	// transcript read like control chrome instead of a conversation.
	if collapsed {
		return b.String()
	}

	lines := strings.Split(content, "\n")
	for _, sourceLine := range lines {
		wrapped := wrapText(sourceLine, inner)
		if wrapped == "" {
			wrapped = " "
		}
		for _, line := range strings.Split(wrapped, "\n") {
			b.WriteByte('\n')
			b.WriteString(bar)
			b.WriteByte(' ')
			b.WriteString(m.styles.ThinkingContent.UnsetPaddingLeft().Render(line))
		}
	}
	return b.String()
}

// renderLiveThinkingBox is the stable in-progress counterpart to a completed
// reasoning disclosure. Thinking belongs to the assistant transcript; the
// footer is reserved for operational controls such as cancel and queue.
func (m *Model) renderLiveThinkingBox(content string) string {
	width := max(4, m.chatContentWidth()-2)
	inner := max(1, width-2)
	summary := liveThinkingSummary(content)
	header := "Thinking…"
	if summary != "" {
		header += " · " + summary
	}
	if lipgloss.Width(header) > inner {
		header = truncateDisplay(header, inner)
	}
	return m.styles.ThinkingBorder.Render("│") + " " +
		m.styles.ThinkingHeader.Render(header)
}

func liveThinkingSummary(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if summary := sanitizeTerminalSingleLine(lines[index]); summary != "" {
			return summary
		}
	}
	return ""
}

func thinkingHeader(direction string, width int) string {
	header := direction + " Thought"
	if lipgloss.Width(header) <= width {
		return header
	}
	return truncateDisplay(header, width)
}

package tui

import (
	"fmt"
	"regexp"
	"strings"
)

// ToolType represents the category of a tool for rendering.
type ToolType int

const (
	ToolTypeDefault ToolType = iota
	ToolTypeBash
	ToolTypeFileRead
	ToolTypeFileWrite
	ToolTypeWeb
	ToolTypeMemory
)

// classifyTool returns the ToolType based on the tool name.
func classifyTool(name string) ToolType {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "bash") || strings.Contains(lower, "exec") || strings.Contains(lower, "shell") || strings.Contains(lower, "command"):
		return ToolTypeBash
	case strings.Contains(lower, "read") || strings.Contains(lower, "view") || strings.Contains(lower, "cat"):
		return ToolTypeFileRead
	case strings.Contains(lower, "write") || strings.Contains(lower, "edit") || strings.Contains(lower, "create_file") || strings.Contains(lower, "patch"):
		return ToolTypeFileWrite
	case strings.Contains(lower, "web") || strings.Contains(lower, "fetch") || strings.Contains(lower, "http") || strings.Contains(lower, "curl") || strings.Contains(lower, "browse"):
		return ToolTypeWeb
	case strings.Contains(lower, "memory") || strings.Contains(lower, "remember") || strings.Contains(lower, "forget"):
		return ToolTypeMemory
	default:
		return ToolTypeDefault
	}
}

// toolIcon returns a type-specific icon for the tool.
func toolIcon(tt ToolType, status ToolStatus) string {
	if status == ToolStatusError {
		return "✗"
	}
	if status == ToolStatusDone {
		switch tt {
		case ToolTypeBash:
			return "$"
		case ToolTypeFileRead:
			return "◎"
		case ToolTypeFileWrite:
			return "✎"
		case ToolTypeWeb:
			return "◆"
		case ToolTypeMemory:
			return "◈"
		default:
			return "✓"
		}
	}
	// Running
	return "⚙"
}

// toolSummary extracts a key argument for display based on tool type.
func toolSummary(tt ToolType, te ToolEntry) string {
	if te.RawArgs == nil {
		return ""
	}
	switch tt {
	case ToolTypeBash:
		if cmd, ok := te.RawArgs["command"].(string); ok {
			if len(cmd) > 60 {
				cmd = cmd[:57] + "..."
			}
			return cmd
		}
	case ToolTypeFileRead, ToolTypeFileWrite:
		for _, key := range []string{"path", "file_path", "filename", "file"} {
			if p, ok := te.RawArgs[key].(string); ok {
				return p
			}
		}
	case ToolTypeWeb:
		for _, key := range []string{"url", "uri", "href"} {
			if u, ok := te.RawArgs[key].(string); ok {
				if len(u) > 60 {
					u = u[:57] + "..."
				}
				return u
			}
		}
	case ToolTypeMemory:
		if k, ok := te.RawArgs["key"].(string); ok {
			return k
		}
	}
	return ""
}

// codeBlockRegex matches markdown code blocks.
var codeBlockRegex = regexp.MustCompile(`(?s)~~~(\w*)\n(.*?)~~~|` + "```(\\w*)\\n(.*?)```")

// detectCodeBlocks checks if the result contains markdown code blocks.
func detectCodeBlocks(text string) bool {
	return strings.Contains(text, "```") || strings.Contains(text, "~~~")
}

// extractCodeBlocks extracts code blocks from text and returns them with their language.
func extractCodeBlocks(text string) []struct {
	Language string
	Code     string
} {
	var blocks []struct {
		Language string
		Code     string
	}

	lines := strings.Split(text, "\n")
	var inBlock bool
	var currentLang string
	var currentCode strings.Builder

	for _, line := range lines {
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			if !inBlock {
				// Start of code block
				inBlock = true
				currentLang = strings.TrimPrefix(line, "```")
				currentLang = strings.TrimPrefix(currentLang, "~~~")
				currentLang = strings.TrimSpace(currentLang)
				currentCode.Reset()
			} else {
				// End of code block
				inBlock = false
				blocks = append(blocks, struct {
					Language string
					Code     string
				}{
					Language: currentLang,
					Code:     strings.TrimRight(currentCode.String(), "\n"),
				})
			}
		} else if inBlock {
			currentCode.WriteString(line)
			currentCode.WriteString("\n")
		}
	}

	return blocks
}

// formatToolResult formats a tool result for display with smart truncation.
// It preserves code blocks and adds expand/collapse hints.
func formatToolResult(result string, maxLines int, maxWidth int) string {
	if result == "" {
		return "(no output)"
	}

	lines := strings.Split(result, "\n")
	
	// Detect if result contains code blocks
	hasCodeBlocks := detectCodeBlocks(result)
	
	// Truncate by lines if too long
	if len(lines) > maxLines {
		var b strings.Builder
		for i := 0; i < maxLines; i++ {
			line := lines[i]
			// Truncate long lines
			if len(line) > maxWidth {
				line = line[:maxWidth-3] + "..."
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		remaining := len(lines) - maxLines
		b.WriteString("... ")
		if hasCodeBlocks {
			b.WriteString("(code blocks truncated)")
		} else {
			b.WriteString(fmt.Sprintf("%d", remaining))
			b.WriteString(" more lines")
		}
		return b.String()
	}

	// Truncate long lines
	var b strings.Builder
	for i, line := range lines {
		if len(line) > maxWidth {
			line = line[:maxWidth-3] + "..."
		}
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// isLikelyJSON checks if a string looks like JSON.
func isLikelyJSON(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

// isLikelyXML checks if a string looks like XML.
func isLikelyXML(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "<")
}

// detectLanguage tries to detect the language of a code snippet.
func detectLanguage(code string) string {
	if isLikelyJSON(code) {
		return "json"
	}
	if isLikelyXML(code) {
		return "xml"
	}
	// Check for common patterns
	if strings.Contains(code, "func ") && strings.Contains(code, "{") {
		return "go"
	}
	if strings.Contains(code, "import ") && strings.Contains(code, ";") {
		return "java"
	}
	if strings.Contains(code, "def ") || strings.Contains(code, "import ") {
		return "python"
	}
	if strings.Contains(code, "const ") || strings.Contains(code, "function") {
		return "javascript"
	}
	if strings.Contains(code, "<div") || strings.Contains(code, "</") {
		return "html"
	}
	return ""
}

package ui

import (
	"fmt"
	"strings"
)

// ToolType represents the category of a tool for rendering.
type ToolType int

const (
	ToolTypeDefault ToolType = iota
	ToolTypeBash
	ToolTypeFileRead
	ToolTypeFileWrite
	ToolTypeSearch
	ToolTypeWeb
	ToolTypeMemory
)

// classifyTool returns the ToolType based on the tool name.
func classifyTool(name string) ToolType {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "bash") || strings.Contains(lower, "exec") || strings.Contains(lower, "shell") || strings.Contains(lower, "command"):
		return ToolTypeBash
	case strings.Contains(lower, "web") || strings.Contains(lower, "fetch") || strings.Contains(lower, "http") || strings.Contains(lower, "curl") || strings.Contains(lower, "browse"):
		return ToolTypeWeb
	case strings.Contains(lower, "search") || strings.Contains(lower, "grep") || strings.Contains(lower, "find"):
		return ToolTypeSearch
	case strings.Contains(lower, "read") || strings.Contains(lower, "view") || strings.Contains(lower, "cat"):
		return ToolTypeFileRead
	case strings.Contains(lower, "write") || strings.Contains(lower, "edit") || strings.Contains(lower, "create_file") || strings.Contains(lower, "patch"):
		return ToolTypeFileWrite
	case strings.Contains(lower, "memory") || strings.Contains(lower, "remember") || strings.Contains(lower, "forget"):
		return ToolTypeMemory
	default:
		return ToolTypeDefault
	}
}

func toolCardKindForTool(name string) ToolCardKind {
	switch classifyTool(name) {
	case ToolTypeFileRead, ToolTypeFileWrite:
		return ToolCardFile
	case ToolTypeBash:
		return ToolCardBash
	case ToolTypeSearch:
		return ToolCardSearch
	default:
		return ToolCardGeneric
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
	if summary := ecosystemToolSummary(te.Name, te.RawArgs); summary != "" {
		return summary
	}
	switch tt {
	case ToolTypeBash:
		if cmd, ok := te.RawArgs["command"].(string); ok {
			return truncateDisplay(cmd, 60)
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
				return truncateDisplay(u, 60)
			}
		}
	case ToolTypeMemory:
		if k, ok := te.RawArgs["key"].(string); ok {
			return k
		}
	}
	return ""
}

// detectCodeBlocks checks if the result contains markdown code blocks.
func detectCodeBlocks(text string) bool {
	return strings.Contains(text, "```") || strings.Contains(text, "~~~")
}

// formatToolResult formats a tool result for display with smart truncation.
// It preserves code blocks and adds expand/collapse hints.
func formatToolResult(result string, maxLines int, maxWidth int) string {
	result = sanitizeTerminalMultiline(result)
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
			line = truncateDisplay(line, maxWidth)
			b.WriteString(line)
			b.WriteString("\n")
		}
		remaining := len(lines) - maxLines
		b.WriteString("... ")
		if hasCodeBlocks {
			b.WriteString("(code blocks truncated)")
		} else {
			fmt.Fprintf(&b, "%d", remaining)
			b.WriteString(" more lines")
		}
		return b.String()
	}

	// Truncate long lines
	var b strings.Builder
	for i, line := range lines {
		line = truncateDisplay(line, maxWidth)
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

package tui

import "strings"

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

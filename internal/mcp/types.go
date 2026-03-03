package mcp

import "github.com/abdulachik/local-agent/internal/llm"

// ServerInfo holds metadata about a connected MCP server.
type ServerInfo struct {
	Name      string
	ToolCount int
}

// ToolResult is the outcome of a tool call.
type ToolResult struct {
	Content string
	IsError bool
}

// ToLLMToolDef converts MCP tool schema to the LLM's tool definition format.
func ToLLMToolDef(name, description string, inputSchema any) llm.ToolDef {
	params, _ := inputSchema.(map[string]any)
	if params == nil {
		params = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return llm.ToolDef{
		Name:        name,
		Description: description,
		Parameters:  params,
	}
}

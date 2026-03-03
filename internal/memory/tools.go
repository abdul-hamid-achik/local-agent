package memory

import "github.com/abdulachik/local-agent/internal/llm"

// BuiltinToolDefs returns the tool definitions for memory_save and memory_recall.
func BuiltinToolDefs() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name:        "memory_save",
			Description: "Save an important fact, user preference, or piece of context to persistent memory. Use this proactively when the user shares information worth remembering across sessions.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": map[string]any{
						"type":        "string",
						"description": "The fact or information to remember.",
					},
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional tags for categorization (e.g., 'preference', 'project', 'name').",
					},
				},
				"required": []string{"content"},
			},
		},
		{
			Name:        "memory_recall",
			Description: "Search persistent memory for previously saved facts. Use this when you need to recall user preferences, project details, or other saved context.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query to find relevant memories.",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

// IsBuiltinTool returns true if the given tool name is a built-in memory tool.
func IsBuiltinTool(name string) bool {
	return name == "memory_save" || name == "memory_recall"
}

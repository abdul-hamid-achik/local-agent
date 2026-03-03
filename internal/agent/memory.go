package agent

import (
	"fmt"
	"strings"

	"github.com/abdulachik/local-agent/internal/llm"
	"github.com/abdulachik/local-agent/internal/memory"
)

// memoryBuiltinToolDefs returns memory tool definitions for merging with MCP tools.
func (a *Agent) memoryBuiltinToolDefs() []llm.ToolDef {
	return memory.BuiltinToolDefs()
}

// isMemoryTool checks if a tool name is a built-in memory tool.
func (a *Agent) isMemoryTool(name string) bool {
	return memory.IsBuiltinTool(name)
}

// handleMemoryTool dispatches a memory tool call and returns the result.
func (a *Agent) handleMemoryTool(tc llm.ToolCall) (string, bool) {
	switch tc.Name {
	case "memory_save":
		return a.handleMemorySave(tc.Arguments)
	case "memory_recall":
		return a.handleMemoryRecall(tc.Arguments)
	default:
		return fmt.Sprintf("unknown memory tool: %s", tc.Name), true
	}
}

func (a *Agent) handleMemorySave(args map[string]any) (string, bool) {
	content, _ := args["content"].(string)
	if content == "" {
		return "error: content is required", true
	}

	var tags []string
	if rawTags, ok := args["tags"]; ok {
		switch v := rawTags.(type) {
		case []any:
			for _, t := range v {
				if s, ok := t.(string); ok {
					tags = append(tags, s)
				}
			}
		case []string:
			tags = v
		}
	}

	id, err := a.memoryStore.Save(content, tags)
	if err != nil {
		return fmt.Sprintf("error saving memory: %v", err), true
	}

	return fmt.Sprintf("Memory saved (id: %d)", id), false
}

func (a *Agent) handleMemoryRecall(args map[string]any) (string, bool) {
	query, _ := args["query"].(string)
	if query == "" {
		return "error: query is required", true
	}

	memories := a.memoryStore.Recall(query, 5)
	if len(memories) == 0 {
		return "No matching memories found.", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d matching memories:\n", len(memories))
	for _, mem := range memories {
		fmt.Fprintf(&b, "- [%d] %s", mem.ID, mem.Content)
		if len(mem.Tags) > 0 {
			fmt.Fprintf(&b, " (tags: %s)", strings.Join(mem.Tags, ", "))
		}
		b.WriteString("\n")
	}

	return b.String(), false
}

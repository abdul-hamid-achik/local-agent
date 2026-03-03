package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/abdulachik/local-agent/internal/llm"
	"github.com/abdulachik/local-agent/internal/memory"
)

const systemTemplate = `You are a helpful personal assistant running locally on the user's machine.
You have access to tools via MCP servers. Use them when the user's request requires it.

Current date: %s
%s%s%s
## Available Tools
%s
## Guidelines
- Be concise and direct in your responses.
- When a tool call fails, explain what happened and suggest alternatives.
- For multi-step tasks, explain your plan briefly before executing.
- Format responses in markdown when it improves readability.
- If you're unsure about something, say so rather than guessing.
- Never fabricate tool results — always call the actual tool.
%s`

// buildSystemPrompt generates the system prompt with current tool info,
// active skills, loaded context, memory, and optional ICE context.
func buildSystemPrompt(tools []llm.ToolDef, skillContent, loadedContext string, memStore *memory.Store, iceContext string) string {
	var toolList strings.Builder
	if len(tools) == 0 {
		toolList.WriteString("No tools currently available.\n")
	} else {
		for _, t := range tools {
			fmt.Fprintf(&toolList, "- **%s**: %s\n", t.Name, t.Description)
		}
	}

	var skillSection string
	if skillContent != "" {
		skillSection = fmt.Sprintf("\n## Active Skills\n%s\n", skillContent)
	}

	var ctxSection string
	if loadedContext != "" {
		ctxSection = fmt.Sprintf("\n## Loaded Context\n%s\n", loadedContext)
	}

	var memorySection string
	if iceContext != "" {
		// ICE provides its own assembled context (past conversations + memories).
		memorySection = iceContext
	} else if memStore != nil {
		memorySection = buildMemorySection(memStore)
	}

	var memoryGuidelines string
	if memStore != nil {
		memoryGuidelines = `
## Memory Guidelines
- You have access to persistent memory via memory_save and memory_recall tools.
- Proactively save important user preferences, project facts, and key decisions.
- When the user shares personal information (name, preferences, etc.), save it.
- Use memory_recall to look up previously saved information when relevant.
- Don't save trivial or session-specific information.
`
	}

	return fmt.Sprintf(systemTemplate,
		time.Now().Format("Monday, January 2, 2006"),
		skillSection,
		ctxSection,
		memorySection,
		toolList.String(),
		memoryGuidelines,
	)
}

// buildMemorySection creates the remembered facts section from recent memories.
func buildMemorySection(store *memory.Store) string {
	if store.Count() == 0 {
		return ""
	}

	recent := store.Recent(10)
	if len(recent) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## Remembered Facts\n")
	for _, mem := range recent {
		b.WriteString(fmt.Sprintf("- %s", mem.Content))
		if len(mem.Tags) > 0 {
			b.WriteString(fmt.Sprintf(" [tags: %s]", strings.Join(mem.Tags, ", ")))
		}
		b.WriteString("\n")
	}

	return b.String()
}

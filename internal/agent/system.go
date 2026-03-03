package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
)

const systemTemplate = `You are a helpful personal assistant running locally on the user's machine.
You have access to tools via MCP servers. You MUST use tools to accomplish tasks — do not guess or make up answers when a tool can provide the real information.
%s
Current date: %s
%s%s
%s%s%s
## Available Tools
%s
## Guidelines
- **ALWAYS use your tools** when the user asks you to read, explore, search, or modify files. You have filesystem tools — use them.
- When the user says "read this codebase" or similar, use list/read tools starting from the working directory shown above.
- Be concise and direct in your responses.
- When a tool call fails, explain what happened and suggest alternatives.
- For multi-step tasks, explain your plan briefly before executing.
- Format responses in markdown when it improves readability.
- If you're unsure about something, say so rather than guessing.
- Never fabricate tool results — always call the actual tool.
- Do NOT claim you cannot access files or the filesystem. You have tools for that — use them.
%s`

// buildSystemPrompt generates the system prompt with current tool info,
// active skills, loaded context, memory, optional ICE context, and ignore patterns.
func buildSystemPrompt(modePrefix string, tools []llm.ToolDef, skillContent, loadedContext string, memStore *memory.Store, iceContext, workDir, ignoreContent string) string {
	var toolList strings.Builder
	if len(tools) == 0 {
		toolList.WriteString("No tools currently available.\n")
	} else {
		for _, t := range tools {
			fmt.Fprintf(&toolList, "- **%s**: %s\n", t.Name, t.Description)
		}
	}

	envSection := buildEnvironmentSection(workDir)

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

	var ignoreSection string
	if ignoreContent != "" {
		ignoreSection = fmt.Sprintf("\n## Ignored Paths\nThe following paths/patterns should be excluded from file operations:\n%s\n", ignoreContent)
	}

	var modePrefixSection string
	if modePrefix != "" {
		modePrefixSection = "\n" + modePrefix + "\n"
	}

	return fmt.Sprintf(systemTemplate,
		modePrefixSection,
		time.Now().Format("Monday, January 2, 2006"),
		envSection,
		ignoreSection,
		skillSection,
		ctxSection,
		memorySection,
		toolList.String(),
		memoryGuidelines,
	)
}

// buildEnvironmentSection creates the environment context section.
func buildEnvironmentSection(workDir string) string {
	if workDir == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## Environment\n")
	b.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))

	// Auto-detect project type from marker files.
	if info := detectProjectInfo(workDir); info != "" {
		b.WriteString(info)
	}

	return b.String()
}

// detectProjectInfo looks for common project marker files and returns a brief description.
func detectProjectInfo(workDir string) string {
	markers := []struct {
		file string
		desc string
	}{
		{"go.mod", "Go module"},
		{"package.json", "Node.js/JavaScript"},
		{"Cargo.toml", "Rust"},
		{"pyproject.toml", "Python"},
		{"setup.py", "Python"},
		{"Makefile", ""},
		{"Taskfile.yml", ""},
	}

	var found []string
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(workDir, m.file)); err == nil {
			if m.desc != "" {
				found = append(found, fmt.Sprintf("%s (%s)", m.file, m.desc))
			} else {
				found = append(found, m.file)
			}
		}
	}

	if len(found) == 0 {
		return ""
	}

	return fmt.Sprintf("Project markers: %s\n", strings.Join(found, ", "))
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

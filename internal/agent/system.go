package agent

import (
	"fmt"
	"os"
	"os/exec"
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

// smallModelTemplate is a more concise template for small models (0.8B, 2B).
const smallModelTemplate = `You are a local AI assistant. Use tools to read/write files and run commands.
%sDate: %s
%s%s
%s
## Tools
%s
Guidelines:
- Be concise and direct
- Use tools when needed to complete tasks
- If a tool fails, continue with available information
- Don't guess - use tools to verify
- You can complete tasks even if some tools fail
%s`

// isSmallModel returns true if the model name indicates a small model (<=2B parameters).
func isSmallModel(modelName string) bool {
	lower := strings.ToLower(modelName)
	// Check for common small model patterns
	if strings.Contains(lower, "0.8b") || strings.Contains(lower, "1b") || strings.Contains(lower, "2b") {
		return true
	}
	return false
}

// buildSystemPrompt generates the system prompt with current tool info,
// active skills, loaded context, memory, optional ICE context, and ignore patterns.
// It optimizes for small models if isSmallModel is true.
func buildSystemPrompt(modePrefix string, tools []llm.ToolDef, skillContent, loadedContext string, memStore *memory.Store, iceContext, workDir, ignoreContent string) string {
	return buildSystemPromptForModel(modePrefix, tools, skillContent, loadedContext, memStore, iceContext, workDir, ignoreContent, "")
}

// buildSystemPromptForModel generates the system prompt, optionally optimized for the given model name.
func buildSystemPromptForModel(modePrefix string, tools []llm.ToolDef, skillContent, loadedContext string, memStore *memory.Store, iceContext, workDir, ignoreContent string, modelName string) string {
	useSmallModel := isSmallModel(modelName)

	var toolList string
	if len(tools) == 0 {
		toolList = "No tools currently available.\n"
	} else if useSmallModel {
		// Simplified tool list for small models
		toolList = simplifyToolsForSmallModel(tools)
	} else {
		// Full tool list for larger models
		var b strings.Builder
		for _, t := range tools {
			fmt.Fprintf(&b, "- **%s**: %s\n", t.Name, t.Description)
		}
		toolList = b.String()
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

	dateStr := time.Now().Format("Monday, January 2, 2006")

	if useSmallModel {
		return fmt.Sprintf(smallModelTemplate,
			modePrefixSection,
			dateStr,
			envSection,
			ignoreSection,
			skillSection,
			toolList,
			memoryGuidelines,
		)
	}

	return fmt.Sprintf(systemTemplate,
		modePrefixSection,
		dateStr,
		envSection,
		ignoreSection,
		skillSection,
		ctxSection,
		memorySection,
		toolList,
		memoryGuidelines,
	)
}

// simplifyToolsForSmallModel creates a condensed tool list for small models.
func simplifyToolsForSmallModel(tools []llm.ToolDef) string {
	var b strings.Builder
	for _, t := range tools {
		// Truncate long descriptions
		desc := t.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		fmt.Fprintf(&b, "- %s: %s\n", t.Name, desc)
	}
	return b.String()
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

	// Git context
	if gitInfo := detectGitInfo(workDir); gitInfo != "" {
		b.WriteString(gitInfo)
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

// detectGitInfo returns git branch and status information for the working directory.
func detectGitInfo(workDir string) string {
	// Check if this is a git repo
	gitDir := filepath.Join(workDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return ""
	}

	var b strings.Builder

	// Get current branch
	branch := runGitCommand(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "" {
		b.WriteString(fmt.Sprintf("Git branch: %s\n", branch))
	}

	// Get status (short format)
	status := runGitCommand(workDir, "status", "--porcelain")
	if status != "" {
		lines := strings.Split(strings.TrimSpace(status), "\n")
		var modified, added, deleted int
		for _, line := range lines {
			if len(line) >= 2 {
				switch line[0] {
				case 'M', 'm':
					modified++
				case 'A':
					added++
				case 'D':
					deleted++
				}
			}
		}
		if modified > 0 || added > 0 || deleted > 0 {
			statusParts := []string{}
			if modified > 0 {
				statusParts = append(statusParts, fmt.Sprintf("%d modified", modified))
			}
			if added > 0 {
				statusParts = append(statusParts, fmt.Sprintf("%d added", added))
			}
			if deleted > 0 {
				statusParts = append(statusParts, fmt.Sprintf("%d deleted", deleted))
			}
			b.WriteString(fmt.Sprintf("Git status: %s\n", strings.Join(statusParts, ", ")))
		}
	}

	// Get recent commits (last 3)
	recentLog := runGitCommand(workDir, "log", "-3", "--oneline")
	if recentLog != "" {
		b.WriteString(fmt.Sprintf("Recent commits:\n"))
		for _, line := range strings.Split(strings.TrimSpace(recentLog), "\n") {
			b.WriteString(fmt.Sprintf("  - %s\n", line))
		}
	}

	if b.Len() == 0 {
		return ""
	}

	return b.String()
}

// runGitCommand runs a git command and returns the output (trimmed).
func runGitCommand(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
)

type Completion struct {
	Label       string
	Insert      string
	Category    string
	Description string
	Index       int
}

type Completer struct {
	commands       []*command.Command
	models         []string
	skills         []string
	agents         []string
	workDir        string
	registry       *mcp.Registry
	ignorePatterns *config.IgnorePatterns
}

func NewCompleter(cmdReg *command.Registry, models, skills, agents []string, registry *mcp.Registry) *Completer {
	workDir, _ := os.Getwd()
	return &Completer{
		commands: cmdReg.All(),
		models:   models,
		skills:   skills,
		agents:   agents,
		workDir:  workDir,
		registry: registry,
	}
}

func (c *Completer) Complete(input string) []Completion {
	var completions []Completion

	if strings.HasPrefix(input, "/") {
		completions = c.completeCommand(input)
	} else if strings.HasPrefix(input, "@") {
		completions = c.completeAgentOrFile(input)
	} else if strings.HasPrefix(input, "#") {
		completions = c.completeSkill(input)
	}

	return completions
}

func (c *Completer) completeCommand(input string) []Completion {
	var completions []Completion
	input = strings.TrimPrefix(input, "/")

	for _, cmd := range c.commands {
		if strings.HasPrefix(cmd.Name, input) {
			comp := Completion{
				Label:    "/" + cmd.Name,
				Insert:   "/" + cmd.Name + " ",
				Category: "command",
			}
			if cmd.Usage != "" {
				parts := strings.Fields(cmd.Usage)
				if len(parts) > 1 {
					comp.Label = "/" + cmd.Name + " " + parts[1]
				}
			}
			completions = append(completions, comp)
		}

		for _, alias := range cmd.Aliases {
			if strings.HasPrefix(alias, input) {
				completions = append(completions, Completion{
					Label:    "/" + alias,
					Insert:   "/" + alias + " ",
					Category: "command",
				})
			}
		}
	}

	return completions
}

func (c *Completer) completeAgentOrFile(input string) []Completion {
	var completions []Completion
	input = strings.TrimPrefix(input, "@")

	// Always show agents first
	for _, agent := range c.agents {
		if strings.HasPrefix(agent, input) {
			completions = append(completions, Completion{
				Label:    "@" + agent,
				Insert:   "@" + agent + " ",
				Category: "agent",
			})
		}
	}

	// Always append file results (not just when no agents match)
	completions = append(completions, c.completeFile(input)...)

	return completions
}

func (c *Completer) completeFile(input string) []Completion {
	var completions []Completion

	// Determine the directory to list
	dir := c.workDir
	if strings.Contains(input, "/") {
		// User is typing a path
		lastSlash := strings.LastIndex(input, "/")
		dirPart := input[:lastSlash]
		if !strings.HasPrefix(dirPart, "/") {
			dirPart = filepath.Join(c.workDir, dirPart)
		}
		if info, err := os.Stat(dirPart); err == nil && info.IsDir() {
			dir = dirPart
		}
	}

	// Read directory entries
	entries, err := os.ReadDir(dir)
	if err != nil {
		return completions
	}

	prefix := input
	if strings.Contains(input, "/") {
		prefix = input[strings.LastIndex(input, "/")+1:]
	}

	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden files unless user explicitly types .
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		// Skip entries matching ignore patterns.
		if c.ignorePatterns.Match(name) {
			continue
		}

		if strings.HasPrefix(name, prefix) {
			isDir := entry.IsDir()
			displayName := name
			insertName := name

			if isDir {
				displayName += "/"
				insertName += "/"
			}

			// Build full path relative to input
			if strings.Contains(input, "/") {
				dirPath := input[:strings.LastIndex(input, "/")+1]
				displayName = dirPath + displayName
				insertName = dirPath + insertName
			} else if dir != c.workDir {
				relPath, _ := filepath.Rel(c.workDir, dir)
				if relPath != "." {
					displayName = relPath + "/" + name
					if isDir {
						displayName += "/"
					} else {
						insertName = relPath + "/" + insertName
					}
				}
			}

			category := "file"
			if isDir {
				category = "folder"
			}

			completions = append(completions, Completion{
				Label:    "@" + displayName,
				Insert:   "@" + insertName + " ",
				Category: category,
			})
		}
	}

	return completions
}

// CompleteFilePath lists directory contents at a given relative path.
// Used for folder drill-down in the completion modal.
func (c *Completer) CompleteFilePath(relPath string) []Completion {
	var completions []Completion

	dir := filepath.Join(c.workDir, relPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return completions
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Skip entries matching ignore patterns.
		if c.ignorePatterns.Match(name) {
			continue
		}

		isDir := entry.IsDir()
		displayName := name
		insertPath := relPath
		if insertPath != "" && !strings.HasSuffix(insertPath, "/") {
			insertPath += "/"
		}
		insertPath += name

		if isDir {
			displayName += "/"
		}

		category := "file"
		if isDir {
			category = "folder"
		}

		completions = append(completions, Completion{
			Label:    displayName,
			Insert:   "@" + insertPath + " ",
			Category: category,
		})
	}

	return completions
}

func (c *Completer) completeSkill(input string) []Completion {
	var completions []Completion
	input = strings.TrimPrefix(input, "#")

	for _, skill := range c.skills {
		if strings.HasPrefix(skill, input) {
			completions = append(completions, Completion{
				Label:    "#" + skill,
				Insert:   "#" + skill + " ",
				Category: "skill",
			})
		}
	}

	return completions
}

// FilterCompletions filters completions by case-insensitive substring match on Label.
func FilterCompletions(items []Completion, query string) []Completion {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	var filtered []Completion
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Label), q) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// SearchFiles performs an async vecgrep search via the MCP registry.
func (c *Completer) SearchFiles(ctx context.Context, query string) []Completion {
	if c.registry == nil || query == "" {
		return nil
	}

	result, err := c.registry.CallTool(ctx, "vecgrep_search", map[string]any{
		"query": query,
		"limit": 10,
	})
	if err != nil {
		return nil
	}

	var results []Completion
	// Parse the result content as JSON array of file paths or objects
	var searchResults []struct {
		Path  string  `json:"path"`
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(result.Content), &searchResults); err != nil {
		// Try as simple string lines
		for _, line := range strings.Split(result.Content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			results = append(results, Completion{
				Label:       "@" + line,
				Insert:      "@" + line + " ",
				Category:    "search_result",
				Description: "vecgrep match",
			})
		}
		return results
	}

	for _, sr := range searchResults {
		results = append(results, Completion{
			Label:       "@" + sr.Path,
			Insert:      "@" + sr.Path + " ",
			Category:    "search_result",
			Description: "vecgrep match",
		})
	}
	return results
}

func (c *Completer) UpdateModels(models []string) {
	c.models = models
}

func (c *Completer) UpdateSkills(skills []string) {
	c.skills = skills
}

func (c *Completer) UpdateAgents(agents []string) {
	c.agents = agents
}

// SetIgnorePatterns sets the ignore patterns used to filter file completions.
func (c *Completer) SetIgnorePatterns(patterns *config.IgnorePatterns) {
	c.ignorePatterns = patterns
}

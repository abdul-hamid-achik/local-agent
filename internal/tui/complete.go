package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/abdulachik/local-agent/internal/command"
)

type Completion struct {
	Label    string
	Insert   string
	Category string
	Index    int
}

type Completer struct {
	commands []*command.Command
	models   []string
	skills   []string
	agents   []string
	workDir  string
}

func NewCompleter(cmdReg *command.Registry, models, skills, agents []string) *Completer {
	workDir, _ := os.Getwd()
	return &Completer{
		commands: cmdReg.All(),
		models:   models,
		skills:   skills,
		agents:   agents,
		workDir:  workDir,
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

	// First try to match agents
	for _, agent := range c.agents {
		if strings.HasPrefix(agent, input) {
			completions = append(completions, Completion{
				Label:    "@" + agent,
				Insert:   "@" + agent + " ",
				Category: "agent",
			})
		}
	}

	// If no agent matches, try to match files/folders
	if len(completions) == 0 {
		completions = c.completeFile(input)
	}

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

func (c *Completer) UpdateModels(models []string) {
	c.models = models
}

func (c *Completer) UpdateSkills(skills []string) {
	c.skills = skills
}

func (c *Completer) UpdateAgents(agents []string) {
	c.agents = agents
}

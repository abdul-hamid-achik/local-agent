package command

import (
	"os"
	"path/filepath"
	"strings"
)

// CustomCommand represents a user-defined command loaded from a markdown file.
type CustomCommand struct {
	Name        string
	Description string
	Template    string // prompt template with {{input}} placeholder
}

// LoadCustomCommands reads .md files from the commands directory and returns
// parsed custom commands. Each file should have YAML-like frontmatter:
//
//	---
//	name: review
//	description: Code review prompt
//	---
//	Review this code: {{input}}
func LoadCustomCommands(dir string) []CustomCommand {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var cmds []CustomCommand
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if cmd, ok := parseCustomCommand(string(data)); ok {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// parseCustomCommand parses a markdown file with YAML frontmatter.
func parseCustomCommand(content string) (CustomCommand, bool) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return CustomCommand{}, false
	}

	// Find end of frontmatter.
	rest := content[3:]
	idx := strings.Index(rest, "---")
	if idx < 0 {
		return CustomCommand{}, false
	}

	frontmatter := rest[:idx]
	body := strings.TrimSpace(rest[idx+3:])

	cmd := CustomCommand{Template: body}

	// Parse simple key: value pairs from frontmatter.
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "name":
			cmd.Name = val
		case "description":
			cmd.Description = val
		}
	}

	if cmd.Name == "" || cmd.Template == "" {
		return CustomCommand{}, false
	}

	return cmd, true
}

// RegisterCustomCommands loads and registers custom commands from the given directory.
func RegisterCustomCommands(r *Registry, dir string) {
	cmds := LoadCustomCommands(dir)
	for _, cc := range cmds {
		// Capture for closure.
		tmpl := cc.Template
		desc := cc.Description
		if desc == "" {
			desc = "Custom command"
		}

		r.Register(&Command{
			Name:        cc.Name,
			Description: desc,
			Handler: func(_ *Context, args []string) Result {
				input := strings.Join(args, " ")
				prompt := strings.ReplaceAll(tmpl, "{{input}}", input)
				return Result{
					Action: ActionSendPrompt,
					Data:   prompt,
				}
			},
		})
	}
}

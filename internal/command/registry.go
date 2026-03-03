package command

import (
	"fmt"
	"sort"
	"strings"
)

// Command represents a slash command.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	Usage       string
	Handler     func(ctx *Context, args []string) Result
}

// Context provides commands with read access to application state.
type Context struct {
	Model            string
	ModelList        []string
	AgentProfile     string
	AgentList        []string
	ToolCount        int
	ServerCount      int
	ServerNames      []string
	Skills           []SkillInfo
	LoadedFile       string
	ICEEnabled       bool
	ICEConversations int
	ICESessionID     string
}

// SkillInfo is a read-only view of a skill for command display.
type SkillInfo struct {
	Name        string
	Description string
	Active      bool
}

// Result is returned by command handlers to describe what to do.
type Result struct {
	Text   string // Display text (shown as system message)
	Action Action // Side effect for the TUI to execute
	Data   string // Optional payload (e.g. file path, model name)
	Error  string // Error text (takes priority over Text)
}

// Action describes a side effect the TUI should perform.
type Action int

const (
	ActionNone            Action = iota
	ActionShowHelp               // Show help overlay
	ActionClear                  // Clear conversation history
	ActionQuit                   // Exit the application
	ActionLoadContext            // Load markdown context (Data = path)
	ActionUnloadContext          // Remove loaded context
	ActionActivateSkill          // Activate skill (Data = name)
	ActionDeactivateSkill        // Deactivate skill (Data = name)
	ActionSwitchModel            // Switch model (Data = model name)
	ActionSwitchAgent            // Switch agent profile (Data = agent name)
)

// Registry holds all registered slash commands.
type Registry struct {
	commands map[string]*Command // name/alias → command
	all      []*Command          // ordered list
}

// NewRegistry creates an empty command registry.
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]*Command),
	}
}

// Register adds a command to the registry.
func (r *Registry) Register(cmd *Command) {
	r.all = append(r.all, cmd)
	r.commands[cmd.Name] = cmd
	for _, alias := range cmd.Aliases {
		r.commands[alias] = cmd
	}
}

// Execute dispatches a slash command by name and returns the result.
func (r *Registry) Execute(ctx *Context, name string, args []string) Result {
	cmd, ok := r.commands[name]
	if !ok {
		return Result{Error: fmt.Sprintf("unknown command: /%s — type /help for available commands", name)}
	}
	return cmd.Handler(ctx, args)
}

// All returns all registered commands in registration order.
func (r *Registry) All() []*Command {
	return r.all
}

// Match returns commands whose name starts with the given prefix.
func (r *Registry) Match(prefix string) []*Command {
	var matches []*Command
	seen := make(map[string]bool)
	for _, cmd := range r.all {
		if strings.HasPrefix(cmd.Name, prefix) && !seen[cmd.Name] {
			matches = append(matches, cmd)
			seen[cmd.Name] = true
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})
	return matches
}

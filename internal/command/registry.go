package command

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Command represents a slash command.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	Usage       string
	// Hidden commands remain executable for compatibility and maintenance but
	// stay out of everyday help, completion, and discovery surfaces.
	Hidden  bool
	Handler func(ctx *Context, args []string) Result
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
	Servers          []ServerInfo
	MCPToolCount     int
	ReadRoots        []string
	ReadGrants       []ReadGrantInfo
	Skills           []SkillInfo
	LoadedFile       string
	ICEEnabled       bool
	ICEConversations int
	ICESessionID     string
	// Token stats
	SessionEvalTotal   int
	SessionPromptTotal int
	// LatestPromptTokens is the provider-reported prompt size for the most
	// recent request. Unlike SessionPromptTotal, it is an occupancy snapshot
	// that can be compared with the active model's context window.
	LatestPromptTokens int
	SessionTurnCount   int
	NumCtx             int
	CurrentModel       string
	// Artifacts is a bounded, parser-safe view of durable artifacts projected
	// from completed tool receipts in transcript order. It deliberately omits
	// provider prose, source paths, raw manifests, and MCP structured content.
	Artifacts          []ArtifactInfo
	ArtifactsTruncated bool
	// File changes
	FileChanges map[string]int // path → modification count
	// Goal runtime summary. Commands receive only the bounded fields needed to
	// choose an action; the UI remains the authority for transitions.
	GoalConfigured       bool
	GoalObjective        string
	GoalStatus           string
	GoalPending          bool
	GoalBlocker          string
	GoalExhausted        bool
	GoalPersistenceDirty bool
	GoalBusy             bool
}

// MaxContextArtifacts bounds the number of artifact summaries exposed to one
// slash-command invocation, even when a session contains more tool receipts.
const MaxContextArtifacts = 64

// ArtifactInfo is the read-only command view of a normalized artifact digest.
// URI and CreatedAt are host-derived/canonicalized before this value reaches
// the command package; ContentSHA256 is the full lowercase SHA-256 digest.
type ArtifactInfo struct {
	URI            string
	FileCount      int64
	TotalBytes     int64
	CreatedAt      string
	ContentSHA256  string
	SecretsWarning bool
	IndexingFailed bool
}

// SkillInfo is a read-only view of a skill for command display.
type SkillInfo struct {
	Name        string
	Description string
	Active      bool
}

// ServerInfo is the bounded, read-only MCP connection projection exposed to
// slash commands. It intentionally excludes transport error strings so command
// output can be persisted without retaining raw server failures.
type ServerInfo struct {
	Name      string
	Connected bool
	ToolCount int
}

// ReadGrantInfo is the typed temporary filesystem authority shown by /scope.
type ReadGrantInfo struct {
	Path string
	Kind string
}

// Result is returned by command handlers to describe what to do.
type Result struct {
	Text   string // Display text (shown as system message)
	Action Action // Side effect for the TUI to execute
	Data   string // Optional payload (e.g. file path, model name)
	Error  string // Error text (takes priority over Text)
	Force  bool   // Explicit confirmation for commands that replace existing data
	Goal   *GoalRequest
}

// GoalRequest is the typed creation intent carried from slash-command parsing
// to the TUI. Prompt and budget stay separate so the UI never reparses text.
type GoalRequest struct {
	Prompt       string
	TimeBudget   time.Duration
	TimeExplicit bool
}

// Action describes a side effect the TUI should perform.
type Action int

const (
	ActionNone              Action = iota
	ActionShowHelp                 // Show help overlay
	ActionClear                    // Clear conversation history
	ActionQuit                     // Exit the application
	ActionLoadContext              // Load markdown context (Data = path)
	ActionUnloadContext            // Remove loaded context
	ActionActivateSkill            // Activate skill (Data = name)
	ActionDeactivateSkill          // Deactivate skill (Data = name)
	ActionSwitchModel              // Switch model (Data = model name)
	ActionEnableAutoModel          // Resume availability-aware automatic routing
	ActionSwitchAgent              // Switch agent profile (Data = agent name)
	ActionShowSessions             // Open sessions picker
	ActionShowModelPicker          // Open model picker overlay
	ActionCommit                   // Generate commit message and commit
	ActionSendPrompt               // Send Data as a message to the agent
	ActionExport                   // Export conversation (Data = path)
	ActionImport                   // Import conversation (Data = path)
	ActionCheckpoint               // Save a conversation checkpoint (Data = optional label)
	ActionListCheckpoints          // List saved checkpoints
	ActionRestoreCheckpoint        // Restore a checkpoint (Data = id)
	ActionOpenGoal                 // Open the goal form (Data = optional objective)
	ActionShowGoal                 // Show the active goal summary
	ActionPauseGoal                // Pause automatic goal continuation
	ActionResumeGoal               // Resume/retry the active goal
	ActionDropGoal                 // Drop the active goal without claiming completion
	ActionEditGoalBudget           // Open the active goal's budget-only editor
	ActionRecoverExecution         // Review typed evidence for a paused ordinary execution
	ActionAddReadRoot              // Grant one temporary external read-only directory (Data = path)
	ActionRemoveReadRoot           // Revoke one temporary external read-only grant (Data = path)
	ActionClearReadRoots           // Revoke every temporary external read-only grant
)

// Registry holds all registered slash commands.
type Registry struct {
	commands    map[string]*Command // name/alias → command
	all         []*Command          // ordered list
	actions     map[ActionID]ActionSpec
	actionOrder []ActionID
}

// NewRegistry creates an empty command registry.
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]*Command),
		actions:  make(map[ActionID]ActionSpec),
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
	if ctx == nil {
		ctx = &Context{}
	}
	return cmd.Handler(ctx, args)
}

// All returns all registered commands in registration order.
func (r *Registry) All() []*Command {
	visible := make([]*Command, 0, len(r.all))
	for _, cmd := range r.all {
		if !cmd.Hidden {
			visible = append(visible, cmd)
		}
	}
	return visible
}

// Match returns commands whose name starts with the given prefix.
func (r *Registry) Match(prefix string) []*Command {
	var matches []*Command
	seen := make(map[string]bool)
	for _, cmd := range r.all {
		if cmd.Hidden {
			continue
		}
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

package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Message types for the BubbleTea update loop.

// StreamTextMsg delivers incremental text from the LLM.
type StreamTextMsg struct {
	Text string
}

// StreamDoneMsg signals the LLM has finished responding.
type StreamDoneMsg struct {
	EvalCount    int
	PromptTokens int
}

// ToolCallStartMsg signals a tool invocation has begun.
type ToolCallStartMsg struct {
	Name      string
	Args      map[string]any
	StartTime time.Time
}

// ToolCallResultMsg delivers the result of a tool call.
type ToolCallResultMsg struct {
	Name     string
	Result   string
	IsError  bool
	Duration time.Duration
}

// ErrorMsg reports an error.
type ErrorMsg struct {
	Msg string
}

// SystemMessageMsg displays a system-level message.
type SystemMessageMsg struct {
	Msg string
}

// AgentDoneMsg signals the agent loop has completed.
type AgentDoneMsg struct{}

// FailedServer records an MCP server that failed to connect.
type FailedServer struct {
	Name   string
	Reason string
}

// InitCompleteMsg signals startup is done.
type InitCompleteMsg struct {
	Model            string
	ModelList        []string
	AgentProfile     string
	AgentList        []string
	ToolCount        int
	ServerCount      int
	NumCtx           int
	FailedServers    []FailedServer
	ICEEnabled       bool
	ICEConversations int
	ICESessionID     string
}

// CommandResultMsg carries the result of a slash command for display.
type CommandResultMsg struct {
	Text string
}

// StartupStatusMsg reports progress of a single startup task (Ollama, MCP server, ICE).
type StartupStatusMsg struct {
	ID     string // unique key: "ollama", "mcp:<name>", "ice"
	Label  string // display name: "Ollama (qwen3:8b)", "docker-gateway"
	Status string // "connecting", "connected", "failed"
	Detail string // e.g. "110 tools", error message
}

// CompletionSearchResultMsg delivers async vecgrep search results.
type CompletionSearchResultMsg struct {
	Tag     int
	Results []Completion
}

// CompletionDebounceTickMsg fires after the debounce interval to trigger a search.
type CompletionDebounceTickMsg struct {
	Tag   int
	Query string
}

// PlanFormCompletedMsg signals the plan form has been submitted with a structured prompt.
type PlanFormCompletedMsg struct {
	Prompt string
}

// DoneFlashExpiredMsg clears the "done" terminal title after a timeout.
type DoneFlashExpiredMsg struct{}

// SessionCreatedMsg signals a session note was created via noted.
type SessionCreatedMsg struct {
	NoteID int
	Err    error
}

// SessionListMsg delivers the list of saved sessions from noted.
type SessionListMsg struct {
	Sessions []SessionListItem
	Err      error
}

// SessionLoadedMsg delivers a loaded session's entries from noted.
type SessionLoadedMsg struct {
	Entries []ChatEntry
	Title   string
	Err     error
}

// sendMsg is a helper to send a tea.Msg to the program.
func sendMsg(p *tea.Program, msg tea.Msg) {
	if p != nil {
		p.Send(msg)
	}
}

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

// MCPStatusMsg carries MCP connection results for display in the TUI.
type MCPStatusMsg struct {
	Connected     int
	ToolCount     int
	FailedServers []FailedServer
}

// sendMsg is a helper to send a tea.Msg to the program.
func sendMsg(p *tea.Program, msg tea.Msg) {
	if p != nil {
		p.Send(msg)
	}
}

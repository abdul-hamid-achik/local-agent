package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Adapter bridges the agent.Output interface to BubbleTea messages.
type Adapter struct {
	program *tea.Program
}

// NewAdapter creates an Adapter that sends messages to the given program.
func NewAdapter(p *tea.Program) *Adapter {
	return &Adapter{program: p}
}

func (a *Adapter) StreamText(text string) {
	sendMsg(a.program, StreamTextMsg{Text: text})
}

func (a *Adapter) StreamDone(evalCount, promptTokens int) {
	sendMsg(a.program, StreamDoneMsg{EvalCount: evalCount, PromptTokens: promptTokens})
}

func (a *Adapter) ToolCallStart(name string, args map[string]any) {
	sendMsg(a.program, ToolCallStartMsg{Name: name, Args: args, StartTime: time.Now()})
}

func (a *Adapter) ToolCallResult(name string, result string, isError bool, duration time.Duration) {
	sendMsg(a.program, ToolCallResultMsg{Name: name, Result: result, IsError: isError, Duration: duration})
}

func (a *Adapter) SystemMessage(msg string) {
	sendMsg(a.program, SystemMessageMsg{Msg: msg})
}

func (a *Adapter) Error(msg string) {
	// Log error for debugging
	if len(msg) > 100 {
		msg = msg[:97] + "..."
	}
	sendMsg(a.program, ErrorMsg{Msg: msg})
}

// Done sends the final completion message.
func (a *Adapter) Done() {
	sendMsg(a.program, AgentDoneMsg{})
}

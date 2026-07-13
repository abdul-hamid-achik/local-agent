package ui

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

func (a *Adapter) StreamReasoning(text string) {
	sendMsg(a.program, StreamThinkingMsg{Text: text})
}

func (a *Adapter) StreamDone(evalCount, promptTokens int) {
	sendMsg(a.program, StreamDoneMsg{EvalCount: evalCount, PromptTokens: promptTokens})
}

func (a *Adapter) ToolCallStart(callID, name string, args map[string]any) {
	sendMsg(a.program, ToolCallStartMsg{ID: callID, Name: name, Args: args, StartTime: time.Now()})
}

func (a *Adapter) ToolCallResult(callID, name string, result string, isError bool, duration time.Duration) {
	sendMsg(a.program, ToolCallResultMsg{ID: callID, Name: name, Result: result, IsError: isError, Duration: duration})
}

func (a *Adapter) SystemMessage(msg string) {
	sendMsg(a.program, SystemMessageMsg{Msg: msg})
}

func (a *Adapter) ContextCompacted() {
	sendMsg(a.program, ContextCompactedMsg{})
}

func (a *Adapter) Error(msg string) {
	// Log error for debugging
	if len(msg) > 100 {
		msg = msg[:97] + "..."
	}
	sendMsg(a.program, ErrorMsg{Msg: msg})
}

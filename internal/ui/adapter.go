package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
)

// Adapter bridges the agent.Output interface to BubbleTea messages.
type Adapter struct {
	program *tea.Program
	workDir string
}

var _ agent.BobWorkspaceContextOutput = (*Adapter)(nil)
var _ agent.ExpertProgressOutput = (*Adapter)(nil)

// NewAdapter creates an Adapter that sends messages to the given program.
func NewAdapter(p *tea.Program, workDir ...string) *Adapter {
	dir := ""
	if len(workDir) > 0 {
		dir = workDir[0]
	}
	return &Adapter{program: p, workDir: dir}
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
	sendMsg(a.program, newToolCallStartMsg(callID, name, args, a.workDir))
}

// newToolCallStartMsg captures an optional pre-write snapshot on the agent's
// background execution path, before ToolCallStart returns and the backend can
// mutate the file. Bubble Tea Update receives immutable snapshot data and
// never performs filesystem I/O.
func newToolCallStartMsg(callID, name string, args map[string]any, workDir string) ToolCallStartMsg {
	msg := ToolCallStartMsg{ID: callID, Name: name, Args: args, StartTime: time.Now()}
	if classifyTool(name) == ToolTypeFileWrite {
		snapshot := readDiffSnapshotForArgsAt(args, workDir)
		msg.BeforeContent = snapshot.Content
		msg.BeforeSnapshotAvailable = snapshot.Available
	}
	return msg
}

func (a *Adapter) ToolCallResult(callID, name string, result string, isError bool, duration time.Duration) {
	sendMsg(a.program, ToolCallResultMsg{ID: callID, Name: name, Result: result, IsError: isError, Duration: duration})
}

// ExpertProgress forwards only the host-owned bounded scheduler event. The
// expert runtime deliberately keeps objectives, reports, reasoning, and raw
// provider errors on its transient side of this boundary.
func (a *Adapter) ExpertProgress(callID string, event expertteam.ProgressEvent) {
	sendMsg(a.program, ExpertProgressMsg{CallID: callID, Event: event})
}

// ToolCallSemanticResult carries only the bounded host projection into the UI;
// raw StructuredContent remains inside the agent parser boundary.
func (a *Adapter) ToolCallSemanticResult(callID, name string, result string, isError bool, duration time.Duration, projection ecosystem.ToolProjection) {
	sendMsg(a.program, ToolCallResultMsg{
		ID: callID, Name: name, Result: result, IsError: isError, Duration: duration, Projection: projection,
	})
}

func (a *Adapter) SystemMessage(msg string) {
	sendMsg(a.program, SystemMessageMsg{Msg: msg})
}

func (a *Adapter) CapabilityRoute(route agent.CapabilityRoute) {
	sendMsg(a.program, CapabilityRouteMsg{Route: route})
}

// ContinuationSuggestion forwards only the already bounded presentation. Tool
// arguments, workspace references, command strings, and raw receipt content
// never cross into Bubble Tea state.
func (a *Adapter) ContinuationSuggestion(turnID string, sequence uint64, suggestion *agent.ContinuationSuggestion) {
	var presentation *ContinuationActionPresentation
	if suggestion != nil {
		presentation = &ContinuationActionPresentation{
			Tool: suggestion.Tool, Inputs: append([]string(nil), suggestion.Inputs...),
			BlockedBy: append([]string(nil), suggestion.BlockedBy...), ReasonCode: suggestion.ReasonCode,
		}
	}
	sendMsg(a.program, ContinuationActionMsg{TurnID: turnID, Sequence: sequence, Action: presentation})
}

// BobWorkspaceContext forwards only the bounded semantic workspace digest.
// The adapter detaches the value so an agent-side cache update cannot mutate a
// message that Bubble Tea has not processed yet.
func (a *Adapter) BobWorkspaceContext(state agent.BobWorkspaceContextState) {
	var digest *ecosystem.ReceiptDigest
	if state.Digest != nil {
		copy := *state.Digest
		copy.Items = append([]string(nil), state.Digest.Items...)
		copy.Required = append([]string(nil), state.Digest.Required...)
		digest = &copy
	}
	sendMsg(a.program, BobWorkspaceContextMsg{Generation: state.Generation, Digest: digest})
}

func (a *Adapter) ContextCompacted() {
	sendMsg(a.program, ContextCompactedMsg{})
}

func (a *Adapter) ContextCompactionStarted() {
	sendMsg(a.program, ContextCompactionStartedMsg{})
}

func (a *Adapter) ContextCompactionFinished() {
	sendMsg(a.program, ContextCompactionFinishedMsg{})
}

func (a *Adapter) Error(msg string) {
	// Log error for debugging
	if len(msg) > 100 {
		msg = msg[:97] + "..."
	}
	sendMsg(a.program, ErrorMsg{Msg: msg})
}

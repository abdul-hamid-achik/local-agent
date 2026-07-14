package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
)

// Message types for the BubbleTea update loop.

// StreamTextMsg delivers incremental text from the LLM.
type StreamTextMsg struct {
	Text string
}

// StreamThinkingMsg carries provider-native reasoning separately from answer text.
type StreamThinkingMsg struct{ Text string }

// StreamDoneMsg signals the LLM has finished responding.
type StreamDoneMsg struct {
	EvalCount    int
	PromptTokens int
}

// ToolCallStartMsg signals a tool invocation has begun.
type ToolCallStartMsg struct {
	ID                      string
	Name                    string
	Args                    map[string]any
	StartTime               time.Time
	BeforeContent           string
	BeforeSnapshotAvailable bool
}

// ToolCallResultMsg delivers the result of a tool call.
type ToolCallResultMsg struct {
	ID         string
	Name       string
	Result     string
	IsError    bool
	Duration   time.Duration
	Projection ecosystem.ToolProjection
}

// ErrorMsg reports an error.
type ErrorMsg struct {
	Msg string
}

// SystemMessageMsg displays a system-level message.
type SystemMessageMsg struct {
	Msg string
}

// CapabilityRouteMsg is an ephemeral host advisory. It must never be appended
// to ChatEntry, ToolEntry, session state, or evidence receipts.
type CapabilityRouteMsg struct {
	Route agent.CapabilityRoute
}

// ContextCompactedMsg invalidates the previous provider occupancy snapshot.
// The retained history is smaller, but its next exact prompt size is not known
// until Ollama reports the following request.
type ContextCompactedMsg struct{}

// ContextCompactionStartedMsg and ContextCompactionFinishedMsg expose the
// hidden summarization request as one explicit UI phase. They carry no model
// text or transcript data.
type ContextCompactionStartedMsg struct{}
type ContextCompactionFinishedMsg struct{}

// AgentDoneMsg signals the agent loop has completed.
type AgentDoneMsg struct {
	TurnID string
	Err    error
}

// ShutdownMsg requests a graceful stop. Active turns are cancelled and joined
// before BubbleTea exits so dispatched effects receive a final receipt.
type ShutdownMsg struct{}

// FailedServer records an MCP server that failed to connect.
type FailedServer struct {
	Name   string
	Reason string
}

// InitCompleteMsg signals startup is done.
type InitCompleteMsg struct {
	Model                    string
	ModelList                []string
	OllamaModels             []OllamaModelDescriptor
	OllamaVersion            string
	LocalOnly                bool
	OllamaInventoryAttempted bool
	AgentProfile             string
	AgentList                []string
	ToolCount                int
	ServerCount              int
	NumCtx                   int
	FailedServers            []FailedServer
	ICEEnabled               bool
	ICEConversations         int
	ICESessionID             string
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
	Generation uint64
	Tag        int
	Results    []Completion
}

// CompletionDebounceTickMsg fires after the debounce interval to trigger a search.
type CompletionDebounceTickMsg struct {
	Generation uint64
	Tag        int
	Query      string
	Path       string
}

// PlanFormCompletedMsg signals the plan form has been submitted with a structured prompt.
type PlanFormCompletedMsg struct {
	Prompt string
}

// DoneFlashExpiredMsg clears the "done" terminal title after a timeout.
type DoneFlashExpiredMsg struct{}

// SessionListMsg delivers the list of saved SQLite sessions.
type SessionListMsg struct {
	ListToken uint64
	Sessions  []SessionListItem
	Err       error
}

// SessionLoadedMsg delivers a persisted session and its execution lease.
type SessionLoadedMsg struct {
	LoadToken        uint64
	SessionID        int64
	State            persistedSessionState
	StateRecord      db.SessionStateRecord
	Title            string
	RecoveryWarning  string
	RecoveryTarget   *agent.UnresolvedExecutionError
	RecoveryContexts []db.StandaloneReconciliationContext
	ExecutionLease   *db.ExecutionSessionLease
	Err              error
}

// ToolApprovalMsg asks the user to approve a tool call.
type ToolApprovalMsg struct {
	RequestID       string
	ToolName        string
	Args            map[string]any
	ArgumentsSHA256 string
	Preview         permission.ApprovalPreview
	Scope           permission.ApprovalScope
	Response        chan<- permission.ApprovalResponse
}

// CommitResultMsg carries the result of an async /commit operation.
type CommitResultMsg struct {
	Token   uint64
	Message string // commit message used
	Err     error
}

// ContextLoadResultMsg completes a bounded asynchronous /load operation.
type ContextLoadResultMsg struct {
	Token uint64
	Path  string
	Data  string
	Err   error
}

// ReadScopeResultMsg completes one process-local external read-root change.
// The Agent remains the authority for canonicalization and overlap checks.
type ReadScopeResultMsg struct {
	Token     uint64
	Operation string
	Path      string
	Count     int
	Err       error
}

// ImportResultMsg completes a bounded asynchronous /import operation. Parsing
// is done off the BubbleTea update loop so a large valid transcript cannot
// freeze rendering.
type ImportResultMsg struct {
	Token          uint64
	Path           string
	Entries        []ChatEntry
	Messages       []llm.Message
	UIOnlySections int
	ToolSections   int
	Err            error
}

// ExportResultMsg reports the outcome of an atomic asynchronous /export.
type ExportResultMsg struct {
	Token uint64
	Path  string
	Err   error
}

// sendMsg is a helper to send a tea.Msg to the program.
func sendMsg(p *tea.Program, msg tea.Msg) {
	if p != nil {
		p.Send(msg)
	}
}

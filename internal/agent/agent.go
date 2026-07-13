package agent

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/ice"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
)

// Agent orchestrates the LLM and tools in a ReAct loop.
type Agent struct {
	mu               sync.RWMutex
	llmClient        llm.Client
	registry         *mcp.Registry
	messages         []llm.Message
	skillContent     string
	loadedCtx        string
	numCtx           int
	memoryStore      *memory.Store
	iceEngine        *ice.Engine
	router           config.ModelRouter
	modePrefix       string
	toolPolicy       ToolPolicy
	workDir          string
	ignoreContent    string
	permChecker      *permission.Checker
	approvalCallback func(permission.ApprovalRequest)
	toolsConfig      config.ToolsConfig
	logger           *log.Logger
	turnRunning      atomic.Bool
	turnMu           sync.Mutex
	turnCancel       context.CancelFunc
	turnDone         chan struct{}
	closed           bool
	readOnlySlots    chan struct{}
	hooks            []ToolHook
	mcpServerScope   map[string]struct{}
	mcpScopeSet      bool

	checkpointStore     CheckpointStore
	checkpointSessionID int64

	executionLedger     ExecutionLedger
	executionSessionID  int64
	executionCursor     int64
	executionRunID      string
	executionRunIDErr   error
	requireExecutionLog bool
	unresolvedExecution *UnresolvedExecutionError
}

// contextWindowProvider is an optional capability implemented by clients that
// can report the context window of their currently selected model.
type contextWindowProvider interface {
	NumCtx() int
}

// SetLogger sets the structured logger used for observability. Safe to leave
// unset; all logging is nil-guarded.
func (a *Agent) SetLogger(l *log.Logger) {
	a.logger = l
}

// log returns a logger scoped to the given turn correlation ID, or nil.
func (a *Agent) logTurn(turnID string) *log.Logger {
	if a.logger == nil {
		return nil
	}
	return a.logger.With("turn", turnID)
}

// New creates a new Agent.
func New(llmClient llm.Client, registry *mcp.Registry, numCtx int) *Agent {
	runID, runIDErr := execution.NewRunID()
	return &Agent{
		llmClient:         llmClient,
		registry:          registry,
		numCtx:            numCtx,
		toolPolicy:        DefaultToolPolicy(),
		executionRunID:    runID,
		executionRunIDErr: runIDErr,
		// Filesystem reads can enter OS syscalls that do not observe context
		// cancellation. Allow at most one abandoned worker for the lifetime of
		// an Agent; later reads wait on this slot and remain cancellable.
		readOnlySlots: make(chan struct{}, 1),
	}
}

// SetRouter sets the model router for auto-selection.
func (a *Agent) SetRouter(router config.ModelRouter) {
	a.router = router
}

// SetModeContext configures the mode prefix injected into the system prompt
// and the tool policy for the current turn.
func (a *Agent) SetModeContext(prefix string, policy ToolPolicy) {
	a.modePrefix = prefix
	a.toolPolicy = policy
}

// AppendLoadedContext appends to the loaded context.
func (a *Agent) AppendLoadedContext(content string) {
	if a.loadedCtx == "" {
		a.loadedCtx = content
	} else {
		a.loadedCtx += content
	}
}

// Router returns the model router.
func (a *Agent) Router() config.ModelRouter {
	return a.router
}

// NumCtx returns the active provider context window when available, falling
// back to the value configured when the agent was created.
func (a *Agent) NumCtx() int {
	a.mu.RLock()
	client := a.llmClient
	fallback := a.numCtx
	a.mu.RUnlock()

	if provider, ok := client.(contextWindowProvider); ok {
		if numCtx := provider.NumCtx(); numCtx > 0 {
			return numCtx
		}
	}
	return fallback
}

// SetMemoryStore sets the memory store for cross-session persistence.
func (a *Agent) SetMemoryStore(store *memory.Store) {
	a.mu.Lock()
	a.memoryStore = store
	a.mu.Unlock()
}

// MemoryStore returns the active project-scoped memory store.
func (a *Agent) MemoryStore() *memory.Store {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.memoryStore
}

// AddUserMessage appends a user message to the conversation.
func (a *Agent) AddUserMessage(content string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, llm.Message{
		Role:    "user",
		Content: content,
	})
}

// Messages returns a copy of the conversation history. A copy (not the live
// slice) is required because callers read it from other goroutines (e.g. the
// TUI's checkpoint-restore path) while Run may be appending concurrently.
func (a *Agent) Messages() []llm.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]llm.Message, len(a.messages))
	copy(out, a.messages)
	return out
}

// ClearHistory resets the conversation history.
func (a *Agent) ClearHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = nil
}

// AppendMessage appends a message to the conversation history.
func (a *Agent) AppendMessage(msg llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, msg)
}

// ReplaceMessages replaces the entire conversation history.
func (a *Agent) ReplaceMessages(msgs []llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = msgs
}

// SetSkillContent sets the combined content of active skills.
func (a *Agent) SetSkillContent(content string) {
	a.skillContent = content
}

// SetLoadedContext sets the loaded context file content.
func (a *Agent) SetLoadedContext(content string) {
	a.loadedCtx = content
}

// LoadedContext returns the currently assembled loaded context.
func (a *Agent) LoadedContext() string {
	return a.loadedCtx
}

// SkillContent returns the currently active skill prompt content.
func (a *Agent) SkillContent() string {
	return a.skillContent
}

// Model returns the LLM model name.
func (a *Agent) Model() string {
	return a.llmClient.Model()
}

// LLMClient returns the underlying LLM client.
func (a *Agent) LLMClient() llm.Client {
	return a.llmClient
}

// ToolCount returns the number of available tools.
func (a *Agent) ToolCount() int {
	count := len(a.toolPolicy.localTools)
	if a.memoryStore != nil {
		count += len(a.toolPolicy.memoryTools)
	}
	if a.toolPolicy.AllowMCP && a.registry != nil {
		count += len(a.mcpTools())
	}
	return count
}

// SetMCPServerScope restricts model-visible and executable MCP tools to the
// named servers. An empty scope keeps the default of all configured servers.
func (a *Agent) SetMCPServerScope(serverNames []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(serverNames) == 0 {
		a.mcpServerScope = nil
		a.mcpScopeSet = false
		return
	}
	a.mcpScopeSet = true
	a.mcpServerScope = make(map[string]struct{}, len(serverNames))
	for _, name := range serverNames {
		if name = strings.TrimSpace(name); name != "" {
			a.mcpServerScope[name] = struct{}{}
		}
	}
}

// DenyAllMCPTools installs an explicit empty scope. It is used when an
// explicitly requested profile fails to load, preventing fallback to the
// default all-server authority.
func (a *Agent) DenyAllMCPTools() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mcpScopeSet = true
	a.mcpServerScope = make(map[string]struct{})
}

// MCPServerScope reports the explicit MCP allowlist. restricted=false means
// the default all-server scope; restricted=true with no names means deny all.
func (a *Agent) MCPServerScope() (names []string, restricted bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.mcpScopeSet {
		return nil, false
	}
	names = make([]string, 0, len(a.mcpServerScope))
	for name := range a.mcpServerScope {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true
}

func (a *Agent) mcpTools() []llm.ToolDef {
	if a.registry == nil {
		return nil
	}
	tools := a.registry.Tools()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.mcpScopeSet {
		return tools
	}
	filtered := make([]llm.ToolDef, 0, len(tools))
	for _, tool := range tools {
		server, _, namespaced := strings.Cut(tool.Name, "__")
		if namespaced {
			if _, allowed := a.mcpServerScope[server]; allowed {
				filtered = append(filtered, tool)
			}
		}
	}
	return filtered
}

func (a *Agent) allowsMCPTool(toolName string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.mcpScopeSet {
		return true
	}
	server, _, namespaced := strings.Cut(toolName, "__")
	if !namespaced {
		return false
	}
	_, allowed := a.mcpServerScope[server]
	return allowed
}

// ServerCount returns the number of connected MCP servers.
func (a *Agent) ServerCount() int {
	if a.registry == nil {
		return 0
	}
	return a.registry.ServerCount()
}

// ServerNames returns the names of connected MCP servers.
func (a *Agent) ServerNames() []string {
	if a.registry == nil {
		return nil
	}
	return a.registry.ServerNames()
}

// SetWorkDir sets the working directory for environment context in the system prompt.
func (a *Agent) SetWorkDir(dir string) {
	a.workDir = dir
}

// WorkDir returns the workspace boundary used by built-in tools.
func (a *Agent) WorkDir() string {
	return a.workDir
}

// SetIgnoreContent sets the raw .agentignore content for injection into the system prompt.
func (a *Agent) SetIgnoreContent(content string) {
	a.ignoreContent = content
}

// SetPermissionChecker sets the permission checker for tool approval.
func (a *Agent) SetPermissionChecker(checker *permission.Checker) {
	a.permChecker = checker
}

// SetApprovalCallback sets the callback for requesting user approval.
func (a *Agent) SetApprovalCallback(cb func(permission.ApprovalRequest)) {
	a.approvalCallback = cb
}

// SetICEEngine sets the ICE engine for cross-session context retrieval.
func (a *Agent) SetICEEngine(engine *ice.Engine) {
	a.iceEngine = engine
}

// ICEEngine returns the ICE engine, or nil if not enabled.
func (a *Agent) ICEEngine() *ice.Engine {
	return a.iceEngine
}

// PrepareModelSwitch cancels and joins background ICE inference so the model
// manager can unload/switch without racing a stream on the previous model.
func (a *Agent) PrepareModelSwitch() {
	if a.iceEngine != nil {
		a.iceEngine.StopAutoMemory()
	}
}

// SetToolsConfig sets the tools configuration.
func (a *Agent) SetToolsConfig(cfg config.ToolsConfig) {
	a.toolsConfig = cfg
}

// MaxIterations returns the configured max iterations, or default if not set.
func (a *Agent) MaxIterations() int {
	if a.toolsConfig.MaxIterations > 0 {
		return a.toolsConfig.MaxIterations
	}
	return 10
}

// ToolTimeout returns the configured tool timeout, or default if not set.
func (a *Agent) ToolTimeout() time.Duration {
	if a.toolsConfig.Timeout != "" {
		if d, err := time.ParseDuration(a.toolsConfig.Timeout); err == nil {
			return d
		}
	}
	return 30 * time.Second
}

// MaxGrepResults returns the configured max grep results, or default if not set.
func (a *Agent) MaxGrepResults() int {
	if a.toolsConfig.MaxGrepResults > 0 {
		return a.toolsConfig.MaxGrepResults
	}
	return 500
}

// Close cleans up resources.
func (a *Agent) Close() {
	a.turnMu.Lock()
	if a.closed {
		done := a.turnDone
		a.turnMu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	a.closed = true
	cancel := a.turnCancel
	done := a.turnDone
	a.turnMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if a.iceEngine != nil {
		_ = a.iceEngine.Close()
	}
	if a.registry != nil {
		a.registry.Close()
	}
}

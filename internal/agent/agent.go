package agent

import (
	"sync"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
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
}

// New creates a new Agent.
func New(llmClient llm.Client, registry *mcp.Registry, numCtx int) *Agent {
	return &Agent{
		llmClient:  llmClient,
		registry:   registry,
		numCtx:     numCtx,
		toolPolicy: DefaultToolPolicy(),
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

// NumCtx returns the context window size.
func (a *Agent) NumCtx() int {
	return a.numCtx
}

// SetMemoryStore sets the memory store for cross-session persistence.
func (a *Agent) SetMemoryStore(store *memory.Store) {
	a.memoryStore = store
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

// Messages returns the conversation history.
func (a *Agent) Messages() []llm.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.messages
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
		count += a.registry.ToolCount()
	}
	return count
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
	if a.iceEngine != nil {
		_ = a.iceEngine.Flush()
	}
	if a.registry != nil {
		a.registry.Close()
	}
}

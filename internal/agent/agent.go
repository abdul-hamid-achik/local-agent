package agent

import (
	"github.com/abdulachik/local-agent/internal/config"
	"github.com/abdulachik/local-agent/internal/ice"
	"github.com/abdulachik/local-agent/internal/llm"
	"github.com/abdulachik/local-agent/internal/mcp"
	"github.com/abdulachik/local-agent/internal/memory"
)

const maxIterations = 10

// Agent orchestrates the LLM and tools in a ReAct loop.
type Agent struct {
	llmClient    llm.Client
	registry     *mcp.Registry
	messages     []llm.Message
	skillContent string
	loadedCtx    string
	numCtx       int
	memoryStore  *memory.Store
	iceEngine    *ice.Engine
	router       *config.Router
	modePrefix   string
	toolsEnabled bool
	workDir      string
}

// New creates a new Agent.
func New(llmClient llm.Client, registry *mcp.Registry, numCtx int) *Agent {
	return &Agent{
		llmClient:    llmClient,
		registry:     registry,
		numCtx:       numCtx,
		toolsEnabled: true,
	}
}

// SetRouter sets the model router for auto-selection.
func (a *Agent) SetRouter(router *config.Router) {
	a.router = router
}

// SetModeContext configures the mode prefix injected into the system prompt
// and whether tools are available for the current turn.
func (a *Agent) SetModeContext(prefix string, allowTools bool) {
	a.modePrefix = prefix
	a.toolsEnabled = allowTools
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
func (a *Agent) Router() *config.Router {
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
	a.messages = append(a.messages, llm.Message{
		Role:    "user",
		Content: content,
	})
}

// Messages returns the conversation history.
func (a *Agent) Messages() []llm.Message {
	return a.messages
}

// ClearHistory resets the conversation history.
func (a *Agent) ClearHistory() {
	a.messages = nil
}

// SetSkillContent sets the combined content of active skills.
func (a *Agent) SetSkillContent(content string) {
	a.skillContent = content
}

// SetLoadedContext sets the loaded context file content.
func (a *Agent) SetLoadedContext(content string) {
	a.loadedCtx = content
}

// Model returns the LLM model name.
func (a *Agent) Model() string {
	return a.llmClient.Model()
}

// ToolCount returns the number of available tools.
func (a *Agent) ToolCount() int {
	count := a.registry.ToolCount()
	if a.memoryStore != nil {
		count += 2 // memory_save + memory_recall
	}
	return count
}

// ServerCount returns the number of connected MCP servers.
func (a *Agent) ServerCount() int {
	return a.registry.ServerCount()
}

// ServerNames returns the names of connected MCP servers.
func (a *Agent) ServerNames() []string {
	return a.registry.ServerNames()
}

// SetWorkDir sets the working directory for environment context in the system prompt.
func (a *Agent) SetWorkDir(dir string) {
	a.workDir = dir
}

// SetICEEngine sets the ICE engine for cross-session context retrieval.
func (a *Agent) SetICEEngine(engine *ice.Engine) {
	a.iceEngine = engine
}

// ICEEngine returns the ICE engine, or nil if not enabled.
func (a *Agent) ICEEngine() *ice.Engine {
	return a.iceEngine
}

// Close cleans up resources.
func (a *Agent) Close() {
	if a.iceEngine != nil {
		_ = a.iceEngine.Flush()
	}
	a.registry.Close()
}

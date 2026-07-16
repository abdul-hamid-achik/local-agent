package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"github.com/abdul-hamid-achik/local-agent/internal/capabilityadvisor"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
	"github.com/abdul-hamid-achik/local-agent/internal/ice"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
)

// Agent orchestrates the LLM and tools in a ReAct loop.
type Agent struct {
	mu                  sync.RWMutex
	llmClient           llm.Client
	registry            *mcp.Registry
	messages            []llm.Message
	skillContent        string
	skillLoader         SkillLoader
	loadedCtx           string
	numCtx              int
	memoryStore         *memory.Store
	iceEngine           *ice.Engine
	router              config.ModelRouter
	modePrefix          string
	toolPolicy          ToolPolicy
	authorityMode       AuthorityMode
	workDir             string
	ignoreContent       string
	filesystemVersion   uint64
	activeFilesystem    filesystemContext
	filesystemPinned    bool
	permChecker         *permission.Checker
	approvalCallback    func(permission.ApprovalRequest)
	approvalHostVersion uint64
	// approvalGrants contains host-approved, exact-request grants for this Agent
	// session. Keys bind workspace, tool and canonical arguments; they are never
	// persisted as global tool-only policies.
	approvalGrants      map[string]struct{}
	toolsConfig         config.ToolsConfig
	continuationsConfig config.ContinuationsConfig
	logger              *log.Logger
	turnRunning         atomic.Bool
	turnMu              sync.Mutex
	turnCancel          context.CancelFunc
	turnDone            chan struct{}
	closed              bool
	readOnlySlots       chan struct{}
	hooks               []ToolHook
	mcpServerScope      map[string]struct{}
	mcpScopeSet         bool
	trustedMCP          map[string]trustedMCPServer
	// mcpRouteVersion changes only when exact MCP route trust or scope changes.
	// It intentionally excludes approval-renderer churn so opaque continuation
	// contexts survive the UI installing a per-turn callback.
	mcpRouteVersion   uint64
	readRoots         map[string]*additionalReadRoot
	readFiles         map[string]*additionalReadFile
	capabilityAdvisor capabilityAdviser
	capabilityRetries map[capabilityRetryKey]struct{}
	expertConsultant  ExpertConsultant
	imageResolver     ImageResolver
	mcphubResults     *ecosystem.MCPHubResultAssembler
	// continuationContracts is a bounded, ephemeral cache of exact downstream
	// input schemas observed through a trusted MCPHub describe call. It is never
	// serialized and never grants authority; the host trust catalog still owns
	// route and effect classification.
	continuationContracts map[string]continuationContract
	continuationSequence  uint64
	continuationHistory   *continuationTurnState
	// autoContinuationHistory is a separate, bounded session-scope reservation
	// ledger. Suggesting an action never writes it; only an LA-3 schedule claim
	// does. It prevents the same source revision/context from auto-running again
	// across model turns.
	autoContinuationHistory *autoContinuationHistory
	continuationFreshness   *continuationFreshnessState

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

// ExpertConsultant is the bounded read-only team runtime surface. Keeping the
// interface on Agent allows deterministic fakes without coupling tests to a
// provider implementation.
type ExpertConsultant interface {
	Consult(context.Context, expertteam.Request) (expertteam.Result, error)
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
	agent := &Agent{
		llmClient:               llmClient,
		registry:                registry,
		numCtx:                  numCtx,
		toolPolicy:              DefaultToolPolicy(),
		authorityMode:           AuthorityNormal,
		executionRunID:          runID,
		executionRunIDErr:       runIDErr,
		approvalGrants:          make(map[string]struct{}),
		trustedMCP:              make(map[string]trustedMCPServer),
		readRoots:               make(map[string]*additionalReadRoot),
		readFiles:               make(map[string]*additionalReadFile),
		capabilityRetries:       make(map[capabilityRetryKey]struct{}),
		mcphubResults:           ecosystem.NewMCPHubResultAssembler(),
		continuationContracts:   make(map[string]continuationContract),
		continuationHistory:     newContinuationTurnState(0),
		autoContinuationHistory: newAutoContinuationHistory(),
		continuationFreshness:   newContinuationFreshnessState(),
		continuationsConfig: config.ContinuationsConfig{
			Mode:         config.ContinuationSuggest,
			MaxAutoSteps: config.MaxAutoContinuationSteps,
		},
		// Filesystem reads can enter OS syscalls that do not observe context
		// cancellation. Allow at most one abandoned worker for the lifetime of
		// an Agent; later reads wait on this slot and remain cancellable.
		readOnlySlots: make(chan struct{}, 1),
	}
	capabilityRegistry := scopedCapabilityRegistry{agent: agent}
	if registry != nil {
		capabilityRegistry.backend = registry
	}
	agent.capabilityAdvisor = capabilityadvisor.New(capabilityRegistry)
	return agent
}

// SetRouter sets the model router for auto-selection.
func (a *Agent) SetRouter(router config.ModelRouter) {
	a.router = router
}

// SetExpertConsultant installs application-level Team/Swarm/MoE support. A nil
// consultant removes the model-visible tool.
func (a *Agent) SetExpertConsultant(consultant ExpertConsultant) {
	a.mu.Lock()
	a.expertConsultant = consultant
	a.mu.Unlock()
}

// SetImageResolver installs the path-free content-addressed lookup used to
// rehydrate images restored from durable session state. A nil resolver removes
// the lookup. The callback is always invoked without an Agent lock held.
func (a *Agent) SetImageResolver(resolver ImageResolver) {
	a.mu.Lock()
	a.imageResolver = resolver
	a.mu.Unlock()
}

// SetModeContext configures the mode prefix injected into the system prompt
// and the tool policy for the current turn.
func (a *Agent) SetModeContext(prefix string, policy ToolPolicy) {
	a.mu.Lock()
	a.modePrefix = prefix
	a.toolPolicy = policy
	a.mu.Unlock()
}

// modeContext returns one coherent mode snapshot. A turn keeps this snapshot
// for its lifetime so a concurrent host-mode change cannot alter its visible
// tools after the provider has already received the system prompt.
func (a *Agent) modeContext() (string, ToolPolicy) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.modePrefix, a.toolPolicy
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
	_ = a.AddUserMessageWithImages(content, nil)
}

// AddUserMessageWithImages appends a user message with transient image bytes
// and/or complete durable references. It validates and defensively copies every
// image before publishing the message to concurrent readers.
func (a *Agent) AddUserMessageWithImages(content string, images []llm.ImageData) error {
	cloned, err := cloneValidatedImages(images)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, llm.Message{
		Role:    "user",
		Content: content,
		Images:  cloned,
	})
	return nil
}

// Messages returns a copy of the conversation history. A copy (not the live
// slice) is required because callers read it from other goroutines (e.g. the
// TUI's checkpoint-restore path) while Run may be appending concurrently.
func (a *Agent) Messages() []llm.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneMessagesWithImages(a.messages)
}

// ClearHistory resets the conversation history.
func (a *Agent) ClearHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = nil
	a.continuationHistory = newContinuationTurnState(0)
	a.resetAutoContinuationHistoryLocked()
}

// AppendMessage appends a message to the conversation history.
func (a *Agent) AppendMessage(msg llm.Message) {
	msg.Images = cloneImages(msg.Images)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, msg)
}

// AppendDurableRecoveryContext installs one already validated, host-authored
// reconciliation receipt. Exact content is idempotent; a persisted copy is
// re-marked HostOwned after restore instead of being duplicated. Callers must
// derive content from durable typed reconciliation rather than user text.
func (a *Agent) AppendDurableRecoveryContext(content string) error {
	if !strings.HasPrefix(content, DurableRecoveryContextPrefix) {
		return fmt.Errorf("durable recovery context has an invalid prefix")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	next := make([]llm.Message, 0, len(a.messages)+1)
	found := false
	seenHostContent := make(map[string]struct{})
	for _, message := range a.messages {
		if message.Role == "system" && strings.HasPrefix(message.Content, DurableRecoveryContextPrefix) {
			if message.Content == content {
				message.HostOwned = true
				found = true
			}
			// A prefix never grants authority. Persisted or otherwise injected
			// prefixed messages are removed unless the host marker is already
			// present or this exact content is the newly validated projection.
			if !message.HostOwned {
				continue
			}
			if _, duplicate := seenHostContent[message.Content]; duplicate {
				continue
			}
			seenHostContent[message.Content] = struct{}{}
		}
		next = append(next, message)
	}
	if !found {
		next = append(next, llm.Message{Role: "system", Content: content, HostOwned: true})
	}
	if _, err := collectDurableRecoveryContexts(next); err != nil {
		return err
	}
	a.messages = next
	return nil
}

// InstallDurableRecoveryContexts replaces every prefixed system message with
// the complete set derived from the current durable DB projection. This is the
// restore boundary: persisted JSON carries no HostOwned marker, so no saved
// prefix text is allowed to survive unless the DB independently re-authorizes
// its exact content.
func (a *Agent) InstallDurableRecoveryContexts(contents []string) error {
	nextContexts := make([]llm.Message, 0, len(contents))
	seen := make(map[string]struct{}, len(contents))
	for _, content := range contents {
		if !strings.HasPrefix(content, DurableRecoveryContextPrefix) {
			return fmt.Errorf("durable recovery context has an invalid prefix")
		}
		if _, duplicate := seen[content]; duplicate {
			continue
		}
		seen[content] = struct{}{}
		nextContexts = append(nextContexts, llm.Message{Role: "system", Content: content, HostOwned: true})
	}
	if _, err := collectDurableRecoveryContexts(nextContexts); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	next := make([]llm.Message, 0, len(a.messages)+len(nextContexts))
	for _, message := range a.messages {
		if message.Role == "system" && strings.HasPrefix(message.Content, DurableRecoveryContextPrefix) {
			continue
		}
		next = append(next, message)
	}
	next = append(next, nextContexts...)
	a.messages = next
	return nil
}

// ReplaceMessages replaces the entire conversation history.
func (a *Agent) ReplaceMessages(msgs []llm.Message) {
	a.replaceMessages(msgs, true)
}

// ReplaceMessagesWithinSession atomically rewrites the transcript for
// compaction or transactional rollback without erasing LA-2/LA-3 replay
// history. Callers must use ReplaceMessages for a genuine restore, import, or
// conversation boundary.
func (a *Agent) ReplaceMessagesWithinSession(msgs []llm.Message) {
	a.replaceMessages(msgs, false)
}

func (a *Agent) replaceMessages(msgs []llm.Message, resetContinuationHistory bool) {
	msgs = cloneMessagesWithImages(msgs)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = msgs
	if resetContinuationHistory {
		a.continuationHistory = newContinuationTurnState(0)
		a.resetAutoContinuationHistoryLocked()
	}
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

// ToolAvailability separates local authority from currently connected MCP
// transport. MCPRetained includes scoped definitions kept during reconnect so
// diagnostics can explain why a tool remains known without calling it ready.
type ToolAvailability struct {
	Local        int
	MCPConnected int
	MCPRetained  int
}

// Ready returns tool definitions backed by local authority or a currently
// connected MCP namespace.
func (availability ToolAvailability) Ready() int {
	return availability.Local + availability.MCPConnected
}

// ToolAvailability returns a non-blocking host snapshot. It does not ping or
// reconnect MCP servers and says nothing about downstream domain success.
func (a *Agent) ToolAvailability() ToolAvailability {
	if a == nil {
		return ToolAvailability{}
	}
	a.mu.RLock()
	policy := a.toolPolicy
	hasMemory := a.memoryStore != nil
	a.mu.RUnlock()

	availability := ToolAvailability{
		Local: len(filterToolDefsByName(a.toolsBuiltinToolDefs(), policy.localTools)),
	}
	if hasMemory {
		availability.Local += len(filterToolDefsByName(a.memoryBuiltinToolDefs(), policy.memoryTools))
	}
	if !policy.AllowMCP || a.registry == nil {
		return availability
	}

	mcpTools := a.mcpTools()
	availability.MCPRetained = len(mcpTools)
	connected := make(map[string]struct{})
	for _, status := range a.registry.ConnectionStatuses() {
		if status.Connected {
			connected[status.Name] = struct{}{}
		}
	}
	for _, tool := range mcpTools {
		server, _, namespaced := strings.Cut(tool.Name, "__")
		if !namespaced {
			continue
		}
		if _, ready := connected[server]; ready {
			availability.MCPConnected++
		}
	}
	return availability
}

// ToolCount returns the model-visible tool definition count. During an MCP
// reconnect this intentionally includes retained definitions; presentation
// surfaces should use ToolAvailability when claiming readiness.
func (a *Agent) ToolCount() int {
	availability := a.ToolAvailability()
	return availability.Local + availability.MCPRetained
}

// SetMCPServerScope restricts model-visible and executable MCP tools to the
// named servers. An empty scope keeps the default of all configured servers.
func (a *Agent) SetMCPServerScope(serverNames []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.approvalHostVersion++
	a.mcpRouteVersion++
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
	a.approvalHostVersion++
	a.mcpRouteVersion++
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
	return a.mcpToolSnapshot().Tools
}

func (a *Agent) mcpToolSnapshot() mcp.ToolSnapshot {
	if a.registry == nil {
		return mcp.ToolSnapshot{}
	}
	snapshot := a.registry.SnapshotTools()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.mcpScopeSet {
		return snapshot
	}
	filtered := make([]llm.ToolDef, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		server, _, namespaced := strings.Cut(tool.Name, "__")
		if namespaced {
			if _, allowed := a.mcpServerScope[server]; allowed {
				filtered = append(filtered, tool)
			}
		}
	}
	snapshot.Tools = filtered
	return snapshot
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

// SetWorkspacePolicy atomically updates the workspace boundary and its ignore
// policy for the next turn. Embeddings that reload both values should prefer
// this method so a turn cannot snapshot a mixed pair between two setter calls.
func (a *Agent) SetWorkspacePolicy(dir, ignoreContent string) {
	a.mu.Lock()
	workspaceChanged := a.workDir != dir
	if a.workDir != dir || a.ignoreContent != ignoreContent {
		a.workDir = dir
		a.ignoreContent = ignoreContent
		a.filesystemVersion++
	}
	if workspaceChanged {
		a.continuationHistory = newContinuationTurnState(0)
		a.resetAutoContinuationHistoryLocked()
	}
	a.mu.Unlock()
}

// SetWorkDir sets only the configured working directory. Use
// SetWorkspacePolicy when the corresponding ignore policy changes too.
func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	if a.workDir != dir {
		a.workDir = dir
		a.filesystemVersion++
		a.continuationHistory = newContinuationTurnState(0)
		a.resetAutoContinuationHistoryLocked()
	}
	a.mu.Unlock()
}

// WorkDir returns the configured workspace boundary. A running turn keeps the
// snapshot it admitted with; a value configured mid-turn applies next turn.
func (a *Agent) WorkDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workDir
}

// SetIgnoreContent sets only the configured .agentignore content. Use
// SetWorkspacePolicy when changing the workspace and ignore policy together.
func (a *Agent) SetIgnoreContent(content string) {
	a.mu.Lock()
	if a.ignoreContent != content {
		a.ignoreContent = content
		a.filesystemVersion++
	}
	a.mu.Unlock()
}

// SetPermissionChecker sets the permission checker for tool approval.
func (a *Agent) SetPermissionChecker(checker *permission.Checker) {
	a.mu.Lock()
	if a.permChecker != checker {
		a.permChecker = checker
		a.approvalHostVersion++
	}
	a.mu.Unlock()
}

// SetApprovalCallback sets the callback for requesting user approval.
func (a *Agent) SetApprovalCallback(cb func(permission.ApprovalRequest)) {
	a.mu.Lock()
	a.approvalCallback = cb
	a.approvalHostVersion++
	a.mu.Unlock()
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

// SetContinuationsConfig installs the host policy used by future turns.
// Invalid embedded-caller input fails closed to off; command/config callers
// normally validate before reaching this boundary.
func (a *Agent) SetContinuationsConfig(cfg config.ContinuationsConfig) {
	if !cfg.Mode.Valid() || cfg.MaxAutoSteps < 0 || cfg.MaxAutoSteps > config.MaxAutoContinuationSteps ||
		(cfg.Mode == config.ContinuationAutoReadOnly && cfg.MaxAutoSteps == 0) {
		cfg = config.ContinuationsConfig{Mode: config.ContinuationOff}
	}
	a.mu.Lock()
	a.continuationsConfig = cfg
	a.mu.Unlock()
}

func (a *Agent) continuationsConfigSnapshot() config.ContinuationsConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cfg := a.continuationsConfig
	if !cfg.Mode.Valid() || cfg.MaxAutoSteps < 0 || cfg.MaxAutoSteps > config.MaxAutoContinuationSteps ||
		(cfg.Mode == config.ContinuationAutoReadOnly && cfg.MaxAutoSteps == 0) {
		return config.ContinuationsConfig{Mode: config.ContinuationOff}
	}
	return cfg
}

// MaxIterations returns the configured max iterations, or default if not set.
func (a *Agent) MaxIterations() int {
	if a.toolsConfig.MaxIterations > 0 {
		return a.toolsConfig.MaxIterations
	}
	return 10
}

// MaxIterationsForAuthority keeps interactive turns concise while giving AUTO
// enough room for normal inspect/edit/verify loops. The limit remains bounded
// so a provider that never settles cannot run forever.
func (a *Agent) MaxIterationsForAuthority(mode AuthorityMode) int {
	if mode == AuthorityAutoScoped {
		if a.toolsConfig.AutoMaxIterations > 0 {
			return a.toolsConfig.AutoMaxIterations
		}
		return 40
	}
	return a.MaxIterations()
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
	a.mcphubResults.Reset()
	a.clearContinuationContracts()
	if a.iceEngine != nil {
		_ = a.iceEngine.Close()
	}
	if a.registry != nil {
		a.registry.Close()
	}
	a.closeReadRoots()
}

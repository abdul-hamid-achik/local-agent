package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

// FailedServer records an MCP server that failed to connect.
type FailedServer struct {
	Name   string
	Reason string
}

// ServerStatus represents the health status of an MCP server.
type ServerStatus struct {
	Name      string
	Connected bool
	LastError string
	LastPing  time.Time
}

type toolRoute struct {
	client     toolCaller
	remoteName string
}

type toolCaller interface {
	CallTool(context.Context, string, map[string]any) (*ToolResult, error)
}

// ErrRegistryClosed reports an operation attempted after shutdown began.
var ErrRegistryClosed = errors.New("MCP registry is closed")

// Registry manages multiple MCP server connections and routes tool calls.
type Registry struct {
	mu             sync.RWMutex
	clients        map[string]*MCPClient
	toolMap        map[string]toolRoute // exposed tool name -> server and remote name
	serverTools    map[string][]llm.ToolDef
	serverGuidance map[string]string
	failedServers  []FailedServer
	serverConfigs  map[string]config.ServerConfig // name -> config for reconnection
	callTimeout    time.Duration                  // per tool-call timeout (0 = default)
	version        string                         // advertised MCP client implementation version
	closed         bool
	lifecycleCtx   context.Context
	cancel         context.CancelFunc
	lifecycleWG    sync.WaitGroup
	closeOnce      sync.Once

	// Test seams keep shutdown overlap tests deterministic without launching a
	// real child process. Production always uses discoverServer/client.Close.
	// A test connector must honor ctx cancellation, matching that production
	// contract; the real STDIO lifecycle regression proves the production path.
	testConnector   func(context.Context, config.ServerConfig) (*MCPClient, []llm.ToolDef, error)
	testCloseClient func(*MCPClient) error
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return NewRegistryWithVersion(developmentImplementationVersion)
}

// NewRegistryWithVersion creates a registry whose MCP client handshakes
// advertise the same build version as the local-agent CLI.
func NewRegistryWithVersion(version string) *Registry {
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	return &Registry{
		toolMap:        make(map[string]toolRoute),
		clients:        make(map[string]*MCPClient),
		serverTools:    make(map[string][]llm.ToolDef),
		serverGuidance: make(map[string]string),
		serverConfigs:  make(map[string]config.ServerConfig),
		callTimeout:    defaultCallTimeout,
		version:        clientImplementation(version).Version,
		lifecycleCtx:   lifecycleCtx,
		cancel:         cancel,
	}
}

// SetCallTimeout overrides the per tool-call timeout. A non-positive value
// resets it to the default.
func (r *Registry) SetCallTimeout(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d <= 0 {
		d = defaultCallTimeout
	}
	r.callTimeout = d
}

// connectTimeout is the per-server connection timeout.
const connectTimeout = 15 * time.Second

// defaultCallTimeout bounds a single MCP tool call so a hung or slow server
// cannot block the agent loop (and freeze the UI) indefinitely.
const defaultCallTimeout = 60 * time.Second

// beginLifecycleOperation prevents WaitGroup Add/Wait races by admitting new
// work under the same mutex that Close uses to mark the registry closed.
func (r *Registry) beginLifecycleOperation(ctx context.Context) (context.Context, func(), error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, nil, ErrRegistryClosed
	}
	opCtx, cancel := context.WithCancel(ctx)
	stopLifecycleCancel := context.AfterFunc(r.lifecycleCtx, cancel)
	r.lifecycleWG.Add(1)
	r.mu.Unlock()

	finish := func() {
		stopLifecycleCancel()
		cancel()
		r.lifecycleWG.Done()
	}
	return opCtx, finish, nil
}

func (r *Registry) closeMCPClient(client *MCPClient) error {
	if client == nil {
		return nil
	}
	if r.testCloseClient != nil {
		return r.testCloseClient(client)
	}
	return client.Close()
}

// discoverServer is the only production connector. Every blocking SDK call is
// given ctx. STDIO Connect also links ctx to an owned process-group cancel, so
// Registry.Close's lifecycleWG wait cannot be stranded by a hanging child.
func (r *Registry) discoverServer(ctx context.Context, srv config.ServerConfig) (*MCPClient, []llm.ToolDef, error) {
	client, err := connectWithVersion(ctx, r.version, srv.Name, srv.Command, srv.Args, srv.Env, srv.Transport, srv.URL)
	if err != nil {
		return nil, nil, err
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		if closeErr := r.closeMCPClient(client); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close failed MCP connection: %w", closeErr))
		}
		return nil, nil, fmt.Errorf("%s tools: %w", srv.Name, err)
	}

	serverDefs := make([]llm.ToolDef, 0, len(tools))
	for _, tool := range tools {
		serverDefs = append(serverDefs, ToLLMToolDefFromMCP(tool))
	}
	return client, serverDefs, nil
}

// ConnectServer connects a single MCP server and registers its tools.
// Returns the number of tools discovered, or an error.
func (r *Registry) ConnectServer(ctx context.Context, srv config.ServerConfig) (int, error) {
	opCtx, finish, err := r.beginLifecycleOperation(ctx)
	if err != nil {
		return 0, err
	}
	defer finish()

	if strings.Contains(srv.Name, "__") {
		return 0, fmt.Errorf("server %q contains reserved namespace delimiter __", srv.Name)
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, ErrRegistryClosed
	}
	r.serverConfigs[srv.Name] = srv
	r.mu.Unlock()

	connCtx, cancel := context.WithTimeout(opCtx, connectTimeout)
	defer cancel()

	connector := r.testConnector
	if connector == nil {
		connector = r.discoverServer
	}
	client, serverDefs, err := connector(connCtx, srv)
	if err != nil {
		r.setFailedServer(srv.Name, err.Error())
		return 0, fmt.Errorf("connect to %s: %w", srv.Name, err)
	}
	if ctxErr := connCtx.Err(); ctxErr != nil {
		r.mu.RLock()
		registryClosed := r.closed
		r.mu.RUnlock()
		if registryClosed {
			ctxErr = ErrRegistryClosed
		}
		closeErr := r.closeMCPClient(client)
		if closeErr != nil {
			ctxErr = errors.Join(ctxErr, fmt.Errorf("close cancelled MCP connection: %w", closeErr))
		}
		return 0, fmt.Errorf("connect to %s cancelled before registration: %w", srv.Name, ctxErr)
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		closeErr := r.closeMCPClient(client)
		if closeErr != nil {
			return 0, errors.Join(ErrRegistryClosed, fmt.Errorf("close late MCP connection: %w", closeErr))
		}
		return 0, ErrRegistryClosed
	}
	existing := r.clients[srv.Name]
	if existing != nil {
		r.removeServerLocked(srv.Name)
	}
	r.registerConnectedServerLocked(srv.Name, client, serverDefs)
	r.mu.Unlock()
	if existing != nil {
		_ = r.closeMCPClient(existing)
	}

	return len(serverDefs), nil
}

// ConnectAll spawns and connects to all configured MCP servers.
// Servers that fail to connect are logged but don't prevent others.
func (r *Registry) ConnectAll(ctx context.Context, servers []config.ServerConfig, logFn func(string)) {
	for _, srv := range servers {
		toolCount, err := r.ConnectServer(ctx, srv)
		if err != nil {
			logFn(fmt.Sprintf("skip %s: %v", srv.Name, err))
			continue
		}
		logFn(fmt.Sprintf("connected %s (%d tools)", srv.Name, toolCount))
	}
}

// Tools returns all discovered tool definitions.
func (r *Registry) Tools() []llm.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var serverNames []string
	for name := range r.serverTools {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)

	var toolDefs []llm.ToolDef
	for _, name := range serverNames {
		toolDefs = append(toolDefs, r.serverTools[name]...)
	}
	return toolDefs
}

// ToolCount returns the total number of registered tools.
func (r *Registry) ToolCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, defs := range r.serverTools {
		count += len(defs)
	}
	return count
}

// ResolveToolName returns a unique exposed name for a remote MCP tool. It is
// intended for host-side integrations that know a capability by its protocol
// name while the model-facing registry remains fully namespaced.
func (r *Registry) ResolveToolName(remoteName string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, exact := r.toolMap[remoteName]; exact {
		return remoteName, true
	}
	suffix := "__" + remoteName
	match := ""
	for exposed, route := range r.toolMap {
		if route.remoteName != remoteName && !strings.HasSuffix(exposed, suffix) {
			continue
		}
		if match != "" {
			return "", false
		}
		match = exposed
	}
	return match, match != ""
}

// ServerCount returns the number of connected servers.
func (r *Registry) ServerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.clients)
}

// ServerNames returns the names of all connected servers.
func (r *Registry) ServerNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.clients))
	for name := range r.clients {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

const maxAllServerInstructionBytes = 16 * 1024

// ServerInstructions returns a deterministic, bounded snapshot of usage
// guidance supplied by connected MCP servers during initialization. Guidance
// remains server-authored data; consumers must not treat it as host policy.
func (r *Registry) ServerInstructions() []ServerInstruction {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.serverGuidance))
	for name, guidance := range r.serverGuidance {
		if strings.TrimSpace(guidance) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	remaining := maxAllServerInstructionBytes
	instructions := make([]ServerInstruction, 0, len(names))
	for _, name := range names {
		if remaining <= 0 {
			break
		}
		guidance := boundServerInstruction(r.serverGuidance[name], maxServerInstructionBytes)
		if guidance == "" {
			continue
		}
		// Keep each server's guidance atomic. A silently cut calling convention
		// is less useful than omitting that entry from this bounded snapshot.
		if len(guidance) > remaining {
			continue
		}
		instructions = append(instructions, ServerInstruction{Name: name, Text: guidance})
		remaining -= len(guidance)
	}
	return instructions
}

// FailedServers returns the list of servers that failed to connect.
func (r *Registry) FailedServers() []FailedServer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	failed := make([]FailedServer, len(r.failedServers))
	copy(failed, r.failedServers)
	return failed
}

// CallTool routes a tool call to the correct MCP server.
func (r *Registry) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	r.mu.RLock()
	route, ok := r.toolMap[name]
	r.mu.RUnlock()

	if !ok {
		return &ToolResult{
			Content: fmt.Sprintf("unknown tool: %s", name),
			IsError: true,
		}, nil
	}

	r.mu.RLock()
	timeout := r.callTimeout
	r.mu.RUnlock()
	if timeout <= 0 {
		timeout = defaultCallTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := route.client.CallTool(callCtx, route.remoteName, args)
	if err != nil && callCtx.Err() != nil {
		// Preserve the local deadline/cancellation in the error chain. Callers
		// must treat a dispatched MCP mutation as outcome-unknown, not retry it
		// as though it definitely never ran.
		return nil, fmt.Errorf("MCP tool %s ended without a receipt: %w", name, callCtx.Err())
	}
	return result, err
}

// Close shuts down all MCP server connections.
func (r *Registry) Close() {
	r.closeOnce.Do(func() {
		// Closing admission and cancelling the lifecycle happen under one lock,
		// so no goroutine can Add to lifecycleWG after Wait begins.
		r.mu.Lock()
		r.closed = true
		r.cancel()
		r.mu.Unlock()

		// Includes every health monitor and in-flight ConnectServer operation.
		r.lifecycleWG.Wait()

		r.mu.Lock()
		clients := make([]*MCPClient, 0, len(r.clients))
		for _, client := range r.clients {
			clients = append(clients, client)
		}
		r.clients = make(map[string]*MCPClient)
		r.toolMap = make(map[string]toolRoute)
		r.serverTools = make(map[string][]llm.ToolDef)
		r.serverGuidance = make(map[string]string)
		r.failedServers = nil
		r.serverConfigs = make(map[string]config.ServerConfig)
		r.mu.Unlock()

		for _, client := range clients {
			_ = r.closeMCPClient(client)
		}
	})
}

// HealthCheck pings all servers and returns their status.
//
// Connections are snapshotted under the lock, then pinged with the lock
// released: Ping performs blocking I/O, so holding the read lock across it
// would stall every writer (e.g. ConnectServer) and risk a re-entrant RLock
// deadlock when a writer is queued.
func (r *Registry) HealthCheck(ctx context.Context) []ServerStatus {
	type entry struct {
		name   string
		client *MCPClient
	}

	r.mu.RLock()
	names := make([]string, 0, len(r.clients))
	for name := range r.clients {
		names = append(names, name)
	}
	sort.Strings(names)
	snapshot := make([]entry, 0, len(names))
	for _, name := range names {
		snapshot = append(snapshot, entry{name: name, client: r.clients[name]})
	}
	failed := make([]FailedServer, len(r.failedServers))
	copy(failed, r.failedServers)
	r.mu.RUnlock()

	var results []ServerStatus
	for _, e := range snapshot {
		status := ServerStatus{Name: e.name}

		if e.client.IsConnected() {
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := e.client.Ping(pingCtx)
			cancel()

			status.Connected = err == nil
			if err != nil {
				status.LastError = err.Error()
			}
			status.LastPing = time.Now()
		}

		results = append(results, status)
	}

	// Include failed servers
	for _, f := range failed {
		results = append(results, ServerStatus{
			Name:      f.Name,
			Connected: false,
			LastError: f.Reason,
		})
	}

	return results
}

// ReconnectServer attempts to reconnect to a previously failed server.
// Returns the number of tools reconnected, or an error.
func (r *Registry) ReconnectServer(ctx context.Context, name string) (int, error) {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return 0, ErrRegistryClosed
	}
	srv, ok := r.serverConfigs[name]
	r.mu.RUnlock()

	if !ok {
		return 0, fmt.Errorf("no config found for server: %s", name)
	}
	return r.ConnectServer(ctx, srv)
}

func (r *Registry) setFailedServer(name, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.setFailedServerLocked(name, reason)
}

func (r *Registry) setFailedServerLocked(name, reason string) {
	for i := range r.failedServers {
		if r.failedServers[i].Name == name {
			r.failedServers[i].Reason = reason
			return
		}
	}
	r.failedServers = append(r.failedServers, FailedServer{Name: name, Reason: reason})
}

func (r *Registry) clearFailedServerLocked(name string) {
	var remaining []FailedServer
	for _, failed := range r.failedServers {
		if failed.Name != name {
			remaining = append(remaining, failed)
		}
	}
	r.failedServers = remaining
}

func (r *Registry) removeServerLocked(name string) {
	delete(r.clients, name)
	delete(r.serverTools, name)
	delete(r.serverGuidance, name)
	r.rebuildToolMapLocked()
}

func (r *Registry) registerConnectedServerLocked(name string, client *MCPClient, defs []llm.ToolDef) bool {
	if r.closed {
		return false
	}
	for i := range defs {
		defs[i].Name = namespacedToolName(name, defs[i].Name)
	}
	r.clients[name] = client
	r.serverTools[name] = defs
	if guidance := boundServerInstruction(client.Instructions(), maxServerInstructionBytes); guidance != "" {
		r.serverGuidance[name] = guidance
	} else {
		delete(r.serverGuidance, name)
	}
	r.clearFailedServerLocked(name)
	r.rebuildToolMapLocked()
	return true
}

func (r *Registry) rebuildToolMapLocked() {
	toolMap := make(map[string]toolRoute)
	serverNames := make([]string, 0, len(r.clients))
	for name := range r.clients {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)

	for _, name := range serverNames {
		client := r.clients[name]
		for _, def := range r.serverTools[name] {
			remoteName := strings.TrimPrefix(def.Name, name+"__")
			toolMap[def.Name] = toolRoute{client: client, remoteName: remoteName}
		}
	}
	r.toolMap = toolMap
}

func namespacedToolName(server, tool string) string {
	return server + "__" + tool
}

// MonitorConfig holds configuration for the health monitor.
type MonitorConfig struct {
	Interval    time.Duration
	MaxRetries  int
	BackoffBase time.Duration
}

var defaultMonitorConfig = MonitorConfig{
	Interval:    30 * time.Second,
	MaxRetries:  3,
	BackoffBase: 5 * time.Second,
}

// StartHealthMonitor begins registry-owned background health checking. The
// returned function cancels this monitor; Registry.Close cancels and joins it.
func (r *Registry) StartHealthMonitor(ctx context.Context, cfg MonitorConfig, logFn func(string)) context.CancelFunc {
	if cfg.Interval <= 0 {
		cfg = defaultMonitorConfig
	}
	if logFn == nil {
		logFn = func(string) {}
	}

	monitorCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		cancel()
		return cancel
	}
	stopLifecycleCancel := context.AfterFunc(r.lifecycleCtx, cancel)
	r.lifecycleWG.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.lifecycleWG.Done()
		defer stopLifecycleCancel()
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				r.healthCheckRound(monitorCtx, cfg, logFn)
			}
		}
	}()

	return cancel
}

func (r *Registry) healthCheckRound(ctx context.Context, cfg MonitorConfig, logFn func(string)) {
	statuses := r.HealthCheck(ctx)

	for _, status := range statuses {
		if status.Connected {
			continue
		}

		// Server is down, try to reconnect
		logFn(fmt.Sprintf("server %s unhealthy, attempting reconnect...", status.Name))

		for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
			backoff := cfg.BackoffBase * time.Duration(attempt)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			_, err := r.ReconnectServer(ctx, status.Name)
			if err == nil {
				logFn(fmt.Sprintf("server %s reconnected", status.Name))
				break
			}

			if attempt == cfg.MaxRetries {
				logFn(fmt.Sprintf("server %s reconnection failed after %d attempts: %v", status.Name, cfg.MaxRetries, err))
			}
		}
	}
}

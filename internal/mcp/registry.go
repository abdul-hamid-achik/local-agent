package mcp

import (
	"context"
	"fmt"
	"sort"
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

// Registry manages multiple MCP server connections and routes tool calls.
type Registry struct {
	mu            sync.RWMutex
	clients       map[string]*MCPClient
	toolMap       map[string]*MCPClient // tool name -> owning client
	serverTools   map[string][]llm.ToolDef
	failedServers []FailedServer
	serverConfigs map[string]config.ServerConfig // name -> config for reconnection
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		toolMap:       make(map[string]*MCPClient),
		clients:       make(map[string]*MCPClient),
		serverTools:   make(map[string][]llm.ToolDef),
		serverConfigs: make(map[string]config.ServerConfig),
	}
}

// connectTimeout is the per-server connection timeout.
const connectTimeout = 5 * time.Second

// ConnectServer connects a single MCP server and registers its tools.
// Returns the number of tools discovered, or an error.
func (r *Registry) ConnectServer(ctx context.Context, srv config.ServerConfig) (int, error) {
	r.mu.Lock()
	r.serverConfigs[srv.Name] = srv
	r.mu.Unlock()

	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client, err := Connect(connCtx, srv.Name, srv.Command, srv.Args, srv.Env, srv.Transport, srv.URL)
	if err != nil {
		r.setFailedServer(srv.Name, err.Error())
		return 0, fmt.Errorf("connect to %s: %w", srv.Name, err)
	}

	tools, err := client.ListTools(connCtx)
	if err != nil {
		client.Close()
		r.setFailedServer(srv.Name, err.Error())
		return 0, fmt.Errorf("%s tools: %w", srv.Name, err)
	}

	r.mu.Lock()
	if existing := r.clients[srv.Name]; existing != nil {
		r.removeServerLocked(srv.Name)
		_ = existing.Close()
	}
	serverDefs := make([]llm.ToolDef, 0, len(tools))
	for _, tool := range tools {
		serverDefs = append(serverDefs, ToLLMToolDef(tool.Name, tool.Description, tool.InputSchema))
	}
	r.registerConnectedServerLocked(srv.Name, client, serverDefs)
	r.mu.Unlock()

	return len(tools), nil
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

// FailedServers returns the list of servers that failed to connect.
func (r *Registry) FailedServers() []FailedServer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.failedServers
}

// CallTool routes a tool call to the correct MCP server.
func (r *Registry) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	r.mu.RLock()
	client, ok := r.toolMap[name]
	r.mu.RUnlock()

	if !ok {
		return &ToolResult{
			Content: fmt.Sprintf("unknown tool: %s", name),
			IsError: true,
		}, nil
	}

	return client.CallTool(ctx, name, args)
}

// Close shuts down all MCP server connections.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, c := range r.clients {
		_ = c.Close()
	}
	r.clients = make(map[string]*MCPClient)
	r.toolMap = make(map[string]*MCPClient)
	r.serverTools = make(map[string][]llm.ToolDef)
}

// HealthCheck pings all servers and returns their status.
func (r *Registry) HealthCheck(ctx context.Context) []ServerStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []ServerStatus
	for _, name := range r.ServerNames() {
		client := r.clients[name]
		status := ServerStatus{Name: name}

		if client.IsConnected() {
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := client.Ping(pingCtx)
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
	for _, failed := range r.failedServers {
		results = append(results, ServerStatus{
			Name:      failed.Name,
			Connected: false,
			LastError: failed.Reason,
		})
	}

	return results
}

// ReconnectServer attempts to reconnect to a previously failed server.
// Returns the number of tools reconnected, or an error.
func (r *Registry) ReconnectServer(ctx context.Context, name string) (int, error) {
	r.mu.RLock()
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
	r.rebuildToolMapLocked()
}

func (r *Registry) registerConnectedServerLocked(name string, client *MCPClient, defs []llm.ToolDef) {
	r.clients[name] = client
	r.serverTools[name] = defs
	r.clearFailedServerLocked(name)
	r.rebuildToolMapLocked()
}

func (r *Registry) rebuildToolMapLocked() {
	toolMap := make(map[string]*MCPClient)
	serverNames := make([]string, 0, len(r.clients))
	for name := range r.clients {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)

	for _, name := range serverNames {
		client := r.clients[name]
		for _, def := range r.serverTools[name] {
			toolMap[def.Name] = client
		}
	}
	r.toolMap = toolMap
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

// StartHealthMonitor begins background health checking.
// Call cancel on the returned context to shut down.
func (r *Registry) StartHealthMonitor(ctx context.Context, cfg MonitorConfig, logFn func(string)) context.CancelFunc {
	if cfg.Interval == 0 {
		cfg = defaultMonitorConfig
	}

	monitorCtx, cancel := context.WithCancel(ctx)

	go func() {
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

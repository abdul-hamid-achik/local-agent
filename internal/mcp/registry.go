package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/abdulachik/local-agent/internal/config"
	"github.com/abdulachik/local-agent/internal/llm"
)

// FailedServer records an MCP server that failed to connect.
type FailedServer struct {
	Name   string
	Reason string
}

// Registry manages multiple MCP server connections and routes tool calls.
type Registry struct {
	mu            sync.RWMutex
	clients       []*MCPClient
	toolMap       map[string]*MCPClient // tool name -> owning client
	toolDefs      []llm.ToolDef
	failedServers []FailedServer
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		toolMap: make(map[string]*MCPClient),
	}
}

// connectTimeout is the per-server connection timeout.
const connectTimeout = 5 * time.Second

// ConnectServer connects a single MCP server and registers its tools.
// Returns the number of tools discovered, or an error.
func (r *Registry) ConnectServer(ctx context.Context, srv config.ServerConfig) (int, error) {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client, err := Connect(connCtx, srv.Name, srv.Command, srv.Args, srv.Env, srv.Transport, srv.URL)
	if err != nil {
		r.mu.Lock()
		r.failedServers = append(r.failedServers, FailedServer{Name: srv.Name, Reason: err.Error()})
		r.mu.Unlock()
		return 0, fmt.Errorf("connect to %s: %w", srv.Name, err)
	}

	tools, err := client.ListTools(connCtx)
	if err != nil {
		client.Close()
		r.mu.Lock()
		r.failedServers = append(r.failedServers, FailedServer{Name: srv.Name, Reason: err.Error()})
		r.mu.Unlock()
		return 0, fmt.Errorf("%s tools: %w", srv.Name, err)
	}

	r.mu.Lock()
	r.clients = append(r.clients, client)
	for _, tool := range tools {
		r.toolMap[tool.Name] = client
		r.toolDefs = append(r.toolDefs, ToLLMToolDef(tool.Name, tool.Description, tool.InputSchema))
	}
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
	return r.toolDefs
}

// ToolCount returns the total number of registered tools.
func (r *Registry) ToolCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.toolDefs)
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
	names := make([]string, len(r.clients))
	for i, c := range r.clients {
		names[i] = c.Name()
	}
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
		c.Close()
	}
	r.clients = nil
	r.toolMap = make(map[string]*MCPClient)
	r.toolDefs = nil
}

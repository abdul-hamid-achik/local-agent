package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

type deadlineToolCaller struct {
	started chan struct{}
}

func (c *deadlineToolCaller) CallTool(ctx context.Context, _ string, _ map[string]any) (*ToolResult, error) {
	close(c.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()

	if r.ToolCount() != 0 {
		t.Errorf("ToolCount() = %d, want 0", r.ToolCount())
	}
	if r.ServerCount() != 0 {
		t.Errorf("ServerCount() = %d, want 0", r.ServerCount())
	}
	if tools := r.Tools(); len(tools) != 0 {
		t.Errorf("Tools() = %v, want empty", tools)
	}
}

func TestRegistry_CallTool_Unknown(t *testing.T) {
	r := NewRegistry()

	result, err := r.CallTool(context.Background(), "nonexistent_tool", nil)
	if err != nil {
		t.Fatalf("CallTool() unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("CallTool() IsError = false, want true for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("CallTool() Content = %q, want to contain 'unknown tool'", result.Content)
	}
}

func TestRegistryCallToolPreservesDispatchDeadline(t *testing.T) {
	r := NewRegistry()
	caller := &deadlineToolCaller{started: make(chan struct{})}
	r.toolMap["srv__mutate"] = toolRoute{client: caller, remoteName: "mutate"}
	r.SetCallTimeout(10 * time.Millisecond)
	_, err := r.CallTool(context.Background(), "srv__mutate", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
	select {
	case <-caller.started:
	default:
		t.Fatal("MCP backend was not dispatched")
	}
}

func TestRegistry_HealthCheck_Empty(t *testing.T) {
	r := NewRegistry()

	statuses := r.HealthCheck(context.Background())
	if len(statuses) != 0 {
		t.Errorf("HealthCheck() returned %d statuses, want 0", len(statuses))
	}
}

func TestRegistry_HealthCheck_TracksFailedServers(t *testing.T) {
	r := NewRegistry()

	// Simulate a failed server by directly adding to failedServers
	r.mu.Lock()
	r.failedServers = append(r.failedServers, FailedServer{
		Name:   "failed-server",
		Reason: "connection refused",
	})
	r.mu.Unlock()

	statuses := r.HealthCheck(context.Background())
	if len(statuses) != 1 {
		t.Fatalf("HealthCheck() returned %d statuses, want 1", len(statuses))
	}

	status := statuses[0]
	if status.Name != "failed-server" {
		t.Errorf("status.Name = %q, want 'failed-server'", status.Name)
	}
	if status.Connected {
		t.Error("status.Connected = true, want false")
	}
	if status.LastError != "connection refused" {
		t.Errorf("status.LastError = %q, want 'connection refused'", status.LastError)
	}
}

func TestRegistryFailedServersReturnsSnapshot(t *testing.T) {
	r := NewRegistry()
	t.Cleanup(r.Close)
	r.setFailedServer("down", "original")

	first := r.FailedServers()
	if len(first) != 1 {
		t.Fatalf("failed server snapshot = %#v", first)
	}
	first[0].Reason = "caller mutation"
	second := r.FailedServers()
	if len(second) != 1 || second[0].Reason != "original" {
		t.Fatalf("caller mutated registry backing state: %#v", second)
	}
}

func TestRegistry_RegisterConnectedServer_ReplacesExistingState(t *testing.T) {
	r := NewRegistry()

	first := &MCPClient{name: "demo"}
	second := &MCPClient{name: "demo"}
	firstDefs := []llm.ToolDef{{Name: "tool-a"}, {Name: "tool-b"}}
	secondDefs := []llm.ToolDef{{Name: "tool-a"}}

	r.mu.Lock()
	r.failedServers = append(r.failedServers, FailedServer{Name: "demo", Reason: "old error"})
	r.registerConnectedServerLocked("demo", first, firstDefs)
	r.registerConnectedServerLocked("demo", second, secondDefs)
	r.mu.Unlock()

	if r.ServerCount() != 1 {
		t.Fatalf("ServerCount() = %d, want 1", r.ServerCount())
	}
	if r.ToolCount() != 1 {
		t.Fatalf("ToolCount() = %d, want 1", r.ToolCount())
	}
	tools := r.Tools()
	if len(tools) != 1 || tools[0].Name != "demo__tool-a" {
		t.Fatalf("Tools() = %#v, want only demo__tool-a", tools)
	}
	if route := r.toolMap["demo__tool-a"]; route.client != second || route.remoteName != "tool-a" {
		t.Fatalf("demo__tool-a route = %#v, want client %p and remote tool-a", route, second)
	}
	if _, ok := r.toolMap["demo__tool-b"]; ok {
		t.Fatal("tool-b should be removed when the server is replaced")
	}
	if len(r.FailedServers()) != 0 {
		t.Fatalf("failed server entry should be cleared on successful registration, got %#v", r.FailedServers())
	}
}

func TestRegistryNamespacesDuplicateToolNamesByServer(t *testing.T) {
	r := NewRegistry()
	first := &MCPClient{name: "first"}
	second := &MCPClient{name: "second"}

	r.mu.Lock()
	r.registerConnectedServerLocked("first", first, []llm.ToolDef{{Name: "search"}})
	r.registerConnectedServerLocked("second", second, []llm.ToolDef{{Name: "search"}})
	r.mu.Unlock()

	firstRoute, firstOK := r.toolMap["first__search"]
	secondRoute, secondOK := r.toolMap["second__search"]
	if !firstOK || !secondOK {
		t.Fatalf("namespaced routes missing: %#v", r.toolMap)
	}
	if firstRoute.client != first || secondRoute.client != second {
		t.Fatalf("routes point at wrong servers: %#v", r.toolMap)
	}
	if firstRoute.remoteName != "search" || secondRoute.remoteName != "search" {
		t.Fatalf("remote tool names changed: %#v", r.toolMap)
	}
	if _, ok := r.ResolveToolName("search"); ok {
		t.Fatal("ambiguous remote tool resolved to an arbitrary server")
	}
	if got, ok := r.ResolveToolName("first__search"); !ok || got != "first__search" {
		t.Fatalf("exact exposed tool resolution = %q, %v", got, ok)
	}
}

func TestRegistryRejectsAmbiguousServerNamespace(t *testing.T) {
	r := NewRegistry()
	_, err := r.ConnectServer(context.Background(), config.ServerConfig{
		Name: "first__second", Command: "unused",
	})
	if err == nil || !strings.Contains(err.Error(), "reserved namespace delimiter") {
		t.Fatalf("ConnectServer error = %v, want namespace rejection", err)
	}
	if r.ServerCount() != 0 || r.ToolCount() != 0 {
		t.Fatalf("invalid server mutated registry: servers=%d tools=%d", r.ServerCount(), r.ToolCount())
	}
}

func TestRegistryCloseCancelsAndJoinsHealthMonitor(t *testing.T) {
	r := NewRegistry()
	r.mu.Lock()
	r.failedServers = append(r.failedServers, FailedServer{Name: "down", Reason: "test"})
	r.mu.Unlock()

	enteredLog := make(chan struct{})
	releaseLog := make(chan struct{})
	var enterOnce sync.Once
	r.StartHealthMonitor(context.Background(), MonitorConfig{
		Interval: time.Millisecond, MaxRetries: 0, BackoffBase: time.Millisecond,
	}, func(string) {
		enterOnce.Do(func() { close(enteredLog) })
		<-releaseLog
	})

	select {
	case <-enteredLog:
	case <-time.After(time.Second):
		t.Fatal("health monitor did not enter its owned goroutine")
	}

	closeDone := make(chan struct{})
	go func() {
		r.Close()
		close(closeDone)
	}()

	// The monitor deliberately ignores cancellation while inside logFn. Close
	// must remain blocked until that owned goroutine is released and joined.
	select {
	case <-closeDone:
		close(releaseLog)
		t.Fatal("Registry.Close returned before the health monitor exited")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseLog)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Registry.Close did not finish after the health monitor exited")
	}

	if r.ServerCount() != 0 || len(r.FailedServers()) != 0 {
		t.Fatalf("closed registry retained state: servers=%d failed=%v", r.ServerCount(), r.FailedServers())
	}
}

func TestRegistryCloseRejectsLateConnectionRegistration(t *testing.T) {
	r := NewRegistry()
	lateClient := &MCPClient{name: "late"}
	connectStarted := make(chan struct{})
	clientClosed := make(chan struct{})
	var connectorCalls atomic.Int32
	var closeCalls atomic.Int32
	var closeOnce sync.Once
	r.testConnector = func(ctx context.Context, _ config.ServerConfig) (*MCPClient, []llm.ToolDef, error) {
		connectorCalls.Add(1)
		close(connectStarted)
		<-ctx.Done()
		// Simulate a transport that races cancellation and still hands back a
		// successfully initialized client. The registry must reject and close it.
		return lateClient, []llm.ToolDef{{Name: "mutate"}}, nil
	}
	r.testCloseClient = func(client *MCPClient) error {
		if client == lateClient {
			closeCalls.Add(1)
			closeOnce.Do(func() { close(clientClosed) })
		}
		return nil
	}

	type connectResult struct {
		count int
		err   error
	}
	connectDone := make(chan connectResult, 1)
	go func() {
		count, err := r.ConnectServer(context.Background(), config.ServerConfig{Name: "late"})
		connectDone <- connectResult{count: count, err: err}
	}()
	select {
	case <-connectStarted:
	case <-time.After(time.Second):
		t.Fatal("connection did not start")
	}

	closeDone := make(chan struct{})
	go func() {
		r.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not join the in-flight connection")
	}
	result := <-connectDone
	if !errors.Is(result.err, ErrRegistryClosed) || result.count != 0 {
		t.Fatalf("late ConnectServer result = (%d, %v), want ErrRegistryClosed", result.count, result.err)
	}
	select {
	case <-clientClosed:
	default:
		t.Fatal("late client was not closed before Registry.Close returned")
	}
	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("late client close calls = %d, want 1", got)
	}
	if r.ServerCount() != 0 || r.ToolCount() != 0 || len(r.Tools()) != 0 {
		t.Fatalf("client appeared after Close: servers=%d tools=%d defs=%v", r.ServerCount(), r.ToolCount(), r.Tools())
	}

	if _, err := r.ConnectServer(context.Background(), config.ServerConfig{Name: "after-close"}); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("ConnectServer after Close error = %v", err)
	}
	if _, err := r.ReconnectServer(context.Background(), "late"); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("ReconnectServer after Close error = %v", err)
	}
	if got := connectorCalls.Load(); got != 1 {
		t.Fatalf("connector ran %d times; post-Close calls must be rejected before launch", got)
	}
}

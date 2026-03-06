package mcp

import (
	"context"
	"strings"
	"testing"
)

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

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
	"github.com/abdul-hamid-achik/local-agent/internal/skill"
)

func TestBuildBaseLoadedContextPrefersAgentsMD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("current instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("legacy instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := buildBaseLoadedContextAt(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "current instructions") {
		t.Fatalf("context missing AGENTS.md: %q", got)
	}
	if strings.Contains(got, "legacy instructions") {
		t.Fatalf("context should prefer AGENTS.md over AGENT.md: %q", got)
	}
}

func TestBuildBaseLoadedContextFallsBackToLegacyAgentMD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("legacy instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := buildBaseLoadedContextAt(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "legacy instructions" {
		t.Fatalf("context = %q, want legacy instructions", got)
	}
}

func TestBuildBaseLoadedContextNeverLoadsOutsideSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	secret := "PRIVATE-KEY-MATERIAL-MUST-NOT-ENTER-PROMPT"
	if err := os.WriteFile(outside, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	loaded, err := buildBaseLoadedContextAt(nil, dir)
	if !errors.Is(err, safeio.ErrSymlink) {
		t.Fatalf("outside AGENTS.md symlink error = %v", err)
	}
	if strings.Contains(loaded, secret) {
		t.Fatalf("outside secret entered model context: %q", loaded)
	}
}

func TestBuildHostConfigProjectionIsUsefulAndRedacted(t *testing.T) {
	cfg := &config.Config{
		SourcePath: "/xdg/local-agent/config.yaml",
		Privacy:    config.PrivacyConfig{LocalOnly: true},
	}
	agentsDir := &config.AgentsDir{
		Path:               "/home/user/.agents",
		Agents:             map[string]config.AgentProfile{"reviewer": {Name: "reviewer"}},
		Skills:             []config.SkillDef{{Name: "go"}, {Name: "docs"}},
		GlobalInstructions: "private instructions must not be copied",
	}
	servers := []config.ServerConfig{
		{
			Name:    "mcphub",
			Command: "/opt/homebrew/bin/mcphub",
			Args:    []string{"mcp", "serve", "--agent", "SECRET_ROUTE_VALUE"},
			Env:     []string{"TOKEN=SECRET_ENV_VALUE"},
		},
		{
			Name:      "remote",
			Transport: "streamable-http",
			URL:       "https://SECRET_URL_VALUE.example/mcp?token=SECRET_QUERY",
		},
	}

	projection := buildHostConfigProjection(cfg, agentsDir, servers)
	for _, want := range []string{
		"/xdg/local-agent/config.yaml",
		"/home/user/.agents",
		"profiles: 1",
		"skills: 2",
		`"mcphub" (stdio, gateway, scoped agent route)`,
		`"remote" (streamable-http)`,
		"Do not use filesystem tools",
	} {
		if !strings.Contains(projection, want) {
			t.Fatalf("projection missing %q: %s", want, projection)
		}
	}
	for _, secret := range []string{
		"SECRET_ROUTE_VALUE",
		"SECRET_ENV_VALUE",
		"SECRET_URL_VALUE",
		"SECRET_QUERY",
		"private instructions must not be copied",
	} {
		if strings.Contains(projection, secret) {
			t.Fatalf("projection leaked %q: %s", secret, projection)
		}
	}
}

func TestAppendLoadedContextKeepsProjectionSeparate(t *testing.T) {
	if got := appendLoadedContext("project instructions", "host projection"); got != "project instructions\n\nhost projection" {
		t.Fatalf("combined context = %q", got)
	}
}

func TestBuildHostConfigProjectionBoundsAndQuotesHostFields(t *testing.T) {
	servers := make([]config.ServerConfig, 0, maxHostProjectionServers+5)
	for i := 0; i < maxHostProjectionServers+5; i++ {
		servers = append(servers, config.ServerConfig{
			Name:    fmt.Sprintf("server-%02d-\n%s", i, strings.Repeat("x", maxHostProjectionNameRunes*2)),
			Command: "tool",
		})
	}
	longPath := "/" + strings.Repeat("private/", maxHostProjectionPathRunes)
	projection := buildHostConfigProjection(&config.Config{SourcePath: longPath}, &config.AgentsDir{Path: longPath}, servers)

	if strings.Contains(projection, longPath) {
		t.Fatal("projection included an unbounded host path")
	}
	if !strings.Contains(projection, "... (5 more configured endpoints)") {
		t.Fatalf("projection did not disclose bounded endpoints: %s", projection)
	}
	if strings.Contains(projection, "server-00-\n") {
		t.Fatal("server name injected a literal newline")
	}
	if !strings.Contains(projection, `server-00-\n`) {
		t.Fatalf("quoted server name missing: %s", projection)
	}
}

func TestApplyInitialAgentProfileValidationErrorsAreTransactional(t *testing.T) {
	tests := []struct {
		name      string
		agentsDir *config.AgentsDir
	}{
		{
			name:      "missing profile directory",
			agentsDir: nil,
		},
		{
			name:      "unknown profile",
			agentsDir: &config.AgentsDir{Agents: map[string]config.AgentProfile{}},
		},
		{
			name: "missing profile skill",
			agentsDir: &config.AgentsDir{Agents: map[string]config.AgentProfile{
				"requested": {
					Name:       "requested",
					Model:      "qwen3.5:4b",
					Skills:     []string{"not-installed"},
					MCPServers: []string{"new-server"},
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modelManager := llm.NewModelManager("http://localhost:11434", 4096)
			if err := modelManager.SetCurrentModel("qwen3.5:2b"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(modelManager.Close)

			ag := agent.New(modelManager, nil, 4096)
			ag.SetLoadedContext("existing context")
			ag.SetSkillContent("existing skill content")
			ag.SetMCPServerScope([]string{"existing-server"})
			skillMgr := skill.NewManager(t.TempDir())
			if err := skillMgr.LoadAll(); err != nil {
				t.Fatal(err)
			}

			beforeScope, beforeRestricted := ag.MCPServerScope()
			beforeModel := modelManager.CurrentModel()
			if err := applyInitialAgentProfile(ag, skillMgr, modelManager, tt.agentsDir, "replacement base", "requested"); err == nil {
				t.Fatal("invalid requested profile was accepted")
			}

			afterScope, afterRestricted := ag.MCPServerScope()
			if ag.LoadedContext() != "existing context" {
				t.Fatalf("loaded context changed to %q", ag.LoadedContext())
			}
			if ag.SkillContent() != "existing skill content" {
				t.Fatalf("skill content changed to %q", ag.SkillContent())
			}
			if modelManager.CurrentModel() != beforeModel {
				t.Fatalf("model changed from %q to %q", beforeModel, modelManager.CurrentModel())
			}
			if beforeRestricted != afterRestricted || !reflect.DeepEqual(beforeScope, afterScope) {
				t.Fatalf("MCP scope changed from (%v,%v) to (%v,%v)", beforeScope, beforeRestricted, afterScope, afterRestricted)
			}
		})
	}
}

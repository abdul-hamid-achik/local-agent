package main

import (
	"errors"
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

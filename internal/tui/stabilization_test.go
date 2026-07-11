package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/skill"
)

type stubRouter struct {
	selected string
	query    string
	mode     config.ModeContext
}

func (r *stubRouter) SelectModel(query string) string {
	r.query = query
	return r.selected
}

func (r *stubRouter) SelectModelForMode(query string, mode config.ModeContext) string {
	r.query = query
	r.mode = mode
	return r.selected
}

func (r *stubRouter) RecordOverride(string, string) {}

func (r *stubRouter) GetModelForCapability(capability config.ModelCapability) string {
	for _, model := range r.ListModels() {
		if model.Capability == capability {
			return model.Name
		}
	}
	return r.selected
}

func (r *stubRouter) ListModels() []config.Model {
	return config.DefaultModels()
}

func TestSendToAgent_RoutesModelPerSend(t *testing.T) {
	m := newTestModel(t)
	modelManager := llm.NewModelManager("http://localhost:11434", 4096)
	if err := modelManager.SetCurrentModel("qwen3.5:2b"); err != nil {
		t.Fatal(err)
	}

	router := &stubRouter{selected: "qwen3.5:9b"}
	m.modelManager = modelManager
	m.router = router
	m.model = "qwen3.5:2b"
	m.mode = ModeBuild

	cmd := m.sendToAgent("debug this issue across files")
	if cmd == nil {
		t.Fatal("expected sendToAgent to return a command")
	}
	if router.query != "debug this issue across files" {
		t.Fatalf("router query = %q", router.query)
	}
	if router.mode != config.ModeBuildContext {
		t.Fatalf("router mode = %v", router.mode)
	}
	if m.model != "qwen3.5:9b" {
		t.Fatalf("expected routed model to be applied, got %q", m.model)
	}
}

func TestSendToAgentKeepsPinnedModel(t *testing.T) {
	m := newTestModel(t)
	modelManager := llm.NewModelManager("http://localhost:11434", 4096)
	if err := modelManager.SetCurrentModel("qwen3.5:2b"); err != nil {
		t.Fatal(err)
	}
	router := &stubRouter{selected: "qwen3.5:4b"}
	m.modelManager = modelManager
	m.router = router
	m.model = "qwen3.5:2b"
	m.modelPinned = true

	if cmd := m.sendToAgent("debug this issue"); cmd == nil {
		t.Fatal("expected send command")
	}
	if m.model != "qwen3.5:2b" {
		t.Fatalf("pinned model changed to %q", m.model)
	}
	if router.query != "" {
		t.Fatalf("router ran for pinned model with query %q", router.query)
	}
}

func TestHandleCommandAction_SwitchAgentReappliesProfile(t *testing.T) {
	tmpDir := t.TempDir()
	if err := osWriteFile(filepath.Join(tmpDir, "skill-a.md"), "---\nname: skill-a\n---\ncontent a\n"); err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(filepath.Join(tmpDir, "skill-b.md"), "---\nname: skill-b\n---\ncontent b\n"); err != nil {
		t.Fatal(err)
	}

	skillMgr := skill.NewManager(tmpDir)
	if err := skillMgr.LoadAll(); err != nil {
		t.Fatal(err)
	}

	reg := command.NewRegistry()
	command.RegisterBuiltins(reg)
	ag := agent.New(nil, nil, 0)
	modelManager := llm.NewModelManager("http://localhost:11434", 4096)
	if err := modelManager.SetCurrentModel("qwen3.5:2b"); err != nil {
		t.Fatal(err)
	}

	m := New(ag, reg, skillMgr, nil, modelManager, nil, nil)
	m.initializing = false
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)

	agentsDir := &config.AgentsDir{
		Agents: map[string]config.AgentProfile{
			"default": {
				Name:         "default",
				Model:        "qwen3.5:2b",
				Skills:       []string{"skill-a"},
				SystemPrompt: "default prompt",
			},
			"coder": {
				Name:         "coder",
				Model:        "qwen3.5:9b",
				Skills:       []string{"skill-b"},
				SystemPrompt: "coder prompt",
			},
		},
	}

	m.SetAgentProfileSource(agentsDir, "base context", "default")
	if err := m.applyAgentProfile("default"); err != nil {
		t.Fatal(err)
	}
	m.loadedFile = "manual.md"
	m.manualLoadedContext = "manual context"
	m.syncLoadedContext()

	m.handleCommandAction(command.Result{
		Action: command.ActionSwitchAgent,
		Data:   "coder",
		Text:   "Switched.",
	})

	if m.agentProfile != "coder" {
		t.Fatalf("expected active profile coder, got %q", m.agentProfile)
	}
	if m.model != "qwen3.5:9b" {
		t.Fatalf("expected profile model to apply, got %q", m.model)
	}

	loadedContext := ag.LoadedContext()
	if !strings.Contains(loadedContext, "base context") || !strings.Contains(loadedContext, "manual context") || !strings.Contains(loadedContext, "coder prompt") {
		t.Fatalf("loaded context missing expected pieces: %q", loadedContext)
	}
	if strings.Contains(loadedContext, "default prompt") {
		t.Fatalf("old profile prompt should be removed: %q", loadedContext)
	}

	skillContent := ag.SkillContent()
	if !strings.Contains(skillContent, "### skill-b") || !strings.Contains(skillContent, "content b") {
		t.Fatalf("expected new profile skill content, got %q", skillContent)
	}
	if strings.Contains(skillContent, "### skill-a") {
		t.Fatalf("old profile skill content should be removed, got %q", skillContent)
	}
}

func TestRestoreUnprofiledSessionClearsProfileRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	if err := osWriteFile(filepath.Join(tmpDir, "profile-only.md"), "---\nname: profile-only\n---\nprofile skill\n"); err != nil {
		t.Fatal(err)
	}
	skillMgr := skill.NewManager(tmpDir)
	if err := skillMgr.LoadAll(); err != nil {
		t.Fatal(err)
	}
	ag := agent.New(nil, nil, 0)
	m := New(ag, command.NewRegistry(), skillMgr, nil, nil, nil, nil)
	m.SetAgentProfileSource(&config.AgentsDir{Agents: map[string]config.AgentProfile{
		"scoped": {
			Name: "scoped", Skills: []string{"profile-only"},
			SystemPrompt: "profile-only prompt", MCPServers: []string{"private"},
		},
	}}, "base context", "")
	if err := m.applyAgentProfile("scoped"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ag.SkillContent(), "profile skill") || !strings.Contains(ag.LoadedContext(), "profile-only prompt") {
		t.Fatal("profile runtime fixture was not activated")
	}

	if err := m.restoreSessionState(persistedSessionState{
		Version: 1, Mode: ModeBuild, ModelPinned: false, AgentProfile: "",
	}); err != nil {
		t.Fatal(err)
	}
	if m.agentProfile != "" || len(m.profileSkills) != 0 {
		t.Fatalf("profile identity survived restore: name=%q skills=%v", m.agentProfile, m.profileSkills)
	}
	if strings.Contains(ag.SkillContent(), "profile skill") || strings.Contains(ag.LoadedContext(), "profile-only prompt") {
		t.Fatalf("profile authority leaked into unprofiled session: skill=%q context=%q", ag.SkillContent(), ag.LoadedContext())
	}
	if ag.LoadedContext() != "base context" || m.modelPinned {
		t.Fatalf("base runtime not restored: context=%q pinned=%v", ag.LoadedContext(), m.modelPinned)
	}
}

func TestReActIterationsCountAsOneUserTurn(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(StreamDoneMsg{EvalCount: 10, PromptTokens: 100})
	m = updated.(*Model)
	updated, _ = m.Update(StreamDoneMsg{EvalCount: 20, PromptTokens: 150})
	m = updated.(*Model)
	if m.sessionTurnCount != 0 {
		t.Fatalf("iterations incremented turn count before completion: %d", m.sessionTurnCount)
	}
	updated, _ = m.Update(AgentDoneMsg{})
	m = updated.(*Model)
	if m.sessionTurnCount != 1 {
		t.Fatalf("completed user turn count = %d, want 1", m.sessionTurnCount)
	}
	if m.turnEvalTotal != 30 || m.turnPromptTotal != 250 {
		t.Fatalf("turn token totals = eval %d prompt %d", m.turnEvalTotal, m.turnPromptTotal)
	}
	if m.evalCount != 20 || m.promptTokens != 150 {
		t.Fatalf("latest context metrics = eval %d prompt %d", m.evalCount, m.promptTokens)
	}
}

func osWriteFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

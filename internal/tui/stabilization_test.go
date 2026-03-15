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

func osWriteFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

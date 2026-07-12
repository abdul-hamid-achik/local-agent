package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestRegistry() *Registry {
	r := NewRegistry()
	RegisterBuiltins(r)
	return r
}

func TestBuiltin_Help(t *testing.T) {
	r := newTestRegistry()
	result := r.Execute(&Context{}, "help", nil)
	if result.Action != ActionShowHelp {
		t.Errorf("help action = %d, want %d (ActionShowHelp)", result.Action, ActionShowHelp)
	}
}

func TestBuiltin_Clear(t *testing.T) {
	r := newTestRegistry()
	result := r.Execute(&Context{}, "clear", nil)
	if result.Action != ActionClear {
		t.Errorf("clear action = %d, want %d (ActionClear)", result.Action, ActionClear)
	}
	if result.Text == "" {
		t.Error("clear should have text")
	}
}

func TestBuiltin_New(t *testing.T) {
	r := newTestRegistry()
	result := r.Execute(&Context{}, "new", nil)
	if result.Action != ActionClear {
		t.Errorf("new action = %d, want %d (ActionClear)", result.Action, ActionClear)
	}
	if result.Text == "" {
		t.Error("new should have text")
	}
}

func TestBuiltin_Goal(t *testing.T) {
	r := newTestRegistry()

	tests := []struct {
		name       string
		ctx        *Context
		args       []string
		wantAction Action
		wantData   string
		wantError  bool
	}{
		{name: "opens form when absent", ctx: &Context{}, wantAction: ActionOpenGoal},
		{name: "shows configured goal", ctx: &Context{GoalConfigured: true}, wantAction: ActionShowGoal},
		{name: "prefills free-form objective", ctx: &Context{}, args: []string{"ship", "the", "release"}, wantAction: ActionOpenGoal, wantData: "ship the release"},
		{name: "new accepts objective", ctx: &Context{}, args: []string{"new", "fix", "resume"}, wantAction: ActionOpenGoal, wantData: "fix resume"},
		{name: "show", ctx: &Context{}, args: []string{"show"}, wantAction: ActionShowGoal},
		{name: "pause", ctx: &Context{}, args: []string{"pause"}, wantAction: ActionPauseGoal},
		{name: "resume", ctx: &Context{}, args: []string{"resume"}, wantAction: ActionResumeGoal},
		{name: "edit is budget-only", ctx: &Context{GoalObjective: "durable objective"}, args: []string{"edit"}, wantAction: ActionEditGoalBudget},
		{name: "drop", ctx: &Context{}, args: []string{"drop"}, wantAction: ActionDropGoal},
		{name: "drop rejects trailing input", ctx: &Context{}, args: []string{"drop", "extra"}, wantError: true},
		{name: "pause rejects trailing input", ctx: &Context{}, args: []string{"pause", "now"}, wantError: true},
		{name: "status rejects trailing input", ctx: &Context{}, args: []string{"status", "verbose"}, wantError: true},
		{name: "unknown flag fails closed", ctx: &Context{}, args: []string{"--forever"}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Execute(tt.ctx, "goal", tt.args)
			if tt.wantError {
				if result.Error == "" {
					t.Fatal("expected an error")
				}
				return
			}
			if result.Error != "" {
				t.Fatalf("unexpected error: %s", result.Error)
			}
			if result.Action != tt.wantAction || result.Data != tt.wantData {
				t.Fatalf("goal result = action %d data %q, want action %d data %q", result.Action, result.Data, tt.wantAction, tt.wantData)
			}
		})
	}

	for _, alias := range []struct {
		name       string
		args       []string
		wantAction Action
	}{
		{name: "g", args: []string{"set", "alias objective"}, wantAction: ActionOpenGoal},
		{name: "goal", args: []string{"status"}, wantAction: ActionShowGoal},
		{name: "goal", args: []string{"retry"}, wantAction: ActionResumeGoal},
		{name: "goal", args: []string{"budget"}, wantAction: ActionEditGoalBudget},
	} {
		result := r.Execute(&Context{}, alias.name, alias.args)
		if result.Error != "" || result.Action != alias.wantAction {
			t.Fatalf("/%s %v = action %d error %q, want action %d", alias.name, alias.args, result.Action, result.Error, alias.wantAction)
		}
	}
}

func TestBuiltin_Model(t *testing.T) {
	r := newTestRegistry()
	ctx := &Context{
		Model:     "qwen3.5:0.8b",
		ModelList: []string{"qwen3.5:0.8b", "qwen3.5:2b", "qwen3.5:4b", "qwen3.5:9b"},
	}

	tests := []struct {
		name       string
		args       []string
		wantAction Action
		wantData   string
		wantErr    bool
		checkText  string
	}{
		{
			name:       "no args opens model picker",
			args:       nil,
			wantAction: ActionShowModelPicker,
		},
		{
			name:       "auto resumes routing",
			args:       []string{"auto"},
			wantAction: ActionEnableAutoModel,
		},
		{
			name:      "list shows models",
			args:      []string{"list"},
			checkText: "Available models",
		},
		{
			name:       "fast switches to first",
			args:       []string{"fast"},
			wantAction: ActionSwitchModel,
			wantData:   "qwen3.5:0.8b",
		},
		{
			name:       "smart switches to last",
			args:       []string{"smart"},
			wantAction: ActionSwitchModel,
			wantData:   "qwen3.5:9b",
		},
		{
			name:       "valid name switches",
			args:       []string{"qwen3.5:2b"},
			wantAction: ActionSwitchModel,
			wantData:   "qwen3.5:2b",
		},
		{
			name:    "invalid name errors",
			args:    []string{"nonexistent"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Execute(ctx, "model", tt.args)
			if tt.wantErr {
				if result.Error == "" {
					t.Error("expected error")
				}
				return
			}
			if result.Error != "" {
				t.Errorf("unexpected error: %s", result.Error)
				return
			}
			if tt.wantAction != ActionNone && result.Action != tt.wantAction {
				t.Errorf("action = %d, want %d", result.Action, tt.wantAction)
			}
			if tt.wantData != "" && result.Data != tt.wantData {
				t.Errorf("data = %q, want %q", result.Data, tt.wantData)
			}
			if tt.checkText != "" && !strings.Contains(result.Text, tt.checkText) {
				t.Errorf("text %q does not contain %q", result.Text, tt.checkText)
			}
		})
	}
}

func TestBuiltin_Models(t *testing.T) {
	r := newTestRegistry()
	ctx := &Context{
		Model:     "qwen3.5:0.8b",
		ModelList: []string{"qwen3.5:0.8b", "qwen3.5:2b"},
	}
	result := r.Execute(ctx, "models", nil)
	if result.Action != ActionShowModelPicker {
		t.Errorf("expected ActionShowModelPicker, got %d", result.Action)
	}
}

func TestBuiltin_MigrateCheckpointsRequiresExplicitCountConfirmation(t *testing.T) {
	r := newTestRegistry()
	preview := r.Execute(&Context{}, "migrate-checkpoints", nil)
	if preview.Error != "" || preview.Action != ActionPreviewLegacyCheckpoints {
		t.Fatalf("preview result = %#v", preview)
	}

	for _, args := range [][]string{{"confirm"}, {"yes", "2"}, {"confirm", "zero"}, {"confirm", "0"}} {
		result := r.Execute(&Context{}, "migrate-checkpoints", args)
		if result.Error == "" || result.Action != ActionNone {
			t.Fatalf("invalid confirmation %v accepted: %#v", args, result)
		}
	}

	confirmed := r.Execute(&Context{}, "migrate-checkpoints", []string{"confirm", "2"})
	if confirmed.Error != "" || confirmed.Action != ActionClaimLegacyCheckpoints || confirmed.Data != "2" {
		t.Fatalf("confirmation result = %#v", confirmed)
	}
}

func TestBuiltin_LegacyStoresRequireExplicitCountConfirmation(t *testing.T) {
	r := newTestRegistry()
	tests := []struct {
		name          string
		previewAction Action
		claimAction   Action
	}{
		{name: "migrate-memory", previewAction: ActionPreviewLegacyMemory, claimAction: ActionClaimLegacyMemory},
		{name: "migrate-ice", previewAction: ActionPreviewLegacyICE, claimAction: ActionClaimLegacyICE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview := r.Execute(&Context{}, tt.name, nil)
			if preview.Error != "" || preview.Action != tt.previewAction {
				t.Fatalf("preview result = %#v", preview)
			}
			for _, args := range [][]string{{"confirm"}, {"yes", "2"}, {"confirm", "zero"}, {"confirm", "0"}} {
				result := r.Execute(&Context{}, tt.name, args)
				if result.Error == "" || result.Action != ActionNone {
					t.Fatalf("invalid confirmation %v accepted: %#v", args, result)
				}
			}
			confirmed := r.Execute(&Context{}, tt.name, []string{"confirm", "2"})
			if confirmed.Error != "" || confirmed.Action != tt.claimAction || confirmed.Data != "2" {
				t.Fatalf("confirmation result = %#v", confirmed)
			}
		})
	}
}

func TestBuiltin_Agent(t *testing.T) {
	r := newTestRegistry()

	t.Run("no args lists agents", func(t *testing.T) {
		ctx := &Context{AgentList: []string{"coder", "reviewer"}, AgentProfile: "coder"}
		result := r.Execute(ctx, "agent", nil)
		if !strings.Contains(result.Text, "Available agent profiles") {
			t.Errorf("expected agent list, got %q", result.Text)
		}
	})

	t.Run("list subcommand", func(t *testing.T) {
		ctx := &Context{AgentList: []string{"coder"}}
		result := r.Execute(ctx, "agent", []string{"list"})
		if !strings.Contains(result.Text, "Available agent profiles") {
			t.Errorf("expected agent list, got %q", result.Text)
		}
	})

	t.Run("valid switch", func(t *testing.T) {
		ctx := &Context{AgentList: []string{"coder", "reviewer"}}
		result := r.Execute(ctx, "agent", []string{"reviewer"})
		if result.Action != ActionSwitchAgent {
			t.Errorf("action = %d, want %d", result.Action, ActionSwitchAgent)
		}
		if result.Data != "reviewer" {
			t.Errorf("data = %q, want %q", result.Data, "reviewer")
		}
	})

	t.Run("invalid errors", func(t *testing.T) {
		ctx := &Context{AgentList: []string{"coder"}}
		result := r.Execute(ctx, "agent", []string{"unknown"})
		if result.Error == "" {
			t.Error("expected error for unknown agent")
		}
	})

	t.Run("no agents", func(t *testing.T) {
		ctx := &Context{AgentList: []string{}}
		result := r.Execute(ctx, "agent", nil)
		if !strings.Contains(result.Text, "No agent profiles") {
			t.Errorf("expected no agents message, got %q", result.Text)
		}
	})
}

func TestBuiltin_Load(t *testing.T) {
	r := newTestRegistry()

	t.Run("no args errors", func(t *testing.T) {
		result := r.Execute(&Context{}, "load", nil)
		if result.Error == "" {
			t.Error("expected error for no args")
		}
	})

	t.Run("valid file loads", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "test.md")
		if err := os.WriteFile(path, []byte("# Hello"), 0644); err != nil {
			t.Fatal(err)
		}
		result := r.Execute(&Context{}, "load", []string{path})
		if result.Error != "" {
			t.Errorf("unexpected error: %s", result.Error)
		}
		if result.Action != ActionLoadContext {
			t.Errorf("action = %d, want %d", result.Action, ActionLoadContext)
		}
		if result.Data != path {
			t.Errorf("data path = %q, want %q", result.Data, path)
		}
	})

	t.Run("filesystem validation is deferred to async TUI effect", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "big.md")
		data := make([]byte, 33*1024) // > 32KB
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		result := r.Execute(&Context{}, "load", []string{path})
		if result.Error != "" || result.Action != ActionLoadContext || result.Data != path {
			t.Fatalf("load handler performed I/O instead of returning an effect: %#v", result)
		}
	})

	t.Run("nonexistent path is also deferred", func(t *testing.T) {
		result := r.Execute(&Context{}, "load", []string{"/nonexistent/file.md"})
		if result.Error != "" || result.Action != ActionLoadContext {
			t.Fatalf("load handler performed synchronous path I/O: %#v", result)
		}
	})
}

func TestBuiltin_Unload(t *testing.T) {
	r := newTestRegistry()

	t.Run("no loaded file", func(t *testing.T) {
		result := r.Execute(&Context{LoadedFile: ""}, "unload", nil)
		if !strings.Contains(result.Text, "No context") {
			t.Errorf("expected 'No context' message, got %q", result.Text)
		}
	})

	t.Run("loaded file unloads", func(t *testing.T) {
		result := r.Execute(&Context{LoadedFile: "something.md"}, "unload", nil)
		if result.Action != ActionUnloadContext {
			t.Errorf("action = %d, want %d", result.Action, ActionUnloadContext)
		}
	})
}

func TestBuiltin_Skill(t *testing.T) {
	r := newTestRegistry()
	ctx := &Context{
		Skills: []SkillInfo{
			{Name: "coder", Description: "Code generation", Active: true},
			{Name: "reviewer", Description: "Code review", Active: false},
		},
	}

	t.Run("no args lists skills", func(t *testing.T) {
		result := r.Execute(ctx, "skill", nil)
		if !strings.Contains(result.Text, "Skills") {
			t.Errorf("expected skills list, got %q", result.Text)
		}
	})

	t.Run("list subcommand", func(t *testing.T) {
		result := r.Execute(ctx, "skill", []string{"list"})
		if !strings.Contains(result.Text, "Skills") {
			t.Errorf("expected skills list, got %q", result.Text)
		}
	})

	t.Run("activate", func(t *testing.T) {
		result := r.Execute(ctx, "skill", []string{"activate", "reviewer"})
		if result.Action != ActionActivateSkill {
			t.Errorf("action = %d, want %d", result.Action, ActionActivateSkill)
		}
		if result.Data != "reviewer" {
			t.Errorf("data = %q, want %q", result.Data, "reviewer")
		}
	})

	t.Run("deactivate", func(t *testing.T) {
		result := r.Execute(ctx, "skill", []string{"deactivate", "coder"})
		if result.Action != ActionDeactivateSkill {
			t.Errorf("action = %d, want %d", result.Action, ActionDeactivateSkill)
		}
		if result.Data != "coder" {
			t.Errorf("data = %q, want %q", result.Data, "coder")
		}
	})

	t.Run("unknown action errors", func(t *testing.T) {
		result := r.Execute(ctx, "skill", []string{"unknown", "foo"})
		if result.Error == "" {
			t.Error("expected error for unknown skill action")
		}
	})

	t.Run("missing name errors", func(t *testing.T) {
		result := r.Execute(ctx, "skill", []string{"activate"})
		if result.Error == "" {
			t.Error("expected error for missing skill name")
		}
	})
}

func TestBuiltin_Servers(t *testing.T) {
	r := newTestRegistry()

	t.Run("no servers", func(t *testing.T) {
		result := r.Execute(&Context{ServerNames: nil}, "servers", nil)
		if !strings.Contains(result.Text, "No MCP servers") {
			t.Errorf("expected no servers message, got %q", result.Text)
		}
	})

	t.Run("with servers", func(t *testing.T) {
		ctx := &Context{
			ServerNames: []string{"server-a", "server-b"},
			ToolCount:   10,
		}
		result := r.Execute(ctx, "servers", nil)
		if !strings.Contains(result.Text, "server-a") {
			t.Errorf("expected server-a in output, got %q", result.Text)
		}
		if !strings.Contains(result.Text, "server-b") {
			t.Errorf("expected server-b in output, got %q", result.Text)
		}
		if !strings.Contains(result.Text, "10") {
			t.Errorf("expected tool count in output, got %q", result.Text)
		}
	})
}

func TestBuiltin_ICE(t *testing.T) {
	r := newTestRegistry()

	t.Run("disabled", func(t *testing.T) {
		result := r.Execute(&Context{ICEEnabled: false}, "ice", nil)
		if !strings.Contains(result.Text, "not enabled") {
			t.Errorf("expected disabled message, got %q", result.Text)
		}
	})

	t.Run("enabled shows status", func(t *testing.T) {
		ctx := &Context{
			ICEEnabled:       true,
			ICEConversations: 5,
			ICESessionID:     "abc-123",
		}
		result := r.Execute(ctx, "ice", nil)
		if !strings.Contains(result.Text, "enabled") {
			t.Errorf("expected enabled status, got %q", result.Text)
		}
		if !strings.Contains(result.Text, "5") {
			t.Errorf("expected conversation count, got %q", result.Text)
		}
		if !strings.Contains(result.Text, "abc-123") {
			t.Errorf("expected session ID, got %q", result.Text)
		}
	})
}

func TestBuiltin_Exit(t *testing.T) {
	r := newTestRegistry()
	result := r.Execute(&Context{}, "exit", nil)
	if result.Action != ActionQuit {
		t.Errorf("exit action = %d, want %d (ActionQuit)", result.Action, ActionQuit)
	}
}

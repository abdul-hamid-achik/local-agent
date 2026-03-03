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
			name:      "no args shows current",
			args:      nil,
			checkText: "Current model: qwen3.5:0.8b",
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
	if !strings.Contains(result.Text, "Available models") {
		t.Errorf("expected models list, got %q", result.Text)
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
		// Data should be path\0content
		parts := strings.SplitN(result.Data, "\x00", 2)
		if len(parts) != 2 {
			t.Fatalf("expected path\\0content, got %q", result.Data)
		}
		if parts[0] != path {
			t.Errorf("data path = %q, want %q", parts[0], path)
		}
		if parts[1] != "# Hello" {
			t.Errorf("data content = %q, want %q", parts[1], "# Hello")
		}
	})

	t.Run("too large errors", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "big.md")
		data := make([]byte, 33*1024) // > 32KB
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		result := r.Execute(&Context{}, "load", []string{path})
		if result.Error == "" {
			t.Error("expected error for oversized file")
		}
		if !strings.Contains(result.Error, "too large") {
			t.Errorf("error = %q, want containing 'too large'", result.Error)
		}
	})

	t.Run("nonexistent errors", func(t *testing.T) {
		result := r.Execute(&Context{}, "load", []string{"/nonexistent/file.md"})
		if result.Error == "" {
			t.Error("expected error for nonexistent file")
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

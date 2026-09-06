package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
	"gopkg.in/yaml.v3"
)

func TestConfiguredNativeToolsRemainBoundedAcrossModes(t *testing.T) {
	for _, tt := range []struct {
		name, document string
		count          int
	}{
		{"small editing kit", "builtin_tools: [read, grep, edit, bash]\n", 4},
		{"explicit empty kit", "builtin_tools: []\n", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var cfg config.ToolsConfig
			if err := yaml.Unmarshal([]byte(tt.document), &cfg); err != nil {
				t.Fatal(err)
			}
			encoded, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			cfg = config.ToolsConfig{}
			if err := yaml.Unmarshal(encoded, &cfg); err != nil {
				t.Fatal(err)
			}
			a := New(nil, nil, 8192)
			if err := a.SetToolsConfig(cfg); err != nil {
				t.Fatal(err)
			}
			a.SetModeContext("normal", BuildToolPolicy())
			prefix, normal := a.modeContext()
			if !strings.Contains(prefix, "visible final text") {
				t.Fatal("interactive kit omitted the completion guidance used by headless kits")
			}
			if got := len(normal.BuiltinNames()); got != tt.count {
				t.Fatalf("normal exposes %d tools, want %d", got, tt.count)
			}
			if normal.AllowsBuiltin("remove") || normal.AllowsMemory("memory_save") {
				t.Fatal("kit exposed unrelated native tools")
			}
			if got := a.ToolAvailability().Local; got != tt.count {
				t.Fatalf("UI count %d, want %d", got, tt.count)
			}
			a.SetModeContext("plan", PlanToolPolicy())
			_, plan := a.modeContext()
			if plan.AllowsBuiltin("edit") || plan.AllowsBuiltin("bash") {
				t.Fatal("kit widened PLAN authority")
			}
			a.SetModeContext("normal", BuildToolPolicy())
			_, restored := a.modeContext()
			if len(restored.BuiltinNames()) != tt.count {
				t.Fatal("mode switch lost kit")
			}
		})
	}
}

func TestConfiguredNativeToolsBlockUnadvertisedDispatch(t *testing.T) {
	dir := t.TempDir()
	a := New(&scriptedClient{responses: [][]llm.StreamChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "withheld", Name: "write", Arguments: map[string]any{"path": "no.txt", "content": "must not be written"}}}, Done: true}},
		{{Text: "Refused.", Done: true}},
	}}, nil, 8192)
	a.SetWorkDir(dir)
	a.SetPermissionChecker(permission.NewChecker(nil, true))
	if err := a.SetToolsConfig(config.ToolsConfig{BuiltinTools: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	a.AddUserMessage("write no.txt")
	out := &outputRecorder{}
	if err := a.Run(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "no.txt")); !os.IsNotExist(err) {
		t.Fatal("withheld write reached the filesystem")
	}
	if len(out.toolResults) != 1 || !strings.Contains(out.toolResults[0], "blocked") {
		t.Fatalf("missing refusal receipt: %q", out.toolResults)
	}
}

func TestConfiguredNativeToolsValidateAndCopy(t *testing.T) {
	for _, names := range [][]string{{"unknown"}, {""}, {"read", "read"}} {
		a := New(nil, nil, 8192)
		if err := a.SetToolsConfig(config.ToolsConfig{BuiltinTools: names}); err == nil {
			t.Fatalf("accepted invalid tools %q", names)
		}
		_, policy := a.modeContext()
		if len(policy.BuiltinNames())+len(policy.MemoryNames()) != 0 {
			t.Fatal("invalid configuration left native authority open")
		}
	}
	a := New(nil, nil, 8192)
	names := []string{"read", "memory_recall"}
	if err := a.SetToolsConfig(config.ToolsConfig{BuiltinTools: names}); err != nil {
		t.Fatal(err)
	}
	names[0] = "remove"
	_, policy := a.modeContext()
	if !policy.AllowsBuiltin("read") || policy.AllowsBuiltin("remove") || !policy.AllowsMemory("memory_recall") {
		t.Fatal("caller mutation changed configured authority")
	}
	a.SetModeContext("ask", AskToolPolicy())
	_, policy = a.modeContext()
	if policy.AllowMCP {
		t.Fatal("configured tools widened MCP authority")
	}
}

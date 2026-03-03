package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdulachik/local-agent/internal/llm"
	"github.com/abdulachik/local-agent/internal/memory"
)

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name         string
		tools        []llm.ToolDef
		skillContent string
		loadedCtx    string
		memStore     *memory.Store
		iceContext   string
		contains     []string
		notContains  []string
	}{
		{
			name:       "no optional sections",
			contains:   []string{"No tools currently available.", "Current date:"},
			notContains: []string{"Active Skills", "Loaded Context", "Remembered Facts"},
		},
		{
			name: "with tools",
			tools: []llm.ToolDef{
				{Name: "test_tool", Description: "does stuff"},
			},
			contains:    []string{"test_tool", "does stuff"},
			notContains: []string{"No tools currently available."},
		},
		{
			name:         "with skills",
			skillContent: "skill content here",
			contains:     []string{"Active Skills", "skill content here"},
		},
		{
			name:      "with loaded context",
			loadedCtx: "some loaded context",
			contains:  []string{"Loaded Context", "some loaded context"},
		},
		{
			name:       "ICE overrides memory",
			iceContext: "ice assembled context",
			contains:   []string{"ice assembled context"},
			notContains: []string{"Remembered Facts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildSystemPrompt("", tt.tools, tt.skillContent, tt.loadedCtx, tt.memStore, tt.iceContext, "")
			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("buildSystemPrompt() missing %q", want)
				}
			}
			for _, notWant := range tt.notContains {
				if strings.Contains(result, notWant) {
					t.Errorf("buildSystemPrompt() should not contain %q", notWant)
				}
			}
		})
	}

	// Separate test: memStore with entries (needs temp dir for file I/O).
	t.Run("with memory store entries", func(t *testing.T) {
		store := memory.NewStore(filepath.Join(t.TempDir(), "test-memories.json"))
		_, _ = store.Save("user prefers dark mode", []string{"preference"})
		result := buildSystemPrompt("", nil, "", "", store, "", "")
		if !strings.Contains(result, "Remembered Facts") {
			t.Error("expected Remembered Facts section")
		}
		if !strings.Contains(result, "user prefers dark mode") {
			t.Error("expected memory content in prompt")
		}
	})
}

func TestBuildSystemPrompt_WithWorkDir(t *testing.T) {
	result := buildSystemPrompt("", nil, "", "", nil, "", "/home/user/myproject")
	if !strings.Contains(result, "Working directory: /home/user/myproject") {
		t.Error("expected working directory in prompt")
	}
	if !strings.Contains(result, "Environment") {
		t.Error("expected Environment section header")
	}
}

func TestBuildSystemPrompt_EmptyWorkDir(t *testing.T) {
	result := buildSystemPrompt("", nil, "", "", nil, "", "")
	if strings.Contains(result, "Working directory") {
		t.Error("should not include working directory when empty")
	}
}

func TestDetectProjectInfo_GoProject(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0o644)

	info := detectProjectInfo(dir)
	if !strings.Contains(info, "go.mod") {
		t.Errorf("expected go.mod in project info, got %q", info)
	}
	if !strings.Contains(info, "Go module") {
		t.Errorf("expected 'Go module' in project info, got %q", info)
	}
}

func TestDetectProjectInfo_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	info := detectProjectInfo(dir)
	if info != "" {
		t.Errorf("expected empty for dir with no markers, got %q", info)
	}
}

func TestBuildMemorySection(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(s *memory.Store)
		contains []string
		wantEmpty bool
	}{
		{
			name:      "empty store",
			setup:     func(s *memory.Store) {},
			wantEmpty: true,
		},
		{
			name: "store with tagged entry",
			setup: func(s *memory.Store) {
				_, _ = s.Save("likes Go", []string{"lang", "preference"})
			},
			contains: []string{"Remembered Facts", "likes Go", "[tags: lang, preference]"},
		},
		{
			name: "store with untagged entry",
			setup: func(s *memory.Store) {
				_, _ = s.Save("project uses modules", nil)
			},
			contains: []string{"Remembered Facts", "project uses modules"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := memory.NewStore(filepath.Join(t.TempDir(), "mem.json"))
			tt.setup(store)
			result := buildMemorySection(store)
			if tt.wantEmpty {
				if result != "" {
					t.Errorf("expected empty string, got %q", result)
				}
				return
			}
			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("buildMemorySection() missing %q in:\n%s", want, result)
				}
			}
		})
	}
}

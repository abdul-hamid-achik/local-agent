package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
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
			name:        "no optional sections",
			contains:    []string{"No tools currently available.", "Current date:"},
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
			name:        "ICE overrides memory",
			iceContext:  "ice assembled context",
			contains:    []string{"ice assembled context"},
			notContains: []string{"Remembered Facts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildSystemPrompt("", tt.tools, tt.skillContent, tt.loadedCtx, tt.memStore, tt.iceContext, "", "")
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
		result := buildSystemPrompt("", nil, "", "", store, "", "", "")
		if !strings.Contains(result, "Remembered Facts") {
			t.Error("expected Remembered Facts section")
		}
		if !strings.Contains(result, "user prefers dark mode") {
			t.Error("expected memory content in prompt")
		}
	})
}

func TestSimplifyToolsForSmallModelIncludesRequiredArguments(t *testing.T) {
	prompt := simplifyToolsForSmallModel([]llm.ToolDef{{
		Name:        "bash",
		Description: "Execute a shell command.",
		Parameters: map[string]any{
			"type":     "object",
			"required": []any{"timeout", "command"},
		},
	}})
	if !strings.Contains(prompt, "bash (required: command, timeout)") {
		t.Fatalf("small-model tool prompt omitted required arguments: %q", prompt)
	}
}

func TestBuildSystemPrompt_WithWorkDir(t *testing.T) {
	result := buildSystemPrompt("", nil, "", "", nil, "", "/home/user/myproject", "")
	if !strings.Contains(result, "Working directory: /home/user/myproject") {
		t.Error("expected working directory in prompt")
	}
	if !strings.Contains(result, "Environment") {
		t.Error("expected Environment section header")
	}
}

func TestBuildSystemPrompt_EmptyWorkDir(t *testing.T) {
	result := buildSystemPrompt("", nil, "", "", nil, "", "", "")
	if strings.Contains(result, "Working directory") {
		t.Error("should not include working directory when empty")
	}
}

func TestBuildSystemPrompt_WithIgnoreContent(t *testing.T) {
	ignoreContent := "- node_modules\n- *.log\n- build/"
	result := buildSystemPrompt("", nil, "", "", nil, "", "", ignoreContent)
	if !strings.Contains(result, "Ignored Paths") {
		t.Error("expected Ignored Paths section header")
	}
	if !strings.Contains(result, "node_modules") {
		t.Error("expected node_modules in ignore section")
	}
	if !strings.Contains(result, "*.log") {
		t.Error("expected *.log in ignore section")
	}
}

func TestBuildSystemPrompt_EmptyIgnoreContent(t *testing.T) {
	result := buildSystemPrompt("", nil, "", "", nil, "", "", "")
	if strings.Contains(result, "Ignored Paths") {
		t.Error("should not include Ignored Paths when content is empty")
	}
}

func TestSmallModelPromptPreservesInstructionsAndMemory(t *testing.T) {
	prompt := buildSystemPromptForModel(
		"BUILD MODE",
		nil,
		"use the project skill",
		"follow AGENTS.md",
		nil,
		"retrieved project memory",
		"/tmp/project",
		"secrets/**",
		"qwen3.5:2b",
	)

	for _, want := range []string{
		"BUILD MODE",
		"use the project skill",
		"follow AGENTS.md",
		"retrieved project memory",
		"secrets/**",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("small-model prompt missing %q", want)
		}
	}
}

func TestSystemPromptBoundsOptionalContext(t *testing.T) {
	huge := "BEGIN\n" + strings.Repeat("project-context ", 20_000) + "\nEND"
	prompt := buildSystemPromptForModelBudget(
		"BUILD MODE", nil, huge, huge, nil, huge, "/tmp/project", huge, "qwen3.5:2b", 4096,
	)
	if len([]rune(prompt)) > 12_000 {
		t.Fatalf("bounded system prompt is still excessive: %d characters", len([]rune(prompt)))
	}
	if !strings.Contains(prompt, "context characters omitted") {
		t.Fatal("bounded system prompt did not disclose omitted context")
	}
	if !strings.Contains(prompt, "Guidelines:") {
		t.Fatal("prompt truncation removed core guidelines")
	}
}

func TestIsSmallModelDoesNotMisclassifyLargerTiers(t *testing.T) {
	for _, model := range []string{"qwen3.5:0.8b", "qwen3.5:1.5b-instruct", "qwen3.5:2b"} {
		if !isSmallModel(model) {
			t.Errorf("isSmallModel(%q) = false, want true", model)
		}
	}
	for _, model := range []string{"qwen3.5:12b", "qwen3.5:32b", "gemma4:e4b", "custom"} {
		if isSmallModel(model) {
			t.Errorf("isSmallModel(%q) = true, want false", model)
		}
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

func TestGitEnvironmentProbeHonorsCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	binDir := t.TempDir()
	gitPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nsleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := runGitCommandContext(ctx, t.TempDir(), "status", "--porcelain"); got != "" {
		t.Fatalf("cancelled git probe returned %q", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancelled git probe blocked shutdown for %s", elapsed)
	}
}

func TestProjectMarkerProbeBusySlotFailsFast(t *testing.T) {
	projectInfoProbeSlots <- struct{}{}
	t.Cleanup(func() { <-projectInfoProbeSlots })
	start := time.Now()
	if got := detectProjectInfoContext(context.Background(), t.TempDir()); got != "" {
		t.Fatalf("busy project marker probe returned %q", got)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("busy project marker slot delayed a later turn for %s", elapsed)
	}
}

func TestBuildMemorySection(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(s *memory.Store)
		contains  []string
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

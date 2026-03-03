package initcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_GoProject(t *testing.T) {
	dir := t.TempDir()

	// Create a go.mod marker file.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a source directory with a file.
	if err := os.Mkdir(filepath.Join(dir, "cmd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Run(dir, Options{}); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "AGENT.md"))
	if err != nil {
		t.Fatalf("reading AGENT.md: %v", err)
	}

	content := string(data)

	// Check project type detection.
	if !strings.Contains(content, "Go") {
		t.Error("expected AGENT.md to contain 'Go' project type")
	}

	// Check directory listing includes go.mod.
	if !strings.Contains(content, "go.mod") {
		t.Error("expected AGENT.md to list go.mod")
	}

	// Check directory listing includes cmd/ subdirectory.
	if !strings.Contains(content, "cmd/") {
		t.Error("expected AGENT.md to list cmd/ directory")
	}

	// Check that main.go appears under cmd/.
	if !strings.Contains(content, "main.go") {
		t.Error("expected AGENT.md to list main.go inside cmd/")
	}

	// Check placeholder sections exist.
	for _, section := range []string{"## Build Commands", "## Architecture", "## Key Files", "## Notes"} {
		if !strings.Contains(content, section) {
			t.Errorf("expected AGENT.md to contain section %q", section)
		}
	}
}

func TestRun_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := Run(dir, Options{}); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "AGENT.md"))
	if err != nil {
		t.Fatalf("reading AGENT.md: %v", err)
	}

	content := string(data)

	// With no marker files, project type should be "Unknown".
	if !strings.Contains(content, "Unknown") {
		t.Error("expected AGENT.md to contain 'Unknown' project type for empty dir")
	}
}

func TestRun_ExistingAgentMD_NoOverwrite(t *testing.T) {
	dir := t.TempDir()

	agentPath := filepath.Join(dir, "AGENT.md")
	original := "# Original content\n"
	if err := os.WriteFile(agentPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	err := Run(dir, Options{})
	if err == nil {
		t.Fatal("expected error when AGENT.md already exists")
	}

	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}

	// Verify file was not modified.
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Error("AGENT.md was unexpectedly modified")
	}
}

func TestRun_Force(t *testing.T) {
	dir := t.TempDir()

	agentPath := filepath.Join(dir, "AGENT.md")
	if err := os.WriteFile(agentPath, []byte("# Old content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a go.mod so the new content is distinguishable.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Run(dir, Options{Force: true}); err != nil {
		t.Fatalf("Run() with Force=true returned error: %v", err)
	}

	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if strings.Contains(content, "Old content") {
		t.Error("AGENT.md should have been overwritten with Force=true")
	}
	if !strings.Contains(content, "Go") {
		t.Error("expected new AGENT.md to detect Go project type")
	}
}

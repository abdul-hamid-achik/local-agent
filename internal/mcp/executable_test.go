package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExecutableUsesLocalBinWithMinimalPATH(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(binDir, "test-mcp-server")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")

	got, err := resolveExecutable("test-mcp-server")
	if err != nil {
		t.Fatal(err)
	}
	if got != command {
		t.Fatalf("resolved command = %q, want %q", got, command)
	}
}

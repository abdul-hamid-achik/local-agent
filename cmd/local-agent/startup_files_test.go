package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
)

func TestApplyWorkspaceIgnoreFailsClosedForExistingUnsafeInput(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".agentignore"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := applyWorkspaceIgnore(agent.New(nil, nil, 0), dir); !errors.Is(err, safeio.ErrNotRegular) {
		t.Fatalf("unsafe .agentignore error = %v", err)
	}
}

func TestApplyWorkspaceIgnoreAllowsMissingPolicy(t *testing.T) {
	if err := applyWorkspaceIgnore(agent.New(nil, nil, 0), t.TempDir()); err != nil {
		t.Fatalf("missing .agentignore = %v", err)
	}
}

func TestApplyWorkspaceIgnoreRejectsOutsideSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-ignore")
	if err := os.WriteFile(outside, []byte("private/**"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".agentignore")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := applyWorkspaceIgnore(agent.New(nil, nil, 0), dir); !errors.Is(err, safeio.ErrSymlink) {
		t.Fatalf("symlinked .agentignore error = %v", err)
	}
}

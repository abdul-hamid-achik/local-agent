package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAdditionalReadRootGrantsReadsWithoutWideningWrites(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "mcphub")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(external, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(external, "docs", "secrets.md")
	if err := os.WriteFile(externalFile, []byte("canonical tavily setup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	external, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	externalFile = filepath.Join(external, "docs", "secrets.md")

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()

	if result, isErr := ag.handleRead(map[string]any{"path": externalFile}); !isErr || !strings.Contains(result, "/scope add-read") {
		t.Fatalf("external read before grant = %q, error=%v", result, isErr)
	}
	granted, err := ag.AddReadRoot(external)
	if err != nil {
		t.Fatalf("AddReadRoot: %v", err)
	}
	if granted != external {
		t.Fatalf("granted root = %q, want %q", granted, external)
	}
	if got := ag.ReadRoots(); len(got) != 1 || got[0] != external {
		t.Fatalf("ReadRoots = %#v", got)
	}

	for name, requested := range map[string]string{
		"absolute": externalFile,
		"relative": filepath.Join("..", "mcphub", "docs", "secrets.md"),
	} {
		t.Run(name, func(t *testing.T) {
			result, isErr := ag.handleRead(map[string]any{"path": requested})
			if isErr || result != "canonical tavily setup\n" {
				t.Fatalf("read = %q, error=%v", result, isErr)
			}
		})
	}
	if result, isErr := ag.handleGrep(context.Background(), map[string]any{
		"path": external, "pattern": "tavily",
	}); isErr || !strings.Contains(result, "secrets.md") {
		t.Fatalf("grep = %q, error=%v", result, isErr)
	}
	if result, isErr := ag.handleLs(context.Background(), map[string]any{"path": filepath.Join(external, "docs")}); isErr || !strings.Contains(result, "secrets.md") {
		t.Fatalf("ls = %q, error=%v", result, isErr)
	}
	if result, isErr := ag.handleCopy(map[string]any{"source": externalFile, "destination": "imported.md"}); isErr {
		t.Fatalf("copy external source into workspace = %q", result)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "imported.md")); err != nil || string(data) != "canonical tavily setup\n" {
		t.Fatalf("copied data = %q, err=%v", data, err)
	}

	for name, run := range map[string]func() (string, bool){
		"write": func() (string, bool) {
			return ag.handleWrite(map[string]any{"path": externalFile, "content": "mutated"})
		},
		"edit": func() (string, bool) {
			return ag.handleEdit(map[string]any{"path": externalFile, "patch": "@@\n-canonical\n+mutated"})
		},
		"mkdir": func() (string, bool) {
			return ag.handleMkdir(map[string]any{"path": filepath.Join(external, "new-directory")})
		},
		"remove": func() (string, bool) {
			return ag.handleRemove(map[string]any{"path": externalFile})
		},
		"copy destination": func() (string, bool) {
			return ag.handleCopy(map[string]any{"source": "imported.md", "destination": filepath.Join(external, "copy.md")})
		},
		"move destination": func() (string, bool) {
			return ag.handleMove(map[string]any{"source": "imported.md", "destination": filepath.Join(external, "moved.md")})
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, isErr := run()
			if !isErr || !strings.Contains(result, "workspace") {
				t.Fatalf("mutation = %q, error=%v", result, isErr)
			}
		})
	}
	if data, err := os.ReadFile(externalFile); err != nil || string(data) != "canonical tavily setup\n" {
		t.Fatalf("external file changed: %q, err=%v", data, err)
	}

	removed, err := ag.RemoveReadRoot(external)
	if err != nil || removed != external {
		t.Fatalf("RemoveReadRoot = %q, %v", removed, err)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": externalFile}); !isErr || !strings.Contains(result, "/scope add-read") {
		t.Fatalf("external read after revoke = %q, error=%v", result, isErr)
	}
}

func TestAdditionalReadRootEnforcesItsAgentIgnore(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, ".agentignore"), []byte("private/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(external, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(external, "private", "token.txt")
	if err := os.WriteFile(blocked, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(external, "README.md")
	if err := os.WriteFile(public, []byte("public"), 0o600); err != nil {
		t.Fatal(err)
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()
	if _, err := ag.AddReadRoot(external); err != nil {
		t.Fatal(err)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": blocked}); !isErr || !strings.Contains(result, ".agentignore") {
		t.Fatalf("ignored read = %q, error=%v", result, isErr)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": public}); isErr || result != "public" {
		t.Fatalf("public read = %q, error=%v", result, isErr)
	}
}

func TestAdditionalReadRootRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	workspace := t.TempDir()
	external := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("do not leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(external, "escape.txt")
	if err := os.Symlink(secret, alias); err != nil {
		t.Fatal(err)
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()
	if _, err := ag.AddReadRoot(external); err != nil {
		t.Fatal(err)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": alias}); !isErr || strings.Contains(result, "do not leak") {
		t.Fatalf("symlink escape read = %q, error=%v", result, isErr)
	}
}

func TestAdditionalReadRootClearCloseAndOverlapRules(t *testing.T) {
	workspace := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)

	if _, err := ag.AddReadRoot(workspace); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("workspace overlap error = %v", err)
	}
	if _, err := ag.AddReadRoot(string(filepath.Separator)); err == nil {
		t.Fatal("filesystem root was accepted")
	}
	for _, root := range []string{first, second} {
		if _, err := ag.AddReadRoot(root); err != nil {
			t.Fatalf("add %s: %v", root, err)
		}
	}
	if count, err := ag.ClearReadRoots(); err != nil || count != 2 {
		t.Fatalf("ClearReadRoots = %d, %v", count, err)
	}
	if roots := ag.ReadRoots(); len(roots) != 0 {
		t.Fatalf("roots after clear = %#v", roots)
	}

	if _, err := ag.AddReadRoot(first); err != nil {
		t.Fatal(err)
	}
	resolved, err := ag.resolveReadablePath(first)
	if err != nil {
		t.Fatal(err)
	}
	ag.Close()
	if roots := ag.ReadRoots(); len(roots) != 0 {
		t.Fatalf("roots after Close = %#v", roots)
	}
	if _, err := resolved.stat(); err == nil {
		t.Fatal("Close left os.Root usable")
	}
	if _, err := ag.AddReadRoot(second); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("AddReadRoot after Close error = %v", err)
	}
}

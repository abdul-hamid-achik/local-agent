package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestResolvePathConfinesWorkspace(t *testing.T) {
	root := t.TempDir()
	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)

	inside, err := ag.resolvePath("nested/file.txt")
	if err != nil {
		t.Fatalf("resolve inside: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inside, canonicalRoot+string(filepath.Separator)) {
		t.Fatalf("inside path %q is not under %q", inside, canonicalRoot)
	}

	for _, path := range []string{"../escape.txt", filepath.Join(filepath.Dir(root), "escape.txt")} {
		if _, err := ag.resolvePath(path); err == nil {
			t.Errorf("resolvePath(%q) allowed workspace escape", path)
		}
	}
}

func TestResolvePathRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)
	if _, err := ag.resolvePath("outside/new-file.txt"); err == nil {
		t.Fatal("symlink escape was allowed")
	}
}

func TestResolvePathEnforcesAgentIgnore(t *testing.T) {
	root := t.TempDir()
	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)
	ag.SetIgnoreContent("# private files\nsecrets/**\n!secrets/public/example.txt\n*.pem\n")

	for _, path := range []string{"secrets/token.txt", "secrets/deep/token.txt", "cert.pem", "nested/cert.pem"} {
		if _, err := ag.resolvePath(path); err == nil {
			t.Errorf("resolvePath(%q) ignored .agentignore", path)
		}
	}
	if _, err := ag.resolvePath("secrets/public/example.txt"); err != nil {
		t.Fatalf("negated ignore pattern was not restored: %v", err)
	}
	if _, err := ag.resolvePath("src/main.go"); err != nil {
		t.Fatalf("ordinary workspace path rejected: %v", err)
	}
}

func TestAgentIgnoreBlocksDescendantsOfWildcardDirectory(t *testing.T) {
	root := t.TempDir()
	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)
	ag.SetIgnoreContent("secrets/*\n!secrets/public/example.txt\n")
	if _, err := ag.resolvePath("secrets/prod/token.txt"); err == nil {
		t.Fatal("direct descendant bypassed ignored wildcard directory")
	}
	if _, err := ag.resolvePath("secrets/public/example.txt"); err != nil {
		t.Fatalf("ordered explicit negation did not restore file: %v", err)
	}
}

func TestAgentIgnoreCannotBeBypassedThroughSymlinkAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "public.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("public.txt", filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "public-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "public-dir", "token"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("public-dir", filepath.Join(root, "secrets")); err != nil {
		t.Fatal(err)
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)
	ag.SetIgnoreContent(".env\nsecrets/**\n")
	for _, requested := range []string{".env", "secrets/token"} {
		if result, isErr := ag.handleRead(map[string]any{"path": requested}); !isErr || !strings.Contains(result, "excluded by .agentignore") {
			t.Fatalf("read alias %q result=%q error=%v", requested, result, isErr)
		}
	}
	if result, isErr := ag.handleRead(map[string]any{"path": "public.txt"}); isErr || result != "secret" {
		t.Fatalf("canonical public file result=%q error=%v", result, isErr)
	}
	if result, isErr := ag.handleRemove(map[string]any{"path": "secrets/token"}); !isErr || !strings.Contains(result, "excluded by .agentignore") {
		t.Fatalf("remove through ignored alias result=%q error=%v", result, isErr)
	}
	if _, err := os.Stat(filepath.Join(root, "public-dir", "token")); err != nil {
		t.Fatalf("ignored alias mutation reached canonical target: %v", err)
	}
}

func TestDestructiveToolsRefuseWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)
	if result, isErr := ag.handleRemove(map[string]any{"path": ".", "recursive": true}); !isErr || !strings.Contains(result, "workspace root") {
		t.Fatalf("remove root result = %q, error=%v", result, isErr)
	}
	if result, isErr := ag.handleMove(map[string]any{"source": ".", "destination": "moved"}); !isErr || !strings.Contains(result, "workspace root") {
		t.Fatalf("move root result = %q, error=%v", result, isErr)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("workspace root was modified: %v", err)
	}
}

func TestRemoveSymlinkPreservesTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)
	if result, isErr := ag.handleRemove(map[string]any{"path": "link"}); isErr {
		t.Fatalf("remove symlink: %s", result)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink still exists: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target changed: data=%q err=%v", data, err)
	}
}

func TestMoveSymlinkMovesLinkNotTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)
	if result, isErr := ag.handleMove(map[string]any{"source": "link", "destination": "moved-link"}); isErr {
		t.Fatalf("move symlink: %s", result)
	}
	moved := filepath.Join(root, "moved-link")
	info, err := os.Lstat(moved)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination is not a symlink: info=%v err=%v", info, err)
	}
	if got, err := os.Readlink(moved); err != nil || got != target {
		t.Fatalf("moved link target = %q, err=%v", got, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatalf("link target changed: data=%q err=%v", data, err)
	}
}

func TestDestructivePathRejectsSymlinkedParentEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	ag := New(nil, nil, 0)
	ag.SetWorkDir(root)
	if result, isErr := ag.handleRemove(map[string]any{"path": "outside/victim.txt"}); !isErr || !strings.Contains(result, "escapes workspace") {
		t.Fatalf("symlinked parent escape result=%q error=%v", result, isErr)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("outside victim changed: data=%q err=%v", data, err)
	}
}

func TestApplyPatchPreservesUntouchedContent(t *testing.T) {
	content := "one\ntwo\nthree\nfour"
	patch := "@@ -2,2 +2,2 @@\n-two\n+TWO\n three"
	got, err := applyPatch(content, patch)
	if err != nil {
		t.Fatal(err)
	}
	if want := "one\nTWO\nthree\nfour"; got != want {
		t.Fatalf("patched content = %q, want %q", got, want)
	}
}

func TestApplyPatchRejectsStaleContext(t *testing.T) {
	_, err := applyPatch("one\ntwo\nthree", "@@ -2,1 +2,1 @@\n-stale\n+new")
	if err == nil {
		t.Fatal("expected stale patch to fail")
	}
}

func TestHandleBashUsesTurnContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command is Unix-specific")
	}
	ag := New(nil, nil, 0)
	ag.SetWorkDir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, isErr := ag.handleBash(ctx, map[string]any{"command": "sleep 5", "timeout": 10})
	if !isErr {
		t.Fatal("cancelled command unexpectedly succeeded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancelled command took %s", elapsed)
	}
}

func TestHandleBashTimeoutKillsDescendantsAndReportsUnknownOutcome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell process-group behavior is Unix-specific")
	}
	workDir := t.TempDir()
	ag := New(nil, nil, 0)
	ag.SetWorkDir(workDir)
	start := time.Now()
	result, isErr := ag.handleBash(context.Background(), map[string]any{
		"command": "touch started; (sleep 2; touch late) & wait",
		"timeout": 1,
	})
	if !isErr || !strings.Contains(result, "OUTCOME UNKNOWN:") {
		t.Fatalf("timeout result=%q error=%v", result, isErr)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("process-group cancellation returned after %s", elapsed)
	}
	if _, err := os.Stat(filepath.Join(workDir, "started")); err != nil {
		t.Fatalf("command was not dispatched: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(workDir, "late")); !os.IsNotExist(err) {
		t.Fatalf("descendant mutated after timeout: %v", err)
	}
}

func TestReadBoundedFileRejectsNonRegularFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mkfifo is Unix-specific")
	}
	workDir := t.TempDir()
	fifo := filepath.Join(workDir, "blocked")
	if err := exec.Command("mkfifo", fifo).Run(); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	ag := New(nil, nil, 0)
	ag.SetWorkDir(workDir)
	start := time.Now()
	result, isErr := ag.handleBuiltinToolWithCancellation(context.Background(), llm.ToolCall{
		Name: "read", Arguments: map[string]any{"path": "blocked"},
	}, false)
	if !isErr || !strings.Contains(result, "not a regular file") {
		t.Fatalf("special-file read result=%q error=%v", result, isErr)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("special-file rejection took %s", elapsed)
	}
}

func TestReadOnlyWorkerLimitWaitRemainsCancellable(t *testing.T) {
	ag := New(nil, nil, 0)
	ag.readOnlySlots <- struct{}{}
	t.Cleanup(func() { <-ag.readOnlySlots })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result, isErr := ag.handleBuiltinToolWithCancellation(ctx, llm.ToolCall{
		Name: "exists", Arguments: map[string]any{"path": "."},
	}, false)
	if !isErr || !strings.Contains(result, "cancelled before dispatch") {
		t.Fatalf("slot wait result=%q error=%v", result, isErr)
	}
}

func TestCappedBufferBoundsCapturedOutput(t *testing.T) {
	buffer := cappedBuffer{limit: 4}
	input := []byte("abcdefgh")
	n, err := buffer.Write(input)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(input) {
		t.Fatalf("Write returned %d, want %d so subprocess is not back-pressured", n, len(input))
	}
	if buffer.Len() != 4 {
		t.Fatalf("captured %d bytes, want 4", buffer.Len())
	}
	if !strings.Contains(buffer.String(), "truncated by host") {
		t.Fatalf("missing truncation marker: %q", buffer.String())
	}
}

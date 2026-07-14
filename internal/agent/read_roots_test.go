package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
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

func TestAdditionalReadRootLsBoundsLargeDirectoryAndHonorsIgnore(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, ".agentignore"), []byte(".agentignore\nignored-*.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1_024; index++ {
		for _, prefix := range []string{"ignored", "visible"} {
			path := filepath.Join(external, fmt.Sprintf("%s-%04d.txt", prefix, index))
			if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	ag.SetToolsConfig(config.ToolsConfig{MaxGrepResults: 7})
	t.Cleanup(ag.Close)
	if _, err := ag.AddReadRoot(external); err != nil {
		t.Fatal(err)
	}

	result, isErr := ag.handleLs(context.Background(), map[string]any{"path": external})
	if isErr {
		t.Fatalf("ls large external directory = %q", result)
	}
	lines := strings.Fields(result)
	if len(lines) != 7 {
		t.Fatalf("ls returned %d entries, want 7: %q", len(lines), result)
	}
	for index, line := range lines {
		want := fmt.Sprintf("visible-%04d.txt", index)
		if line != want {
			t.Fatalf("ls entry %d = %q, want %q; output=%q", index, line, want, result)
		}
	}
	if strings.Contains(result, "ignored-") || strings.Contains(result, ".agentignore") {
		t.Fatalf("ls exposed ignored external entries: %q", result)
	}
}

func TestAdditionalReadRootBoundedDirectoryScanHonorsCancellation(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	for index := 0; index < readDirectoryBatchSize*2; index++ {
		if err := os.WriteFile(filepath.Join(external, fmt.Sprintf("entry-%04d.txt", index)), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	t.Cleanup(ag.Close)
	if _, err := ag.AddReadRoot(external); err != nil {
		t.Fatal(err)
	}
	readable, err := ag.resolveReadablePath(external)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	seen := 0
	_, _, err = readable.readDirBounded(ctx, 5, func(os.DirEntry) bool {
		seen++
		if seen == 10 {
			cancel()
		}
		return true
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readDirBounded cancellation error = %v", err)
	}
	if seen >= readDirectoryBatchSize*2 {
		t.Fatalf("bounded scan ignored cancellation after visiting %d entries", seen)
	}
}

func TestExactReadFileGrantDoesNotAuthorizeParentOrSiblingWrites(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "external")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(external, "requested file.txt")
	sibling := filepath.Join(external, "sibling.txt")
	if err := os.WriteFile(target, []byte("requested"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("sibling"), 0o600); err != nil {
		t.Fatal(err)
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()
	inspection, err := ag.InspectReadPath(target)
	if err != nil || !inspection.External || inspection.AlreadyReadable || inspection.Kind != ReadGrantExactFile {
		t.Fatalf("inspection = %#v, %v", inspection, err)
	}
	granted, err := ag.AddReadFile(target)
	if err != nil || granted != inspection.Path {
		t.Fatalf("AddReadFile = %q, %v", granted, err)
	}
	if roots := ag.ReadRoots(); len(roots) != 0 {
		t.Fatalf("exact file widened ReadRoots: %#v", roots)
	}
	if grants := ag.ReadGrants(); len(grants) != 1 || grants[0].Path != inspection.Path || grants[0].Kind != ReadGrantExactFile {
		t.Fatalf("ReadGrants = %#v", grants)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": target}); isErr || result != "requested" {
		t.Fatalf("target read = %q, error=%v", result, isErr)
	}
	if result, isErr := ag.handleGrep(context.Background(), map[string]any{"path": target, "pattern": "request"}); isErr || !strings.Contains(result, "requested") {
		t.Fatalf("target grep = %q, error=%v", result, isErr)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": sibling}); !isErr || strings.Contains(result, "sibling\n") {
		t.Fatalf("sibling read = %q, error=%v", result, isErr)
	}
	if _, err := ag.resolveReadablePath(external); err == nil {
		t.Fatal("exact file grant authorized its parent directory")
	}
	if result, isErr := ag.handleWrite(map[string]any{"path": target, "content": "changed"}); !isErr || !strings.Contains(result, "workspace") {
		t.Fatalf("external write = %q, error=%v", result, isErr)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "requested" {
		t.Fatalf("target changed: %q, %v", data, err)
	}

	removed, err := ag.RemoveReadPath(target)
	if err != nil || removed.Kind != ReadGrantExactFile || removed.Path != inspection.Path {
		t.Fatalf("RemoveReadPath = %#v, %v", removed, err)
	}
	if _, err := ag.resolveReadablePath(target); err == nil {
		t.Fatal("removed exact file remained readable")
	}
}

func TestExactReadFileGrantCanonicalizesTildeAndSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tilde and symlink fixture uses Unix path syntax")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	target := filepath.Join(home, "notes with spaces.txt")
	alias := filepath.Join(home, "alias.txt")
	if err := os.WriteFile(target, []byte("home note"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	granted, err := ag.AddReadFile("~/alias.txt")
	if err != nil || granted != canonicalTarget {
		t.Fatalf("tilde symlink grant = %q, %v, want %q", granted, err, canonicalTarget)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": alias}); isErr || result != "home note" {
		t.Fatalf("canonical alias read = %q, error=%v", result, isErr)
	}
}

func TestExactReadFileBuiltinsAcceptLiteralTildePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tilde fixture uses Unix path syntax")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	downloads := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(downloads, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(downloads, "requested file.txt")
	if err := os.WriteFile(target, []byte("tilde content"), 0o600); err != nil {
		t.Fatal(err)
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()
	literal := "~/Downloads/requested file.txt"
	if _, err := ag.AddReadFile(literal); err != nil {
		t.Fatal(err)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": literal}); isErr || result != "tilde content" {
		t.Fatalf("tilde read = %q, error=%v", result, isErr)
	}
	if result, isErr := ag.handleExists(map[string]any{"path": literal}); isErr || !strings.Contains(result, "true:") || !strings.Contains(result, target) {
		t.Fatalf("tilde exists = %q, error=%v", result, isErr)
	}
	if result, isErr := ag.handleCopy(map[string]any{"source": literal, "destination": "imported.txt"}); isErr {
		t.Fatalf("tilde copy source = %q", result)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "imported.txt")); err != nil || string(data) != "tilde content" {
		t.Fatalf("tilde copied data = %q, %v", data, err)
	}
}

func TestExactReadFileRejectsReplacementAndClearIncludesFiles(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "target.txt")
	second := filepath.Join(external, "second.txt")
	for path, content := range map[string]string{target: "original", second: "second"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()
	if _, err := ag.AddReadFile(target); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.AddReadFile(second); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, isErr := ag.handleRead(map[string]any{"path": target}); !isErr || strings.Contains(result, "replacement") || !strings.Contains(result, "changed after authorization") {
		t.Fatalf("replacement read = %q, error=%v", result, isErr)
	}
	if count, err := ag.ClearReadRoots(); err != nil || count != 2 {
		t.Fatalf("ClearReadRoots exact files = %d, %v", count, err)
	}
	if grants := ag.ReadGrants(); len(grants) != 0 {
		t.Fatalf("grants after clear = %#v", grants)
	}
}

func TestExistingDirectoryGrantCoversExactFileWithoutSecondAuthority(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()
	if _, err := ag.AddReadRoot(external); err != nil {
		t.Fatal(err)
	}
	inspection, err := ag.InspectReadPath(target)
	if err != nil || !inspection.AlreadyReadable {
		t.Fatalf("covered file inspection = %#v, %v", inspection, err)
	}
	if _, err := ag.AddReadFile(target); err != nil {
		t.Fatal(err)
	}
	if grants := ag.ReadGrants(); len(grants) != 1 || grants[0].Kind != ReadGrantDirectory {
		t.Fatalf("covered exact file created duplicate authority: %#v", grants)
	}
}

func TestInspectReadPathRejectsImpossibleDirectoryAuthority(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	externalParent := filepath.Join(base, "external-parent")
	existing := filepath.Join(externalParent, "existing")
	for _, path := range []string{workspace, existing} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()
	if _, err := ag.InspectReadPath(base); err == nil || !strings.Contains(err.Error(), "overlaps writable workspace") {
		t.Fatalf("workspace parent inspection error = %v", err)
	}
	if _, err := ag.InspectReadPath(string(filepath.Separator)); err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("filesystem root inspection error = %v", err)
	}
	if _, err := ag.AddReadRoot(existing); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.InspectReadPath(externalParent); err == nil || !strings.Contains(err.Error(), "overlaps existing root") {
		t.Fatalf("existing-root parent inspection error = %v", err)
	}
}

func TestReadGrantLimitCountsExactFilesAndDirectorySupersedesThem(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	ag := New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	defer ag.Close()

	for index := 0; index < maxAdditionalReadAuthorities+1; index++ {
		path := filepath.Join(external, fmt.Sprintf("file-%02d.txt", index))
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := ag.AddReadFile(path)
		if index < maxAdditionalReadAuthorities && err != nil {
			t.Fatalf("grant %d: %v", index, err)
		}
		if index == maxAdditionalReadAuthorities && (err == nil || !strings.Contains(err.Error(), "authority limit")) {
			t.Fatalf("grant beyond combined limit error = %v", err)
		}
	}
	if grants := ag.ReadGrants(); len(grants) != maxAdditionalReadAuthorities {
		t.Fatalf("grants at limit = %d, want %d", len(grants), maxAdditionalReadAuthorities)
	}
	canonical, err := ag.AddReadRoot(external)
	if err != nil {
		t.Fatalf("superseding directory grant: %v", err)
	}
	grants := ag.ReadGrants()
	if len(grants) != 1 || grants[0].Kind != ReadGrantDirectory || grants[0].Path != canonical {
		t.Fatalf("superseded grants = %#v", grants)
	}
}

func TestInspectedReadGrantRejectsPathReplacementBeforeCommit(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		prepare func(string) error
		replace func(string) error
	}{
		{
			name: "exact file",
			prepare: func(path string) error {
				return os.WriteFile(path, []byte("approved"), 0o600)
			},
			replace: func(path string) error {
				if err := os.Remove(path); err != nil {
					return err
				}
				return os.WriteFile(path, []byte("replacement"), 0o600)
			},
		},
		{
			name:    "directory",
			prepare: func(path string) error { return os.Mkdir(path, 0o700) },
			replace: func(path string) error {
				moved := path + "-old"
				if err := os.Rename(path, moved); err != nil {
					return err
				}
				return os.Mkdir(path, 0o700)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := filepath.Join(base, strings.ReplaceAll(test.name, " ", "-"))
			if err := test.prepare(target); err != nil {
				t.Fatal(err)
			}
			ag := New(nil, nil, 0)
			ag.SetWorkDir(workspace)
			t.Cleanup(ag.Close)
			inspection, err := ag.InspectReadPath(target)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.replace(target); err != nil {
				t.Fatal(err)
			}
			if _, err := ag.AddInspectedReadGrant(inspection.Grant()); err == nil || !strings.Contains(err.Error(), "changed after approval preview") {
				t.Fatalf("stale inspected grant error = %v", err)
			}
			if grants := ag.ReadGrants(); len(grants) != 0 {
				t.Fatalf("replacement inherited authority: %#v", grants)
			}
		})
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

package skill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustLoadAll(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
}

func mustActivate(t *testing.T, m *Manager, name string) {
	t.Helper()
	if err := m.Activate(name); err != nil {
		t.Fatalf("activate %s: %v", name, err)
	}
}

func TestManager_LoadAll(t *testing.T) {
	dir := t.TempDir()

	// Create valid skill files.
	mustWriteFile(t, filepath.Join(dir, "greeting.md"), "---\nname: greeting\ndescription: Say hello\n---\nHello!")
	mustWriteFile(t, filepath.Join(dir, "farewell.md"), "---\nname: farewell\ndescription: Say bye\n---\nGoodbye!")

	// Create a non-.md file (should be skipped).
	mustWriteFile(t, filepath.Join(dir, "notes.txt"), "not a skill")

	// Create a subdirectory (should be skipped).
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	skills := m.All()
	if len(skills) != 2 {
		t.Fatalf("loaded %d skills, want 2", len(skills))
	}

	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["greeting"] {
		t.Error("missing 'greeting' skill")
	}
	if !names["farewell"] {
		t.Error("missing 'farewell' skill")
	}
}

func TestManager_LoadAll_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()

	// File without frontmatter uses filename as name.
	mustWriteFile(t, filepath.Join(dir, "plain.md"), "Just content, no frontmatter")

	m := NewManager(dir)
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	skills := m.All()
	if len(skills) != 1 {
		t.Fatalf("loaded %d skills, want 1", len(skills))
	}
	if skills[0].Name != "plain" {
		t.Errorf("Name = %q, want 'plain'", skills[0].Name)
	}
}

func TestManager_LoadAll_NonexistentDir(t *testing.T) {
	m := NewManager("/nonexistent/path/that/does/not/exist")
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll on nonexistent dir should not error, got: %v", err)
	}
	if len(m.All()) != 0 {
		t.Errorf("expected 0 skills from nonexistent dir, got %d", len(m.All()))
	}
}

func TestManagerLoadAllRejectsOversizedSkill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oversized.md")
	if err := os.WriteFile(path, make([]byte, maxSkillFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	if err := m.LoadAll(); !errors.Is(err, safeio.ErrTooLarge) {
		t.Fatalf("oversized skill error = %v", err)
	}
}

func TestManagerLoadAllRejectsSymlinkedSkill(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	m := NewManager(dir)
	if err := m.LoadAll(); !errors.Is(err, safeio.ErrSymlink) {
		t.Fatalf("symlinked skill error = %v", err)
	}
}

func TestManager_Activate_Deactivate(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "test.md"), "---\nname: test\n---\nTest content")

	m := NewManager(dir)
	mustLoadAll(t, m)

	t.Run("activate found", func(t *testing.T) {
		err := m.Activate("test")
		if err != nil {
			t.Fatalf("Activate: %v", err)
		}
		skill := m.All()[0]
		if !skill.Active {
			t.Error("skill should be active after Activate")
		}
	})

	t.Run("activate not found", func(t *testing.T) {
		err := m.Activate("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent skill")
		}
	})

	t.Run("deactivate found", func(t *testing.T) {
		err := m.Deactivate("test")
		if err != nil {
			t.Fatalf("Deactivate: %v", err)
		}
		skill := m.All()[0]
		if skill.Active {
			t.Error("skill should be inactive after Deactivate")
		}
	})

	t.Run("deactivate not found", func(t *testing.T) {
		err := m.Deactivate("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent skill")
		}
	})
}

func TestManager_ActiveContent(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "alpha.md"), "---\nname: alpha\n---\nAlpha content")
	mustWriteFile(t, filepath.Join(dir, "beta.md"), "---\nname: beta\n---\nBeta content")

	m := NewManager(dir)
	mustLoadAll(t, m)

	t.Run("none active returns empty", func(t *testing.T) {
		content := m.ActiveContent()
		if content != "" {
			t.Errorf("expected empty content, got %q", content)
		}
	})

	t.Run("one active returns its content", func(t *testing.T) {
		mustActivate(t, m, "alpha")
		content := m.ActiveContent()
		if content == "" {
			t.Fatal("expected non-empty content")
		}
		if !contains(content, "Alpha content") {
			t.Errorf("content missing 'Alpha content': %q", content)
		}
		if contains(content, "Beta content") {
			t.Errorf("content should not contain inactive 'Beta content': %q", content)
		}
		if err := m.Deactivate("alpha"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("multiple active returns combined", func(t *testing.T) {
		mustActivate(t, m, "alpha")
		mustActivate(t, m, "beta")
		content := m.ActiveContent()
		if !contains(content, "Alpha content") || !contains(content, "Beta content") {
			t.Errorf("combined content missing expected parts: %q", content)
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

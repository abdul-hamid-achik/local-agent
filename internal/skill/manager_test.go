package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManager_LoadAll(t *testing.T) {
	dir := t.TempDir()

	// Create valid skill files.
	os.WriteFile(filepath.Join(dir, "greeting.md"), []byte("---\nname: greeting\ndescription: Say hello\n---\nHello!"), 0o644)
	os.WriteFile(filepath.Join(dir, "farewell.md"), []byte("---\nname: farewell\ndescription: Say bye\n---\nGoodbye!"), 0o644)

	// Create a non-.md file (should be skipped).
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a skill"), 0o644)

	// Create a subdirectory (should be skipped).
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

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
	os.WriteFile(filepath.Join(dir, "plain.md"), []byte("Just content, no frontmatter"), 0o644)

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

func TestManager_Activate_Deactivate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.md"), []byte("---\nname: test\n---\nTest content"), 0o644)

	m := NewManager(dir)
	m.LoadAll()

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
	os.WriteFile(filepath.Join(dir, "alpha.md"), []byte("---\nname: alpha\n---\nAlpha content"), 0o644)
	os.WriteFile(filepath.Join(dir, "beta.md"), []byte("---\nname: beta\n---\nBeta content"), 0o644)

	m := NewManager(dir)
	m.LoadAll()

	t.Run("none active returns empty", func(t *testing.T) {
		content := m.ActiveContent()
		if content != "" {
			t.Errorf("expected empty content, got %q", content)
		}
	})

	t.Run("one active returns its content", func(t *testing.T) {
		m.Activate("alpha")
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
		m.Deactivate("alpha")
	})

	t.Run("multiple active returns combined", func(t *testing.T) {
		m.Activate("alpha")
		m.Activate("beta")
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

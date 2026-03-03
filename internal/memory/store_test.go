package memory

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore_Save_And_Count(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memories.json")

	s := NewStore(path)
	if s.Count() != 0 {
		t.Fatalf("new store Count = %d, want 0", s.Count())
	}

	id1, err := s.Save("first memory", []string{"tag1"})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if id1 != 1 {
		t.Errorf("first Save id = %d, want 1", id1)
	}
	if s.Count() != 1 {
		t.Errorf("Count after first Save = %d, want 1", s.Count())
	}

	id2, err := s.Save("second memory", []string{"tag2"})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if id2 != 2 {
		t.Errorf("second Save id = %d, want 2", id2)
	}
	if s.Count() != 2 {
		t.Errorf("Count after second Save = %d, want 2", s.Count())
	}
}

func TestStore_Recall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memories.json")
	s := NewStore(path)

	s.Save("the user prefers Go language", []string{"preference", "golang"})
	s.Save("project uses PostgreSQL database", []string{"tech", "database"})
	s.Save("user name is Alice", []string{"name"})

	t.Run("content match", func(t *testing.T) {
		results := s.Recall("Go", 10)
		if len(results) == 0 {
			t.Fatal("expected results for 'Go' query")
		}
		found := false
		for _, r := range results {
			if r.Content == "the user prefers Go language" {
				found = true
			}
		}
		if !found {
			t.Error("expected to find 'the user prefers Go language'")
		}
	})

	t.Run("tag match", func(t *testing.T) {
		results := s.Recall("golang", 10)
		if len(results) == 0 {
			t.Fatal("expected results for 'golang' tag query")
		}
		if results[0].Content != "the user prefers Go language" {
			t.Errorf("top result = %q, want 'the user prefers Go language'", results[0].Content)
		}
	})

	t.Run("combined scoring", func(t *testing.T) {
		// "database" matches both content and tag for PostgreSQL entry.
		results := s.Recall("database", 10)
		if len(results) == 0 {
			t.Fatal("expected results for 'database' query")
		}
		if results[0].Content != "project uses PostgreSQL database" {
			t.Errorf("top result = %q, want 'project uses PostgreSQL database'",
				results[0].Content)
		}
	})

	t.Run("maxResults limit", func(t *testing.T) {
		results := s.Recall("user", 1)
		if len(results) > 1 {
			t.Errorf("maxResults=1 but got %d results", len(results))
		}
	})

	t.Run("default maxResults 5 when 0", func(t *testing.T) {
		// With 3 memories, should return all 3 (default limit is 5).
		results := s.Recall("user", 0)
		if len(results) > 5 {
			t.Errorf("default maxResults should be 5, got %d results", len(results))
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		results := s.Recall("ALICE", 10)
		if len(results) == 0 {
			t.Fatal("expected case-insensitive match for 'ALICE'")
		}
		if results[0].Content != "user name is Alice" {
			t.Errorf("result = %q, want 'user name is Alice'", results[0].Content)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		results := s.Recall("xyzzyzxyz", 10)
		if len(results) != 0 {
			t.Errorf("expected no results for nonsense query, got %d", len(results))
		}
	})
}

func TestStore_Recall_TieBreakByRecency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memories.json")
	s := NewStore(path)

	// Save two memories with the same scoring potential.
	s.Save("alpha topic info", []string{"info"})
	// Small delay so LastUsed differs.
	time.Sleep(10 * time.Millisecond)
	s.Save("beta topic info", []string{"info"})

	results := s.Recall("info", 10)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// Both match tag "info" equally (+3), so more recent (beta) should come first.
	if results[0].Content != "beta topic info" {
		t.Errorf("expected more recent 'beta topic info' first, got %q", results[0].Content)
	}
}

func TestStore_Recent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memories.json")
	s := NewStore(path)

	s.Save("old memory", nil)
	time.Sleep(10 * time.Millisecond)
	s.Save("new memory", nil)

	t.Run("ordering by LastUsed", func(t *testing.T) {
		recent := s.Recent(2)
		if len(recent) != 2 {
			t.Fatalf("Recent(2) returned %d, want 2", len(recent))
		}
		if recent[0].Content != "new memory" {
			t.Errorf("first recent = %q, want 'new memory'", recent[0].Content)
		}
		if recent[1].Content != "old memory" {
			t.Errorf("second recent = %q, want 'old memory'", recent[1].Content)
		}
	})

	t.Run("limit exceeds count returns all", func(t *testing.T) {
		recent := s.Recent(100)
		if len(recent) != 2 {
			t.Errorf("Recent(100) returned %d, want 2", len(recent))
		}
	})

	t.Run("empty store", func(t *testing.T) {
		emptyPath := filepath.Join(dir, "empty.json")
		empty := NewStore(emptyPath)
		recent := empty.Recent(5)
		if recent != nil {
			t.Errorf("empty Recent should return nil, got %v", recent)
		}
	})
}

func TestStore_Persistence_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memories.json")

	s1 := NewStore(path)
	s1.Save("persistent memory", []string{"test"})
	s1.Save("another memory", []string{"test2"})

	// Create new store from same path.
	s2 := NewStore(path)
	if s2.Count() != 2 {
		t.Errorf("reloaded Count = %d, want 2", s2.Count())
	}

	// Verify data is intact.
	recent := s2.Recent(2)
	contents := map[string]bool{}
	for _, m := range recent {
		contents[m.Content] = true
	}
	if !contents["persistent memory"] {
		t.Error("missing 'persistent memory' after reload")
	}
	if !contents["another memory"] {
		t.Error("missing 'another memory' after reload")
	}

	// Verify IDs continue.
	id, err := s2.Save("third", nil)
	if err != nil {
		t.Fatalf("Save after reload: %v", err)
	}
	if id != 3 {
		t.Errorf("continued id = %d, want 3", id)
	}
}

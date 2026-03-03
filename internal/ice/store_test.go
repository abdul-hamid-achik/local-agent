package ice

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32
		tol  float32
	}{
		{
			name: "identical vectors",
			a:    []float32{1, 2, 3},
			b:    []float32{1, 2, 3},
			want: 1.0,
			tol:  1e-6,
		},
		{
			name: "orthogonal vectors",
			a:    []float32{1, 0},
			b:    []float32{0, 1},
			want: 0.0,
			tol:  1e-6,
		},
		{
			name: "opposite vectors",
			a:    []float32{1, 0},
			b:    []float32{-1, 0},
			want: -1.0,
			tol:  1e-6,
		},
		{
			name: "different lengths returns 0",
			a:    []float32{1, 0},
			b:    []float32{1, 0, 0},
			want: 0,
			tol:  0,
		},
		{
			name: "zero vector returns 0",
			a:    []float32{0, 0},
			b:    []float32{1, 1},
			want: 0,
			tol:  0,
		},
		{
			name: "known value with tolerance",
			a:    []float32{1, 1},
			b:    []float32{1, 0},
			// 1/(sqrt(2)*1) ≈ 0.7071
			want: float32(1.0 / math.Sqrt(2)),
			tol:  1e-4,
		},
		{
			name: "empty vectors",
			a:    []float32{},
			b:    []float32{},
			want: 0,
			tol:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.tol {
				t.Errorf("cosineSimilarity(%v, %v) = %f, want %f (±%f)",
					tt.a, tt.b, got, tt.want, tt.tol)
			}
		})
	}
}

func TestStore_Add_And_Count(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")

	s := NewStore(path)

	if s.Count() != 0 {
		t.Fatalf("new store Count = %d, want 0", s.Count())
	}

	id1, err := s.Add("sess1", "user", "hello", []float32{1, 0, 0}, 0)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if id1 != 1 {
		t.Errorf("first Add returned id=%d, want 1", id1)
	}
	if s.Count() != 1 {
		t.Errorf("Count after first Add = %d, want 1", s.Count())
	}

	id2, err := s.Add("sess1", "assistant", "world", []float32{0, 1, 0}, 1)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if id2 != 2 {
		t.Errorf("second Add returned id=%d, want 2", id2)
	}
	if s.Count() != 2 {
		t.Errorf("Count after second Add = %d, want 2", s.Count())
	}
}

func TestStore_Search(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	s := NewStore(path)

	// Add entries with known embeddings.
	s.Add("sess1", "user", "entry A", []float32{1, 0, 0}, 0)
	s.Add("sess1", "user", "entry B", []float32{0, 1, 0}, 1)
	s.Add("sess2", "user", "entry C", []float32{0.9, 0.1, 0}, 0)
	s.Add("sess2", "user", "entry D", []float32{0, 0, 1}, 1) // orthogonal to query

	t.Run("similarity filtering and sorting", func(t *testing.T) {
		// Query similar to entries A and C, exclude no session.
		results := s.Search([]float32{1, 0, 0}, "", 10)
		if len(results) == 0 {
			t.Fatal("expected results, got 0")
		}
		// Entry A should be highest (identical to query).
		if results[0].Entry.Content != "entry A" {
			t.Errorf("top result = %q, want 'entry A'", results[0].Entry.Content)
		}
	})

	t.Run("session exclusion", func(t *testing.T) {
		results := s.Search([]float32{1, 0, 0}, "sess1", 10)
		for _, r := range results {
			if r.Entry.SessionID == "sess1" {
				t.Errorf("excluded session sess1 should not appear in results")
			}
		}
	})

	t.Run("min threshold 0.3", func(t *testing.T) {
		// Entry D: [0,0,1] is orthogonal to [1,0,0] → similarity 0.
		results := s.Search([]float32{1, 0, 0}, "", 10)
		for _, r := range results {
			if r.Score < minSimilarityThreshold {
				t.Errorf("result %q has score %f below threshold %f",
					r.Entry.Content, r.Score, minSimilarityThreshold)
			}
		}
	})

	t.Run("topK limit", func(t *testing.T) {
		results := s.Search([]float32{1, 0, 0}, "", 1)
		if len(results) > 1 {
			t.Errorf("topK=1 but got %d results", len(results))
		}
	})

	t.Run("empty store returns nil", func(t *testing.T) {
		emptyPath := filepath.Join(dir, "empty.json")
		empty := NewStore(emptyPath)
		results := empty.Search([]float32{1, 0}, "", 5)
		if results != nil {
			t.Errorf("empty store search should return nil, got %v", results)
		}
	})

	t.Run("empty query embedding returns nil", func(t *testing.T) {
		results := s.Search([]float32{}, "", 5)
		if results != nil {
			t.Errorf("empty query should return nil, got %v", results)
		}
	})
}

func TestStore_Flush_Persistence(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "store.json")

		s1 := NewStore(path)
		s1.Add("sess1", "user", "hello", []float32{1, 0}, 0)
		s1.Add("sess1", "assistant", "world", []float32{0, 1}, 1)

		if err := s1.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		// Reload from same path.
		s2 := NewStore(path)
		if s2.Count() != 2 {
			t.Errorf("reloaded store Count = %d, want 2", s2.Count())
		}
	})

	t.Run("corrupt JSON recovery", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "store.json")

		// Write corrupt JSON.
		os.WriteFile(path, []byte("not valid json{{{"), 0o644)

		s := NewStore(path)
		if s.Count() != 0 {
			t.Errorf("corrupt store Count = %d, want 0", s.Count())
		}
	})

	t.Run("nextID restoration", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "store.json")

		s1 := NewStore(path)
		s1.Add("sess1", "user", "first", []float32{1}, 0)
		s1.Add("sess1", "user", "second", []float32{1}, 1)
		s1.Flush()

		s2 := NewStore(path)
		id, _ := s2.Add("sess1", "user", "third", []float32{1}, 2)
		if id != 3 {
			t.Errorf("continued id = %d, want 3", id)
		}
	})
}

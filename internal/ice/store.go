package ice

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// timeNow is a variable for testing.
var timeNow = time.Now

const minSimilarityThreshold = 0.3

// Store is a flat-file vector store for conversation history.
// It holds all entries in memory and persists to a JSON file.
type Store struct {
	mu      sync.Mutex
	path    string
	entries []ConversationEntry
	nextID  int
	dirty   bool
}

// NewStore loads an existing store from path or creates an empty one.
func NewStore(path string) *Store {
	s := &Store{path: path}
	s.load()
	return s
}

// Add appends a new conversation entry and returns its ID.
func (s *Store) Add(sessionID, role, content string, embedding []float32, turnIndex int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	entry := ConversationEntry{
		ID:        s.nextID,
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Embedding: embedding,
		TurnIndex: turnIndex,
	}
	// Use a zero-value check to set CreatedAt (avoids importing time in every call site).
	entry.CreatedAt = timeNow()
	s.entries = append(s.entries, entry)
	s.dirty = true
	return s.nextID, nil
}

// Search returns the top-K entries most similar to queryEmbedding.
// Entries from excludeSession are skipped. Results are sorted by score descending.
func (s *Store) Search(queryEmbedding []float32, excludeSession string, topK int) []ScoredEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(queryEmbedding) == 0 || len(s.entries) == 0 {
		return nil
	}

	var scored []ScoredEntry
	for _, e := range s.entries {
		if e.SessionID == excludeSession {
			continue
		}
		if len(e.Embedding) == 0 {
			continue
		}
		sim := cosineSimilarity(queryEmbedding, e.Embedding)
		if sim >= minSimilarityThreshold {
			scored = append(scored, ScoredEntry{Entry: e, Score: sim})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

// Flush persists any pending changes to disk.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}
	return s.persist()
}

// Count returns the total number of stored entries.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// load reads entries from the JSON file.
func (s *Store) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return // File doesn't exist yet.
	}

	var entries []ConversationEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return // Corrupt file, start empty.
	}

	s.entries = entries
	for _, e := range s.entries {
		if e.ID > s.nextID {
			s.nextID = e.ID
		}
	}
}

// persist writes all entries to the JSON file.
func (s *Store) persist() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create ice store dir: %w", err)
	}

	data, err := json.Marshal(s.entries)
	if err != nil {
		return fmt.Errorf("marshal ice store: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write ice store: %w", err)
	}

	s.dirty = false
	return nil
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

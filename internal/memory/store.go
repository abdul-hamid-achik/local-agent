package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory represents a single persisted memory entry.
type Memory struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

// Store manages persistent memories backed by a JSON file.
type Store struct {
	mu       sync.Mutex
	path     string
	memories []Memory
	nextID   int
}

// NewStore creates a new Store. If path is empty, uses the default
// ~/.config/local-agent/memories.json location.
func NewStore(path string) *Store {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		path = filepath.Join(home, ".config", "local-agent", "memories.json")
	}

	s := &Store{path: path}
	s.load()
	return s
}

// Save persists a new memory with the given content and tags.
func (s *Store) Save(content string, tags []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	mem := Memory{
		ID:        s.nextID,
		Content:   content,
		Tags:      tags,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
	}
	s.memories = append(s.memories, mem)

	if err := s.persist(); err != nil {
		return 0, err
	}
	return mem.ID, nil
}

// Recall searches memories by keyword/tag matching and returns up to maxResults.
func (s *Store) Recall(query string, maxResults int) []Memory {
	s.mu.Lock()
	defer s.mu.Unlock()

	if maxResults <= 0 {
		maxResults = 5
	}

	queryLower := strings.ToLower(query)
	words := strings.Fields(queryLower)

	type scored struct {
		mem   Memory
		score int
	}

	var results []scored
	for i := range s.memories {
		mem := s.memories[i]
		score := 0
		contentLower := strings.ToLower(mem.Content)

		// Score by content word matches.
		for _, w := range words {
			if strings.Contains(contentLower, w) {
				score += 2
			}
		}

		// Score by tag matches.
		for _, tag := range mem.Tags {
			tagLower := strings.ToLower(tag)
			for _, w := range words {
				if strings.Contains(tagLower, w) {
					score += 3
				}
			}
		}

		if score > 0 {
			results = append(results, scored{mem: mem, score: score})
		}
	}

	// Sort by score descending, then by recency.
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].mem.LastUsed.After(results[j].mem.LastUsed)
	})

	if len(results) > maxResults {
		results = results[:maxResults]
	}

	// Update LastUsed for returned memories.
	now := time.Now()
	out := make([]Memory, len(results))
	for i, r := range results {
		out[i] = r.mem
		// Update the original memory's LastUsed.
		for j := range s.memories {
			if s.memories[j].ID == r.mem.ID {
				s.memories[j].LastUsed = now
				break
			}
		}
	}

	// Persist updated LastUsed times (best-effort).
	_ = s.persist()

	return out
}

// Recent returns the N most recently used memories.
func (s *Store) Recent(n int) []Memory {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.memories) == 0 {
		return nil
	}

	// Sort by LastUsed descending.
	sorted := make([]Memory, len(s.memories))
	copy(sorted, s.memories)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LastUsed.After(sorted[j].LastUsed)
	})

	if n > len(sorted) {
		n = len(sorted)
	}
	return sorted[:n]
}

// Count returns the total number of stored memories.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.memories)
}

// Delete removes a memory by ID. Returns true if found and deleted.
func (s *Store) Delete(id int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, mem := range s.memories {
		if mem.ID == id {
			s.memories = append(s.memories[:i], s.memories[i+1:]...)
			return true, s.persist()
		}
	}
	return false, nil
}

// DeleteByTag removes all memories containing a specific tag.
func (s *Store) DeleteByTag(tag string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tagLower := strings.ToLower(tag)
	var remaining []Memory
	deleted := 0

	for _, mem := range s.memories {
		found := false
		for _, t := range mem.Tags {
			if strings.ToLower(t) == tagLower {
				found = true
				break
			}
		}
		if found {
			deleted++
		} else {
			remaining = append(remaining, mem)
		}
	}

	s.memories = remaining
	if deleted > 0 {
		return deleted, s.persist()
	}
	return deleted, nil
}

// Update modifies an existing memory's content and/or tags.
// If content is empty, the existing content is preserved.
// If tags is nil, the existing tags are preserved.
func (s *Store) Update(id int, content string, tags []string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, mem := range s.memories {
		if mem.ID == id {
			if content != "" {
				s.memories[i].Content = content
			}
			if tags != nil {
				s.memories[i].Tags = tags
			}
			s.memories[i].LastUsed = time.Now()
			return true, s.persist()
		}
	}
	return false, nil
}

// Prune removes memories older than the specified duration.
func (s *Store) Prune(olderThan time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-olderThan)
	var remaining []Memory
	deleted := 0

	for _, mem := range s.memories {
		if mem.CreatedAt.Before(cutoff) {
			deleted++
		} else {
			remaining = append(remaining, mem)
		}
	}

	s.memories = remaining
	if deleted > 0 {
		return deleted, s.persist()
	}
	return deleted, nil
}

// Get retrieves a single memory by ID.
func (s *Store) Get(id int) (Memory, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, mem := range s.memories {
		if mem.ID == id {
			return mem, true
		}
	}
	return Memory{}, false
}

// load reads memories from the JSON file.
func (s *Store) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return // File doesn't exist yet, start empty.
	}

	var memories []Memory
	if err := json.Unmarshal(data, &memories); err != nil {
		return // Corrupt file, start empty.
	}

	s.memories = memories

	// Find max ID for nextID.
	for _, m := range s.memories {
		if m.ID > s.nextID {
			s.nextID = m.ID
		}
	}
}

// persist writes all memories to the JSON file.
func (s *Store) persist() error {
	// Ensure directory exists.
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create memory dir: %w", err)
	}

	data, err := json.MarshalIndent(s.memories, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal memories: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write memories: %w", err)
	}

	return nil
}

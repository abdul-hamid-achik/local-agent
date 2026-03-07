package tui

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// SearchState holds the state for conversation search.
type SearchState struct {
	Input       textinput.Model
	Results     []SearchResult
	Index       int
	Active      bool
	CaseSensitive bool
}

// SearchResult represents a single search match.
type SearchResult struct {
	EntryIndex int
	LineNum    int
	Content    string
	Start      int
	End        int
}

// SearchStyles holds styling for search UI.
type SearchStyles struct {
	Input     lipgloss.Style
	Match     lipgloss.Style
	Result    lipgloss.Style
	Selected  lipgloss.Style
	Label     lipgloss.Style
	Hint      lipgloss.Style
}

// DefaultSearchStyles returns default styles.
func DefaultSearchStyles(isDark bool) SearchStyles {
	if isDark {
		return SearchStyles{
			Input:    lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0")),
			Match:    lipgloss.NewStyle().Background(lipgloss.Color("#4c566a")).Foreground(lipgloss.Color("#eceff4")),
			Result:   lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			Selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#88c0d0")),
			Label:    lipgloss.NewStyle().Foreground(lipgloss.Color("#81a1c1")),
			Hint:     lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		}
	}
	return SearchStyles{
		Input:    lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f8f")),
		Match:    lipgloss.NewStyle().Background(lipgloss.Color("#d8dee9")).Foreground(lipgloss.Color("#2e3440")),
		Result:   lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4f8f8f")),
		Label:    lipgloss.NewStyle().Foreground(lipgloss.Color("#5e81ac")),
		Hint:     lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
	}
}

// NewSearchState creates a new search state.
func NewSearchState() *SearchState {
	ti := textinput.New()
	ti.Placeholder = "Search conversation..."
	ti.Focus()
	ti.CharLimit = 256

	return &SearchState{
		Input:   ti,
		Results: nil,
		Index:   0,
		Active:  false,
	}
}

// Activate enables search mode.
func (s *SearchState) Activate() {
	s.Active = true
	s.Input.Focus()
}

// Deactivate disables search mode.
func (s *SearchState) Deactivate() {
	s.Active = false
	s.Input.Blur()
	s.Results = nil
	s.Index = 0
}

// Search performs a search across chat entries.
func (s *SearchState) Search(entries []ChatEntry, query string) {
	s.Results = nil
	s.Index = 0

	if query == "" {
		return
	}

	for entryIdx, entry := range entries {
		content := entry.Content
		if content == "" {
			continue
		}

		// Simple case-insensitive search
		searchQuery := query
		if !s.CaseSensitive {
			searchQuery = toLower(query)
			content = toLower(content)
		}

		start := 0
		for {
			idx := indexOf(content, searchQuery, start)
			if idx == -1 {
				break
			}

			// Get surrounding context (40 chars before and after)
			entryContent := entries[entryIdx].Content
			ctxStart := idx - 40
			if ctxStart < 0 {
				ctxStart = 0
			}
			ctxEnd := idx + len(query) + 40
			if ctxEnd > len(entryContent) {
				ctxEnd = len(entryContent)
			}

			context := entryContent[ctxStart:ctxEnd]
			if ctxStart > 0 {
				context = "..." + context
			}
			if ctxEnd < len(entryContent) {
				context = context + "..."
			}

			s.Results = append(s.Results, SearchResult{
				EntryIndex: entryIdx,
				LineNum:    countNewlines(entryContent[:idx]),
				Content:    context,
				Start:      idx,
				End:        idx + len(query),
			})

			start = idx + len(query)
		}
	}
}

// NextResult moves to the next search result.
func (s *SearchState) NextResult() {
	if len(s.Results) == 0 {
		return
	}
	s.Index = (s.Index + 1) % len(s.Results)
}

// PrevResult moves to the previous search result.
func (s *SearchState) PrevResult() {
	if len(s.Results) == 0 {
		return
	}
	s.Index--
	if s.Index < 0 {
		s.Index = len(s.Results) - 1
	}
}

// CurrentResult returns the currently selected result.
func (s *SearchState) CurrentResult() *SearchResult {
	if len(s.Results) == 0 || s.Index >= len(s.Results) {
		return nil
	}
	return &s.Results[s.Index]
}

// HasResults returns true if there are search results.
func (s *SearchState) HasResults() bool {
	return len(s.Results) > 0
}

// Helper functions to avoid import conflicts.
func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}

func indexOf(s, substr string, start int) int {
	if start >= len(s) {
		return -1
	}
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func countNewlines(s string) int {
	count := 0
	for _, c := range s {
		if c == '\n' {
			count++
		}
	}
	return count
}

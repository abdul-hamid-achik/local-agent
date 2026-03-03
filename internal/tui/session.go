package tui

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// SessionListItem represents a session in the list.
type SessionListItem struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}

// SessionNote represents a full session note from noted.
type SessionNote struct {
	ID      int      `json:"id"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// notedAvailable checks if the noted CLI is available.
func notedAvailable() bool {
	_, err := exec.LookPath("noted")
	return err == nil
}

// createSessionNote creates a new session note via noted CLI.
func createSessionNote(timestamp string) (int, error) {
	title := fmt.Sprintf("local-agent session %s", timestamp)
	cmd := exec.Command("noted", "add", "-t", title, "-c", "(session in progress)", "--tags", "local-agent,session", "--json")
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("noted add: %w", err)
	}

	var result struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0, fmt.Errorf("parse noted output: %w", err)
	}
	return result.ID, nil
}

// updateSessionNote updates an existing session note's content.
func updateSessionNote(id int, content string) error {
	cmd := exec.Command("noted", "edit", strconv.Itoa(id), "-c", content)
	return cmd.Run()
}

// listSessions lists recent session notes.
func listSessions(limit int) ([]SessionListItem, error) {
	cmd := exec.Command("noted", "list", "--tag", "session", "--json", "-n", strconv.Itoa(limit))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("noted list: %w", err)
	}

	var sessions []SessionListItem
	if err := json.Unmarshal(out, &sessions); err != nil {
		return nil, fmt.Errorf("parse noted output: %w", err)
	}
	return sessions, nil
}

// loadSession loads a full session note by ID.
func loadSession(id int) (*SessionNote, error) {
	cmd := exec.Command("noted", "show", strconv.Itoa(id), "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("noted show: %w", err)
	}

	var note SessionNote
	if err := json.Unmarshal(out, &note); err != nil {
		return nil, fmt.Errorf("parse noted output: %w", err)
	}
	return &note, nil
}

// serializeEntries converts chat entries to markdown for storage.
func serializeEntries(entries []ChatEntry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.Kind {
		case "user":
			b.WriteString("## User\n\n")
			b.WriteString(e.Content)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("## Assistant\n\n")
			b.WriteString(e.Content)
			b.WriteString("\n\n")
		case "system":
			b.WriteString("## System\n\n")
			b.WriteString(e.Content)
			b.WriteString("\n\n")
		case "error":
			b.WriteString("## Error\n\n")
			b.WriteString(e.Content)
			b.WriteString("\n\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// deserializeEntries parses markdown back into chat entries.
func deserializeEntries(content string) []ChatEntry {
	if content == "" {
		return nil
	}

	var entries []ChatEntry
	sections := strings.Split(content, "## ")

	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}

		nlIdx := strings.Index(section, "\n")
		if nlIdx == -1 {
			continue
		}

		header := strings.TrimSpace(section[:nlIdx])
		body := strings.TrimSpace(section[nlIdx+1:])

		var kind string
		switch header {
		case "User":
			kind = "user"
		case "Assistant":
			kind = "assistant"
		case "System":
			kind = "system"
		case "Error":
			kind = "error"
		default:
			continue
		}

		entries = append(entries, ChatEntry{
			Kind:    kind,
			Content: body,
		})
	}

	return entries
}

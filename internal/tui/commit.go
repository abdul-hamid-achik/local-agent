package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

// runCommit gets the staged diff, asks the LLM for a commit message, and commits.
func runCommit(client llm.Client, model string, extraMsg string) tea.Cmd {
	return func() tea.Msg {
		// Check for staged changes.
		diff, err := gitDiff()
		if err != nil {
			return CommitResultMsg{Err: fmt.Errorf("git diff: %w", err)}
		}
		if strings.TrimSpace(diff) == "" {
			return CommitResultMsg{Err: fmt.Errorf("no staged changes (use `git add` first)")}
		}

		// Truncate very large diffs for the LLM.
		if len(diff) > 8000 {
			diff = diff[:8000] + "\n... (truncated)"
		}

		prompt := "Write a concise git commit message for the following staged diff. " +
			"Return ONLY the commit message, no explanation or markdown. " +
			"Use conventional commit style (e.g. feat:, fix:, refactor:). " +
			"Keep the first line under 72 characters."
		if extraMsg != "" {
			prompt += "\n\nAdditional context: " + extraMsg
		}
		prompt += "\n\nDiff:\n" + diff

		// Ask LLM for commit message.
		var msgBuf strings.Builder
		err = client.ChatStream(context.Background(), llm.ChatOptions{
			Messages: []llm.Message{{Role: "user", Content: prompt}},
			System:   "You are a helpful assistant that writes git commit messages.",
		}, func(chunk llm.StreamChunk) error {
			if chunk.Text != "" {
				msgBuf.WriteString(chunk.Text)
			}
			return nil
		})
		if err != nil {
			return CommitResultMsg{Err: fmt.Errorf("LLM error: %w", err)}
		}

		commitMsg := strings.TrimSpace(msgBuf.String())
		if commitMsg == "" {
			return CommitResultMsg{Err: fmt.Errorf("LLM returned empty commit message")}
		}

		// Add attribution trailer.
		commitMsg += fmt.Sprintf("\n\nAssisted-by: local-agent (%s)", model)

		// Run git commit.
		if err := gitCommit(commitMsg); err != nil {
			return CommitResultMsg{Err: fmt.Errorf("git commit: %w", err)}
		}

		return CommitResultMsg{Message: commitMsg}
	}
}

func gitDiff() (string, error) {
	cmd := exec.Command("git", "diff", "--cached", "--stat")
	stat, _ := cmd.Output()

	cmd = exec.Command("git", "diff", "--cached")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(stat) + "\n" + string(out), nil
}

func gitCommit(msg string) error {
	cmd := exec.Command("git", "commit", "-m", msg)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", err, stderr.String())
	}
	return nil
}

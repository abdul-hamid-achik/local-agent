package ui

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
)

func executionRecoveryNotice(unresolved *agent.UnresolvedExecutionError) string {
	if unresolved == nil {
		return ""
	}
	detail := "execution state requires reconciliation"
	if unresolved.Cause != nil && strings.TrimSpace(unresolved.Cause.Error()) != "" {
		detail = unresolved.Cause.Error()
	}
	if command := unresolved.RecoveryInspectCommand(); command != "" {
		return fmt.Sprintf(
			"Recovery paused after %s: %s. Automatic retry is disabled.\n\nRun /recover to review and record typed evidence inside this session. Read-only CLI inspection:\n  %s\n\nNo tool is retried. After evidence commits, the next prompt will recheck durable state. Exit this TUI before using the CLI apply form; /new starts a separate session and does not reconcile this execution.",
			unresolved.ToolName, detail, command,
		)
	}
	return fmt.Sprintf(
		"Recovery paused after %s: %s. Automatic retry is disabled. This state requires session projection repair; /new starts a separate session and does not reconcile it.",
		unresolved.ToolName, detail,
	)
}

func appendExecutionRecoveryNotice(entries []ChatEntry, unresolved *agent.UnresolvedExecutionError) ([]ChatEntry, bool) {
	notice := executionRecoveryNotice(unresolved)
	if notice == "" {
		return entries, false
	}
	command := unresolved.RecoveryInspectCommand()
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.Kind != "error" {
			continue
		}
		if entry.Content == notice || (command != "" && strings.Contains(entry.Content, command)) {
			return entries, false
		}
	}
	return append(entries, ChatEntry{Kind: "error", Content: notice}), true
}

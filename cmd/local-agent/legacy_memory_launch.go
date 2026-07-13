package main

import (
	"errors"
	"fmt"

	"github.com/abdul-hamid-achik/local-agent/internal/memory"
)

// legacyMemoryQuarantineNotice performs read-only inventory. Launching Local
// Agent must never assign provenance-free memory to the first working
// directory that happens to run it.
func legacyMemoryQuarantineNotice(workspace string) string {
	preview, err := memory.PreviewDefaultLegacyForWorkspace(workspace)
	if err != nil {
		// A completed claim belongs to exactly one workspace. Other workspaces
		// already use their own scoped store, so repeating that durable receipt
		// on every launch is noise rather than an actionable startup failure.
		if errors.Is(err, memory.ErrLegacyMemoryClaimedByAnotherWorkspace) {
			return ""
		}
		return fmt.Sprintf("legacy memory remains quarantined: %v", err)
	}
	if !preview.AlreadyClaimed && preview.Count > 0 {
		return fmt.Sprintf("%d provenance-free memories quarantined; open the TUI and use /migrate-memory to preview explicit attribution", preview.Count)
	}
	return ""
}

// legacyMemoryNoticeForLaunch keeps optional maintenance diagnostics out of
// the interactive startup surface. The hidden /migrate-memory command remains
// the explicit, read-only preview path in the TUI.
func legacyMemoryNoticeForLaunch(workspace string, headless bool) string {
	if !headless {
		return ""
	}
	return legacyMemoryQuarantineNotice(workspace)
}

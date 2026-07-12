package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/ice"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
)

type legacyCheckpointPreview struct {
	Count       int64
	SessionID   int64
	WorkspaceID string
}

type legacyMemoryPreview struct {
	Claim memory.LegacyClaimPreview
}

type legacyICEPreview struct {
	WorkspaceID string
	Claim       ice.LegacyEntryClaimPreview
}

func (m *Model) previewLegacyCheckpoints() {
	m.legacyCheckpointPreview = nil
	if m.sessionStore == nil {
		m.appendLegacyMigrationEntry("error", "Legacy checkpoint migration is unavailable because the session database is not open.")
		return
	}
	count, err := m.sessionStore.CountUnboundLegacyCheckpoints(context.Background())
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy checkpoint preview failed: %v", err))
		return
	}
	if count == 0 {
		m.appendLegacyMigrationEntry("system", "No unbound legacy checkpoints were found. Nothing was changed.")
		return
	}
	if m.sessionID <= 0 {
		m.appendLegacyMigrationEntry("system", fmt.Sprintf(
			"Found %d unbound legacy checkpoints, but there is no persisted active session. Send a message to create a session, then run /migrate-checkpoints again. Nothing was changed.",
			count,
		))
		return
	}
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy checkpoint preview failed: %v", err))
		return
	}
	m.legacyCheckpointPreview = &legacyCheckpointPreview{
		Count: count, SessionID: m.sessionID, WorkspaceID: workspaceID,
	}
	m.appendLegacyMigrationEntry("system", fmt.Sprintf(
		"Found %d unbound legacy checkpoints with no workspace provenance. To claim exactly this set into active session #%d for workspace %s, run `/migrate-checkpoints confirm %d`. This one-time operation cannot be assigned to another workspace later. Nothing has changed yet.",
		count, m.sessionID, workspaceID, count,
	))
}

func (m *Model) claimLegacyCheckpoints(rawCount string) {
	if m.sessionStore == nil {
		m.legacyCheckpointPreview = nil
		m.appendLegacyMigrationEntry("error", "Legacy checkpoint migration is unavailable because the session database is not open.")
		return
	}
	count, err := strconv.ParseInt(strings.TrimSpace(rawCount), 10, 64)
	if err != nil || count <= 0 {
		m.appendLegacyMigrationEntry("error", "Legacy checkpoint confirmation is invalid. Run /migrate-checkpoints to preview again.")
		return
	}
	preview := m.legacyCheckpointPreview
	if preview == nil {
		m.appendLegacyMigrationEntry("error", "Legacy checkpoint confirmation has no active preview. Run /migrate-checkpoints first.")
		return
	}
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		m.legacyCheckpointPreview = nil
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy checkpoint confirmation failed: %v", err))
		return
	}
	if preview.Count != count || preview.SessionID != m.sessionID || preview.WorkspaceID != workspaceID {
		m.legacyCheckpointPreview = nil
		m.appendLegacyMigrationEntry("error", "Legacy checkpoint preview is stale or belongs to another session/workspace. Run /migrate-checkpoints again.")
		return
	}
	m.legacyCheckpointPreview = nil
	receipt, err := m.sessionStore.ClaimUnboundLegacyCheckpointsForActiveSession(
		context.Background(), m.sessionID, workspaceID, count,
	)
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy checkpoint claim failed: %v", err))
		return
	}
	if receipt.AlreadyClaimed {
		m.appendLegacyMigrationEntry("system", fmt.Sprintf(
			"Legacy checkpoints were already claimed for workspace %s in session #%d. Nothing was changed.",
			receipt.WorkspaceID, receipt.SessionID,
		))
		return
	}
	m.appendLegacyMigrationEntry("system", fmt.Sprintf(
		"Claimed %d legacy checkpoints into active session #%d for workspace %s. A durable one-time receipt prevents cross-workspace adoption.",
		receipt.Claimed, receipt.SessionID, receipt.WorkspaceID,
	))
}

func (m *Model) appendLegacyMigrationEntry(kind, content string) {
	m.entries = append(m.entries, ChatEntry{Kind: kind, Content: content})
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
}

func (m *Model) previewLegacyMemory() {
	m.legacyMemoryPreview = nil
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy memory preview failed: %v", err))
		return
	}
	preview, err := memory.PreviewDefaultLegacyForWorkspace(workspaceID)
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy memory remains quarantined: %v", err))
		return
	}
	if preview.AlreadyClaimed {
		m.appendLegacyMigrationEntry("system", fmt.Sprintf("Legacy memory was already claimed for workspace %s. Nothing was changed.", workspaceID))
		return
	}
	if preview.Count == 0 {
		m.appendLegacyMigrationEntry("system", "No quarantined legacy memories were found. Nothing was changed.")
		return
	}
	m.legacyMemoryPreview = &legacyMemoryPreview{Claim: preview}
	m.appendLegacyMigrationEntry("system", fmt.Sprintf(
		"Found %d provenance-free memories in %s. They may contain data from multiple projects. To assign these exact bytes to workspace %s, preserve the global source at %s, and install the scoped copy at %s, run `/migrate-memory confirm %d`. Nothing has changed yet.",
		preview.Count, preview.LegacyPath, workspaceID, preview.BackupPath, preview.ScopedPath, preview.Count,
	))
}

func (m *Model) claimLegacyMemory(rawCount string) {
	count, err := strconv.ParseInt(strings.TrimSpace(rawCount), 10, 64)
	if err != nil || count <= 0 {
		m.appendLegacyMigrationEntry("error", "Legacy memory confirmation is invalid. Run /migrate-memory to preview again.")
		return
	}
	preview := m.legacyMemoryPreview
	if preview == nil {
		m.appendLegacyMigrationEntry("error", "Legacy memory confirmation has no active preview. Run /migrate-memory first.")
		return
	}
	m.legacyMemoryPreview = nil
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil || int64(preview.Claim.Count) != count || preview.Claim.Workspace != workspaceID {
		m.appendLegacyMigrationEntry("error", "Legacy memory preview is stale or belongs to another workspace. Run /migrate-memory again.")
		return
	}
	result, err := memory.ClaimPreviewedDefaultLegacyForWorkspace(workspaceID, preview.Claim)
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy memory claim failed; data remains quarantined: %v", err))
		return
	}
	store := memory.NewStore(preview.Claim.ScopedPath)
	if err := store.Err(); err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy memory was claimed but the scoped store could not be activated: %v", err))
		return
	}
	m.agent.SetMemoryStore(store)
	if engine := m.agent.ICEEngine(); engine != nil {
		engine.SetMemoryStore(store)
	}
	if result.AlreadyClaimed {
		m.appendLegacyMigrationEntry("system", fmt.Sprintf("Legacy memory was already claimed for workspace %s. Nothing was changed.", workspaceID))
		return
	}
	m.appendLegacyMigrationEntry("system", fmt.Sprintf(
		"Claimed %d legacy memories for workspace %s. Source backup: %s. Durable receipt: %s.",
		preview.Claim.Count, workspaceID, result.BackupPath, result.MarkerPath,
	))
}

func (m *Model) previewLegacyICE() {
	m.legacyICEPreview = nil
	engine := m.agent.ICEEngine()
	if engine == nil {
		m.appendLegacyMigrationEntry("error", "Legacy ICE migration is unavailable because ICE is disabled.")
		return
	}
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy ICE preview failed: %v", err))
		return
	}
	preview, err := engine.PreviewLegacyEntries()
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy ICE history remains quarantined: %v", err))
		return
	}
	if preview.AlreadyClaimed {
		m.appendLegacyMigrationEntry("system", fmt.Sprintf("Legacy ICE history was already claimed for workspace %s. Nothing was changed.", workspaceID))
		return
	}
	if preview.Count == 0 {
		m.appendLegacyMigrationEntry("system", "No quarantined legacy ICE entries were found. Nothing was changed.")
		return
	}
	m.legacyICEPreview = &legacyICEPreview{WorkspaceID: workspaceID, Claim: preview}
	m.appendLegacyMigrationEntry("system", fmt.Sprintf(
		"Found %d provenance-free ICE entries in %s. They may contain history from multiple projects. To assign this exact set to workspace %s, run `/migrate-ice confirm %d`. Nothing has changed yet.",
		preview.Count, preview.StorePath, workspaceID, preview.Count,
	))
}

func (m *Model) claimLegacyICE(rawCount string) {
	count, err := strconv.ParseInt(strings.TrimSpace(rawCount), 10, 64)
	if err != nil || count <= 0 {
		m.appendLegacyMigrationEntry("error", "Legacy ICE confirmation is invalid. Run /migrate-ice to preview again.")
		return
	}
	preview := m.legacyICEPreview
	if preview == nil {
		m.appendLegacyMigrationEntry("error", "Legacy ICE confirmation has no active preview. Run /migrate-ice first.")
		return
	}
	m.legacyICEPreview = nil
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil || int64(preview.Claim.Count) != count || preview.WorkspaceID != workspaceID {
		m.appendLegacyMigrationEntry("error", "Legacy ICE preview is stale or belongs to another workspace. Run /migrate-ice again.")
		return
	}
	engine := m.agent.ICEEngine()
	if engine == nil {
		m.appendLegacyMigrationEntry("error", "Legacy ICE migration is unavailable because ICE is disabled.")
		return
	}
	result, err := engine.ClaimPreviewedLegacyEntries(preview.Claim)
	if err != nil {
		m.appendLegacyMigrationEntry("error", fmt.Sprintf("Legacy ICE claim failed; history remains quarantined: %v", err))
		return
	}
	if result.AlreadyClaimed {
		m.iceConversations = engine.ScopedEntryCount()
		m.appendLegacyMigrationEntry("system", fmt.Sprintf("Legacy ICE history was already claimed for workspace %s. Nothing was changed.", workspaceID))
		return
	}
	m.iceConversations = engine.ScopedEntryCount()
	m.appendLegacyMigrationEntry("system", fmt.Sprintf(
		"Claimed %d legacy ICE entries for workspace %s. Durable receipt: %s.",
		result.Claimed, workspaceID, result.MarkerPath,
	))
}

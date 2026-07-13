package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

// maxAutoCheckpoints bounds how many checkpoints are retained per session so
// pre-compaction snapshots don't grow without limit.
const maxAutoCheckpoints = 25

// CheckpointStore is the subset of db.Store the agent needs to persist and
// restore conversation snapshots. Kept as an interface so the agent has no hard
// dependency on a concrete store (and tests can supply a fake).
type CheckpointStore interface {
	CreateCheckpointForWorkspace(ctx context.Context, sessionID int64, workspaceID, label, kind, messagesJSON string, msgCount int64) (int64, error)
	ListCheckpointsForWorkspace(ctx context.Context, sessionID int64, workspaceID string) ([]db.Checkpoint, error)
	GetCheckpoint(ctx context.Context, id int64) (db.Checkpoint, error)
	PruneCheckpoints(ctx context.Context, sessionID int64, keep int) error
	PruneCheckpointsByKindForWorkspace(ctx context.Context, sessionID int64, workspaceID, kind string, keep int) error
}

// SetCheckpointStore wires a checkpoint store and the session it belongs to.
// Without it, checkpointing is silently disabled.
func (a *Agent) SetCheckpointStore(cs CheckpointStore, sessionID int64) {
	a.checkpointStore = cs
	a.checkpointSessionID = sessionID
}

// SetCheckpointSessionID updates the session a checkpoint is associated with
// (e.g. once a session row is created mid-run).
func (a *Agent) SetCheckpointSessionID(sessionID int64) {
	a.mu.Lock()
	a.checkpointSessionID = sessionID
	a.mu.Unlock()
}

// snapshotMessagesJSON marshals the current history under the read lock.
func (a *Agent) snapshotMessagesJSON() (string, int, error) {
	a.mu.RLock()
	msgs := make([]llm.Message, len(a.messages))
	copy(msgs, a.messages)
	a.mu.RUnlock()
	msgs = SanitizeMessagesForPersistence(msgs)

	data, err := json.Marshal(msgs)
	if err != nil {
		return "", 0, fmt.Errorf("marshal messages: %w", err)
	}
	return string(data), len(msgs), nil
}

func (a *Agent) checkpointWorkspaceID() (string, error) {
	workDir := a.WorkDir()
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve checkpoint workspace: %w", err)
		}
	}
	absolute, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve checkpoint workspace: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute), nil
}

// CreateCheckpoint snapshots the current conversation. kind is typically
// db.CheckpointManual or db.CheckpointPreCompaction. Returns the new id, or 0
// if no checkpoint store is configured.
func (a *Agent) CreateCheckpoint(ctx context.Context, label, kind string) (int64, error) {
	if a.checkpointStore == nil {
		return 0, nil
	}
	msgsJSON, count, err := a.snapshotMessagesJSON()
	if err != nil {
		return 0, err
	}

	a.mu.RLock()
	sid := a.checkpointSessionID
	a.mu.RUnlock()
	workspaceID, err := a.checkpointWorkspaceID()
	if err != nil {
		return 0, err
	}

	id, err := a.checkpointStore.CreateCheckpointForWorkspace(ctx, sid, workspaceID, label, kind, msgsJSON, int64(count))
	if err != nil {
		return 0, err
	}
	// Best-effort prune only automatic snapshots. Manual checkpoints are an
	// explicit user artifact and must not disappear behind their back.
	if kind == db.CheckpointPreCompaction {
		_ = a.checkpointStore.PruneCheckpointsByKindForWorkspace(ctx, sid, workspaceID, kind, maxAutoCheckpoints)
	}
	if a.logger != nil {
		a.logger.Info("checkpoint created", "id", id, "kind", kind, "messages", count)
	}
	return id, nil
}

// ListCheckpoints returns the checkpoints for the current session, newest first.
func (a *Agent) ListCheckpoints(ctx context.Context) ([]db.Checkpoint, error) {
	if a.checkpointStore == nil {
		return nil, nil
	}
	a.mu.RLock()
	sid := a.checkpointSessionID
	a.mu.RUnlock()
	workspaceID, err := a.checkpointWorkspaceID()
	if err != nil {
		return nil, err
	}
	return a.checkpointStore.ListCheckpointsForWorkspace(ctx, sid, workspaceID)
}

// RestoreCheckpoint replaces the live conversation with a stored snapshot and
// returns the number of messages restored.
func (a *Agent) RestoreCheckpoint(ctx context.Context, id int64) (int, error) {
	if a.checkpointStore == nil {
		return 0, fmt.Errorf("checkpoints are not enabled")
	}
	cp, err := a.checkpointStore.GetCheckpoint(ctx, id)
	if err != nil {
		return 0, err
	}
	a.mu.RLock()
	sid := a.checkpointSessionID
	a.mu.RUnlock()
	workspaceID, err := a.checkpointWorkspaceID()
	if err != nil {
		return 0, err
	}
	if cp.WorkspaceID != workspaceID {
		return 0, fmt.Errorf("checkpoint %d belongs to a different workspace", id)
	}
	if cp.SessionID != sid {
		return 0, fmt.Errorf("checkpoint %d belongs to session %d, not active session %d", id, cp.SessionID, sid)
	}
	var msgs []llm.Message
	if err := json.Unmarshal([]byte(cp.Messages), &msgs); err != nil {
		return 0, fmt.Errorf("decode checkpoint %d: %w", id, err)
	}
	a.ReplaceMessages(msgs)
	if a.logger != nil {
		a.logger.Info("checkpoint restored", "id", id, "messages", len(msgs))
	}
	return len(msgs), nil
}

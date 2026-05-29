package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Checkpoint kinds.
const (
	CheckpointManual        = "manual"
	CheckpointPreCompaction = "pre-compaction"
)

// ErrCheckpointNotFound is returned when a checkpoint id does not exist.
var ErrCheckpointNotFound = errors.New("checkpoint not found")

// Checkpoint is an immutable snapshot of a conversation's message history.
type Checkpoint struct {
	ID        int64
	SessionID int64
	Label     string
	Kind      string
	Messages  string // JSON-encoded []llm.Message (opaque to this package)
	MsgCount  int64
	CreatedAt string
}

// CreateCheckpoint stores a snapshot and returns its id. messagesJSON is stored
// verbatim; the caller owns its encoding (keeps this package free of llm types).
func (s *Store) CreateCheckpoint(ctx context.Context, sessionID int64, label, kind, messagesJSON string, msgCount int64) (int64, error) {
	if kind == "" {
		kind = CheckpointManual
	}
	if messagesJSON == "" {
		messagesJSON = "[]"
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO checkpoints (session_id, label, kind, messages, msg_count) VALUES (?, ?, ?, ?, ?)`,
		sessionID, label, kind, messagesJSON, msgCount,
	)
	if err != nil {
		return 0, fmt.Errorf("create checkpoint: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("checkpoint id: %w", err)
	}
	return id, nil
}

// ListCheckpoints returns checkpoints for a session, most recent first. The
// Messages field is omitted (left empty) to keep listings cheap; use
// GetCheckpoint to fetch the snapshot payload.
func (s *Store) ListCheckpoints(ctx context.Context, sessionID int64) ([]Checkpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, label, kind, msg_count, created_at
		   FROM checkpoints WHERE session_id = ? ORDER BY id DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	defer rows.Close()

	var out []Checkpoint
	for rows.Next() {
		var c Checkpoint
		if err := rows.Scan(&c.ID, &c.SessionID, &c.Label, &c.Kind, &c.MsgCount, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan checkpoint: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCheckpoint returns a single checkpoint including its messages payload.
func (s *Store) GetCheckpoint(ctx context.Context, id int64) (Checkpoint, error) {
	var c Checkpoint
	err := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, label, kind, messages, msg_count, created_at
		   FROM checkpoints WHERE id = ?`,
		id,
	).Scan(&c.ID, &c.SessionID, &c.Label, &c.Kind, &c.Messages, &c.MsgCount, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{}, ErrCheckpointNotFound
	}
	if err != nil {
		return Checkpoint{}, fmt.Errorf("get checkpoint: %w", err)
	}
	return c, nil
}

// DeleteCheckpoint removes a checkpoint by id (no error if it does not exist).
func (s *Store) DeleteCheckpoint(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM checkpoints WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete checkpoint: %w", err)
	}
	return nil
}

// PruneCheckpoints keeps only the most recent keep checkpoints for a session,
// deleting older ones. A non-positive keep deletes none.
func (s *Store) PruneCheckpoints(ctx context.Context, sessionID int64, keep int) error {
	if keep <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM checkpoints
		  WHERE session_id = ?
		    AND id NOT IN (
		        SELECT id FROM checkpoints WHERE session_id = ? ORDER BY id DESC LIMIT ?
		    )`,
		sessionID, sessionID, keep,
	)
	if err != nil {
		return fmt.Errorf("prune checkpoints: %w", err)
	}
	return nil
}

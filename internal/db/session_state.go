package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrSessionStateNotFound = errors.New("session state not found")

// SaveSessionState atomically replaces the lossless state for one session.
func (s *Store) SaveSessionState(ctx context.Context, sessionID int64, stateJSON string) error {
	if sessionID <= 0 {
		return fmt.Errorf("invalid session id %d", sessionID)
	}
	if stateJSON == "" {
		stateJSON = "{}"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session state save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_state (session_id, state_json) VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			state_json = excluded.state_json,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`, sessionID, stateJSON)
	if err != nil {
		return fmt.Errorf("save session state: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE sessions SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("update session timestamp: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated session row count: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("update session timestamp affected %d rows, want 1", affected)
	}
	if err := tx.Commit(); err != nil {
		// A commit error can be ambiguous to the caller. Read the exact payload
		// back on a fresh context; if it is present, the safety-critical snapshot
		// was committed and must not be treated as missing.
		readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stored, readErr := s.GetSessionState(readCtx, sessionID)
		if readErr == nil && stored == stateJSON {
			return nil
		}
		return fmt.Errorf("commit session state: %w (read-back: %v)", err, readErr)
	}
	return nil
}

// GetSessionState returns the lossless state payload for one session.
func (s *Store) GetSessionState(ctx context.Context, sessionID int64) (string, error) {
	var stateJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT state_json FROM session_state WHERE session_id = ?`, sessionID,
	).Scan(&stateJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionStateNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get session state: %w", err)
	}
	return stateJSON, nil
}

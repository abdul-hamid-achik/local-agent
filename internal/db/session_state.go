package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_state (session_id, state_json) VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			state_json = excluded.state_json,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`, sessionID, stateJSON)
	if err != nil {
		return fmt.Errorf("save session state: %w", err)
	}
	return s.UpdateSessionTimestamp(ctx, sessionID)
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

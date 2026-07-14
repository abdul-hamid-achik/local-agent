package ui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
)

// SessionResumeSelector identifies one exact saved session or the newest
// session in the canonical current workspace. Its fields are closed so callers
// must pass through the validated constructors below.
type SessionResumeSelector struct {
	sessionID int64
	latest    bool
}

// ParseSessionResumeSelector parses the value accepted by --resume.
func ParseSessionResumeSelector(value string) (SessionResumeSelector, error) {
	if value == "" {
		return SessionResumeSelector{}, fmt.Errorf("resume selector is required (use a positive session ID or latest)")
	}
	if value == "latest" {
		return SessionResumeSelector{latest: true}, nil
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return SessionResumeSelector{}, fmt.Errorf("invalid resume selector %q (use a positive session ID or latest)", value)
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return SessionResumeSelector{}, fmt.Errorf("invalid resume selector %q (use a positive session ID or latest)", value)
	}
	return SessionResumeSelector{sessionID: id}, nil
}

// SessionIDResumeSelector constructs the exact-ID form used by the interactive
// picker after its database-backed list selection.
func SessionIDResumeSelector(id int64) (SessionResumeSelector, error) {
	if id <= 0 {
		return SessionResumeSelector{}, fmt.Errorf("invalid session id %d", id)
	}
	return SessionResumeSelector{sessionID: id}, nil
}

func (s SessionResumeSelector) valid() bool {
	return (s.latest && s.sessionID == 0) || (!s.latest && s.sessionID > 0)
}

func (s SessionResumeSelector) resolve(ctx context.Context, store *db.Store, workspaceID string) (int64, error) {
	if !s.valid() {
		return 0, fmt.Errorf("invalid session resume selector")
	}
	if !s.latest {
		return s.sessionID, nil
	}
	sessions, err := listPersistedSessions(ctx, store, workspaceID, 1)
	if err != nil {
		return 0, err
	}
	if len(sessions) == 0 {
		return 0, fmt.Errorf("no saved sessions in this workspace")
	}
	if sessions[0].ID <= 0 {
		return 0, fmt.Errorf("latest saved session has an invalid id")
	}
	return sessions[0].ID, nil
}

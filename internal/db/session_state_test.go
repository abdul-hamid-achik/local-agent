package db

import (
	"context"
	"errors"
	"testing"
)

func TestSessionStateRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	session, err := store.CreateSession(ctx, CreateSessionParams{Title: "state", Model: "qwen", Mode: "BUILD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSessionState(ctx, session.ID); !errors.Is(err, ErrSessionStateNotFound) {
		t.Fatalf("missing state error = %v", err)
	}
	if err := store.SaveSessionState(ctx, session.ID, `{"turn":1}`); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSessionState(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"turn":1}` {
		t.Fatalf("state = %q", got)
	}
	if err := store.SaveSessionState(ctx, session.ID, `{"turn":2}`); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetSessionState(ctx, session.ID)
	if err != nil || got != `{"turn":2}` {
		t.Fatalf("updated state = %q, err=%v", got, err)
	}
}

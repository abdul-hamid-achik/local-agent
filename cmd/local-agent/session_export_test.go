package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
)

type fakeSessionExportStore struct {
	sessions []db.Session
	session  db.Session
	stateErr error
	stateRaw string
	events   []execution.Event
	states   []controlplane.State
	tokens   []db.TokenStat
	files    []db.FileChange
}

func (s *fakeSessionExportStore) ListSessions(context.Context, db.ListSessionsParams) ([]db.Session, error) {
	return s.sessions, nil
}
func (s *fakeSessionExportStore) GetSession(context.Context, int64) (db.Session, error) {
	return s.session, nil
}
func (s *fakeSessionExportStore) GetSessionState(context.Context, int64) (string, error) {
	if s.stateErr != nil {
		return "", s.stateErr
	}
	return s.stateRaw, nil
}
func (s *fakeSessionExportStore) ListSessionExecutionEvents(context.Context, int64, string, int) ([]execution.Event, error) {
	return s.events, nil
}
func (s *fakeSessionExportStore) ListControlStates(context.Context, controlplane.Query) ([]controlplane.State, error) {
	return s.states, nil
}
func (s *fakeSessionExportStore) GetSessionTokenStats(context.Context, int64) ([]db.TokenStat, error) {
	return s.tokens, nil
}
func (s *fakeSessionExportStore) GetSessionFileChanges(context.Context, int64) ([]db.FileChange, error) {
	return s.files, nil
}
func (s *fakeSessionExportStore) ListCheckpoints(context.Context, int64) ([]db.Checkpoint, error) {
	return nil, nil
}

func exportTestEvent(execID, tool string, id int64, effect execution.EffectClass, eventType execution.EventType) execution.Event {
	return execution.Event{
		ID: id,
		Identity: execution.Identity{
			SessionID: 7, WorkspaceID: "/workspace/repo", ExecutionID: execID,
			TurnID: "turn-1", ToolName: tool, Kind: execution.KindMCP, EffectClass: effect,
		},
		Type: eventType, Approval: execution.ApprovalNotApplicable,
		OccurredAt: time.Date(2026, 7, 14, 9, 52, 0, 0, time.UTC),
	}
}

func TestSessionExportWritesJSONLAndMarkdownWithOpenIssues(t *testing.T) {
	store := &fakeSessionExportStore{
		session: db.Session{
			ID: 7, Title: "investigate bob", Model: "deepseek-v4-pro:cloud",
			Mode: "AUTO", WorkspaceID: "/workspace/repo",
			CreatedAt: "2026-07-14T09:51:00Z", UpdatedAt: "2026-07-14T09:52:00Z",
		},
		stateRaw: `{"version":2,"messages":[]}`,
		events: []execution.Event{
			exportTestEvent("exec_ok", "mcphub__bob__bob_inspect", 1, execution.EffectReadOnly, execution.EventCompleted),
			exportTestEvent("exec_stuck", "mcphub__bob__bob_check", 2, execution.EffectUnknown, execution.EventOutcomeUnknown),
		},
		files: []db.FileChange{{FilePath: "/workspace/repo/main.go", ToolName: "write", Added: 3, Removed: 1}},
	}
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := handleSessionExport(store, "/workspace/repo", []string{"--out", dir, "7"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 open issue(s)") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	jsonlPath := filepath.Join(dir, "session-7.jsonl")
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var row struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", scanner.Text(), err)
		}
		kinds[row.Kind]++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"session", "state_json", "execution_event", "file_change", "open_issue"} {
		if kinds[kind] == 0 {
			t.Fatalf("JSONL missing %q record: %#v", kind, kinds)
		}
	}
	if kinds["execution_event"] != 2 || kinds["open_issue"] != 1 {
		t.Fatalf("JSONL record counts = %#v", kinds)
	}

	md, err := os.ReadFile(filepath.Join(dir, "session-7-summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Session 7 audit", "investigate bob", "## Open issues",
		"UNRESOLVED", "exec_stuck", "execution recover 7 --all", "## Execution timeline", "main.go",
	} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("markdown missing %q", want)
		}
	}
}

func TestSessionExportRejectsForeignWorkspaceAndBadFormat(t *testing.T) {
	store := &fakeSessionExportStore{session: db.Session{ID: 7, WorkspaceID: "/workspace/other"}}
	var stdout, stderr bytes.Buffer
	if code := handleSessionExport(store, "/workspace/repo", []string{"7"}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "different workspace") {
		t.Fatalf("foreign workspace exit=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	if code := handleSessionExport(store, "/workspace/repo", []string{"--format", "pdf", "7"}, &stdout, &stderr); code != 2 ||
		!strings.Contains(stderr.String(), "unknown --format") {
		t.Fatalf("bad format exit=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	if code := handleSessionExport(store, "/workspace/repo", []string{"nope"}, &stdout, &stderr); code != 2 {
		t.Fatalf("bad session id exit=%d", code)
	}
}

func TestSessionListRendersAndEmpties(t *testing.T) {
	store := &fakeSessionExportStore{sessions: []db.Session{
		{ID: 7, Model: "qwen", Mode: "AUTO", UpdatedAt: "2026-07-14T09:52:00Z", Title: "investigate bob"},
	}}
	var stdout, stderr bytes.Buffer
	if code := handleSessionList(store, "/workspace/repo", nil, &stdout, &stderr); code != 0 {
		t.Fatalf("list exit=%d stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"ID", "investigate bob", "session export SESSION_ID"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("list output missing %q: %s", want, stdout.String())
		}
	}

	empty := &fakeSessionExportStore{}
	stdout.Reset()
	if code := handleSessionList(empty, "/workspace/repo", []string{"--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty json list exit=%d", code)
	}
	if strings.TrimSpace(stdout.String()) != "[]" {
		t.Fatalf("empty json list = %q", stdout.String())
	}
}

func TestRelativizeHomeHidesHomePrefix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if got := relativizeHome(home); got != "~" {
		t.Fatalf("relativizeHome(home) = %q", got)
	}
	if got := relativizeHome(filepath.Join(home, "projects", "bob")); !strings.HasPrefix(got, "~"+string(os.PathSeparator)) {
		t.Fatalf("relativizeHome(subdir) = %q", got)
	}
	if got := relativizeHome("/opt/elsewhere"); got != "/opt/elsewhere" {
		t.Fatalf("relativizeHome(outside) = %q", got)
	}
}

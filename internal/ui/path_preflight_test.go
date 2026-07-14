package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
)

func TestExplicitPromptPathCandidatesRecognizesQuotedPathsWithoutGrantCap(t *testing.T) {
	text := `read "/tmp/quoted path.md", '/tmp/single path.md'; ` +
		"`/tmp/backtick path.md` and /tmp/plain.md /tmp/fifth.md /tmp/plain.md"
	got := explicitPromptPathCandidates(text)
	want := []string{
		"/tmp/quoted path.md",
		"/tmp/single path.md",
		"/tmp/backtick path.md",
		"/tmp/plain.md",
		"/tmp/fifth.md",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	if scan := scanExplicitPromptPaths(text); scan.MoreCandidates {
		t.Fatal("ordinary candidate count hit the anti-abuse scan limit")
	}
}

func TestExplicitPromptPathCandidatesRecognizesEscapedWhitespace(t *testing.T) {
	got := explicitPromptPathCandidates(`inspect /Users/me/Desktop/My\ Screenshot.jpg and /tmp/plain.txt`)
	want := []string{`/Users/me/Desktop/My Screenshot.jpg`, "/tmp/plain.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("escaped candidates = %#v, want %#v", got, want)
	}
}

func TestExplicitPromptPathCandidatesPreservesQuotedAndEscapedTerminalPunctuation(t *testing.T) {
	text := `inspect "/tmp/Project (copy)" and /tmp/Report\ final!`
	got := explicitPromptPathCandidates(text)
	want := []string{"/tmp/Project (copy)", "/tmp/Report final!"}
	if !slices.Equal(got, want) {
		t.Fatalf("punctuated candidates = %#v, want %#v", got, want)
	}
	for _, intent := range scanExplicitPromptPaths(text).Intents {
		if intent.Fallback != "" {
			t.Fatalf("exact quoted/escaped intent gained punctuation fallback: %#v", intent)
		}
	}
}

func TestPromptPathScanHasSeparateHardCandidateLimit(t *testing.T) {
	parts := make([]string, 0, maxPromptPathScanIntents+1)
	for index := 0; index <= maxPromptPathScanIntents; index++ {
		parts = append(parts, fmt.Sprintf("/tmp/candidate-%d", index))
	}
	scan := scanExplicitPromptPaths(strings.Join(parts, " "))
	if !scan.MoreCandidates || len(scan.Intents) != maxPromptPathScanIntents {
		t.Fatalf("hard-cap scan = intents:%d more:%v", len(scan.Intents), scan.MoreCandidates)
	}
}

func TestPromptPathHardCandidateLimitKeepsDraftAndSendsNothing(t *testing.T) {
	parts := make([]string, 0, maxPromptPathScanIntents+1)
	for index := 0; index <= maxPromptPathScanIntents; index++ {
		parts = append(parts, fmt.Sprintf("/missing/candidate-%d", index))
	}
	draft := "compare " + strings.Join(parts, " ")
	m := newTestModel(t)
	t.Cleanup(m.agent.Close)
	m.input.SetValue(draft)
	preflight := awaitCommandMessage[PromptPathPreflightResultMsg](t, commandMessages(m.submitInput()), 2*time.Second)
	if !preflight.MoreCandidates || !preflight.CandidateLimitExceeded || len(preflight.Grants) != 0 {
		t.Fatalf("hard-limit preflight = %#v", preflight)
	}
	updated, cmd := m.Update(preflight)
	m = updated.(*Model)
	if cmd != nil || m.input.Value() != draft || len(m.promptHistory) != 0 || len(m.agent.Messages()) != 0 || len(m.agent.ReadGrants()) != 0 {
		t.Fatalf("hard limit sent or authorized work: cmd=%v input=%q history=%#v messages=%#v grants=%#v", cmd != nil, m.input.Value(), m.promptHistory, m.agent.Messages(), m.agent.ReadGrants())
	}
	if transcript := entryText(m.entries); !strings.Contains(transcript, "more than 32 distinct path candidates") || !strings.Contains(transcript, "nothing was sent") {
		t.Fatalf("hard-limit guidance missing: %s", transcript)
	}
}

func TestPromptPathOverflowKeepsDraftAndSendsNothing(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "external")
	for _, path := range []string{workspace, external} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	paths := make([]string, 0, maxPromptPathIntents+1)
	for index := 0; index <= maxPromptPathIntents; index++ {
		path := filepath.Join(external, fmt.Sprintf("evidence-%d.txt", index))
		if err := os.WriteFile(path, []byte("evidence"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	draft := "review " + strings.Join(paths, " ")

	m := newTestModel(t)
	m.agent.SetWorkDir(workspace)
	t.Cleanup(m.agent.Close)
	m.input.SetValue(draft)
	preflight := awaitCommandMessage[PromptPathPreflightResultMsg](t, commandMessages(m.submitInput()), 2*time.Second)
	if !preflight.MoreCandidates || len(preflight.Grants) != 0 {
		t.Fatalf("overflow preflight = %#v", preflight)
	}
	updated, cmd := m.Update(preflight)
	m = updated.(*Model)
	if cmd != nil || m.input.Value() != draft || len(m.promptHistory) != 0 || len(m.agent.Messages()) != 0 || len(m.agent.ReadGrants()) != 0 {
		t.Fatalf("overflow sent or authorized work: cmd=%v input=%q history=%#v messages=%#v grants=%#v", cmd != nil, m.input.Value(), m.promptHistory, m.agent.Messages(), m.agent.ReadGrants())
	}
	if transcript := entryText(m.entries); !strings.Contains(transcript, "more than 4 new external read grants") || !strings.Contains(transcript, "nothing was sent") {
		t.Fatalf("overflow guidance missing: %s", transcript)
	}
}

func TestPromptPathGrantLimitIgnoresWorkspacePathsAndCollapsesCoverage(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "external")
	for _, path := range []string{workspace, external} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	workspacePaths := make([]string, 0, maxPromptPathIntents+1)
	externalPaths := make([]string, 0, maxPromptPathIntents+1)
	for index := 0; index <= maxPromptPathIntents; index++ {
		inside := filepath.Join(workspace, fmt.Sprintf("inside-%d.txt", index))
		outside := filepath.Join(external, fmt.Sprintf("outside-%d.txt", index))
		for _, path := range []string{inside, outside} {
			if err := os.WriteFile(path, []byte("evidence"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		workspacePaths = append(workspacePaths, inside)
		externalPaths = append(externalPaths, outside)
	}

	ag := agent.New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	t.Cleanup(ag.Close)
	workspaceScan := scanExplicitPromptPaths(strings.Join(workspacePaths, " "))
	if grants, overflow := inspectPromptReadGrantIntents(ag, workspaceScan.Intents); overflow || len(grants) != 0 {
		t.Fatalf("workspace paths consumed external grant budget: grants=%#v overflow=%v", grants, overflow)
	}

	coveredScan := scanExplicitPromptPaths(strings.Join(append(externalPaths, external), " "))
	grants, overflow := inspectPromptReadGrantIntents(ag, coveredScan.Intents)
	canonicalExternal, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	if overflow || len(grants) != 1 || grants[0].Kind != agent.ReadGrantDirectory || grants[0].Path != canonicalExternal {
		t.Fatalf("covered grants = %#v overflow=%v", grants, overflow)
	}
}

func TestFreePromptPathPunctuationFallsBackOnlyWhenExactPathIsMissing(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(base, "report")
	punctuated := plain + ","
	if err := os.WriteFile(plain, []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}

	ag := agent.New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	t.Cleanup(ag.Close)
	scan := scanExplicitPromptPaths("inspect " + punctuated)
	grants, overflow := inspectPromptReadGrantIntents(ag, scan.Intents)
	canonicalPlain, err := filepath.EvalSymlinks(plain)
	if err != nil {
		t.Fatal(err)
	}
	if overflow || len(grants) != 1 || grants[0].Path != canonicalPlain {
		t.Fatalf("verified fallback grants = %#v overflow=%v", grants, overflow)
	}

	if err := os.WriteFile(punctuated, []byte("punctuated"), 0o600); err != nil {
		t.Fatal(err)
	}
	grants, overflow = inspectPromptReadGrantIntents(ag, scan.Intents)
	canonicalPunctuated, err := filepath.EvalSymlinks(punctuated)
	if err != nil {
		t.Fatal(err)
	}
	if overflow || len(grants) != 1 || grants[0].Path != canonicalPunctuated {
		t.Fatalf("exact punctuated grants = %#v overflow=%v", grants, overflow)
	}
}

func TestExplicitPromptPathCandidatesRecognizesTildeAndRejectsURLs(t *testing.T) {
	got := explicitPromptPathCandidates(`compare "~/notes with spaces.md" with https://example.com/a and @/tmp/local.md`)
	want := []string{"~/notes with spaces.md", "/tmp/local.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
}

func TestInspectPromptReadGrantsKeepsExactFilesNarrowAndDeduplicated(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "external")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(external, "requested.txt")
	if err := os.WriteFile(target, []byte("requested"), 0o600); err != nil {
		t.Fatal(err)
	}

	ag := agent.New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	t.Cleanup(ag.Close)

	grants := inspectPromptReadGrants(ag, []string{target, target})
	if len(grants) != 1 || grants[0].Kind != agent.ReadGrantExactFile {
		t.Fatalf("exact grants = %#v", grants)
	}
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if grants[0].Path != canonical {
		t.Fatalf("exact grant path = %q, want %q", grants[0].Path, canonical)
	}
	if grants[0].Path == filepath.Dir(grants[0].Path) || grants[0].Path == external {
		t.Fatalf("exact grant widened to a directory: %#v", grants[0])
	}

	// An explicitly named directory supersedes only contained candidates. It
	// is never inferred from the exact-file candidate above.
	grants = inspectPromptReadGrants(ag, []string{target, external})
	canonicalExternal, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].Kind != agent.ReadGrantDirectory || grants[0].Path != canonicalExternal {
		t.Fatalf("explicit directory did not supersede contained file: %#v", grants)
	}
}

func TestPromptPathPreflightIgnoresWorkspaceMissingAndCoveredPaths(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "external")
	for _, path := range []string{workspace, external} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	workspaceFile := filepath.Join(workspace, "inside.txt")
	coveredFile := filepath.Join(external, "covered.txt")
	for path, content := range map[string]string{workspaceFile: "inside", coveredFile: "covered"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ag := agent.New(nil, nil, 0)
	ag.SetWorkDir(workspace)
	t.Cleanup(ag.Close)
	if _, err := ag.AddReadRoot(external); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "missing.txt")
	if grants := inspectPromptReadGrants(ag, []string{workspaceFile, missing, coveredFile}); len(grants) != 0 {
		t.Fatalf("already-readable or missing paths requested authority: %#v", grants)
	}
}

func TestOrdinaryPromptExactFileApprovalAutoResumesOnce(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	external := filepath.Join(base, "external")
	for _, path := range []string{workspace, external} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(external, "requested file.txt")
	sibling := filepath.Join(external, "sibling.txt")
	for path, content := range map[string]string{target: "requested", sibling: "sibling"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m := newTestModel(t)
	m.agent.SetWorkDir(workspace)
	t.Cleanup(m.agent.Close)
	draft := `please summarize "` + target + `"`
	m.input.SetValue(draft)

	preflightCmd := m.submitInput()
	if preflightCmd == nil || !m.readScopeOpRunning || len(m.promptHistory) != 0 {
		t.Fatalf("preflight consumed prompt early: cmd=%v running=%v history=%#v", preflightCmd != nil, m.readScopeOpRunning, m.promptHistory)
	}
	preflight := awaitCommandMessage[PromptPathPreflightResultMsg](t, commandMessages(preflightCmd), 2*time.Second)
	updated, cmd := m.Update(preflight)
	m = updated.(*Model)
	if cmd != nil || m.readScopePrompt == nil || len(m.readScopePrompt.Grants) != 1 || m.readScopePrompt.Grants[0].Kind != agent.ReadGrantExactFile {
		t.Fatalf("preflight prompt = %#v, cmd=%v", m.readScopePrompt, cmd != nil)
	}
	plain := ansi.Strip(m.renderReadScopePrompt())
	for _, want := range []string{"exact external read-only file", filepath.Base(target), "never include", "siblings", "allow read-only", "deny", "cancel"} {
		if !strings.Contains(strings.ToLower(plain), strings.ToLower(want)) {
			t.Fatalf("exact-file approval missing %q:\n%s", want, plain)
		}
	}

	updated, mutationCmd := m.Update(charKey('y'))
	m = updated.(*Model)
	if mutationCmd == nil || !m.readScopeOpRunning || m.readScopePrompt != nil {
		t.Fatal("allow did not start exact-file grant mutation")
	}
	receipt := awaitCommandMessage[ReadScopeResultMsg](t, commandMessages(mutationCmd), 2*time.Second)
	updated, agentCmd := m.Update(receipt)
	m = updated.(*Model)
	if agentCmd == nil || m.state != StateWaiting || m.readScopePrompt != nil || m.readScopeOpRunning {
		t.Fatalf("approval did not resume one agent turn: cmd=%v state=%v prompt=%#v running=%v", agentCmd != nil, m.state, m.readScopePrompt, m.readScopeOpRunning)
	}
	if len(m.promptHistory) != 1 || m.promptHistory[0] != draft {
		t.Fatalf("auto-resume history = %#v", m.promptHistory)
	}
	if messages := m.agent.Messages(); len(messages) != 1 || messages[0].Role != "user" || messages[0].Content != draft {
		t.Fatalf("auto-resume agent messages = %#v", messages)
	}
	userEntries := 0
	for _, entry := range m.entries {
		if entry.Kind == "user" && entry.Content == draft {
			userEntries++
		}
	}
	if userEntries != 1 {
		t.Fatalf("auto-resume visible user entries = %d; entries=%#v", userEntries, m.entries)
	}
	grants := m.agent.ReadGrants()
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].Kind != agent.ReadGrantExactFile || grants[0].Path != canonicalTarget {
		t.Fatalf("committed grants = %#v", grants)
	}
	inspection, err := m.agent.InspectReadPath(sibling)
	if err != nil || inspection.AlreadyReadable {
		t.Fatalf("sibling inherited exact-file authority: %#v, %v", inspection, err)
	}
}

func TestOrdinaryPromptApprovalRejectsFileReplacedWhilePromptIsOpen(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "requested.txt")
	if err := os.WriteFile(target, []byte("approved identity"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t)
	m.agent.SetWorkDir(workspace)
	t.Cleanup(m.agent.Close)
	draft := "inspect " + target
	m.input.SetValue(draft)
	preflight := awaitCommandMessage[PromptPathPreflightResultMsg](t, commandMessages(m.submitInput()), 2*time.Second)
	updated, _ := m.Update(preflight)
	m = updated.(*Model)
	if m.readScopePrompt == nil {
		t.Fatal("external path did not open approval")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("replacement identity"), 0o600); err != nil {
		t.Fatal(err)
	}

	updated, mutationCmd := m.Update(charKey('y'))
	m = updated.(*Model)
	receipt := awaitCommandMessage[ReadScopeResultMsg](t, commandMessages(mutationCmd), 2*time.Second)
	updated, agentCmd := m.Update(receipt)
	m = updated.(*Model)
	if agentCmd != nil || m.state == StateWaiting || len(m.promptHistory) != 0 || len(m.agent.ReadGrants()) != 0 {
		t.Fatalf("stale approval resumed or leaked authority: cmd=%v state=%v history=%#v grants=%#v", agentCmd != nil, m.state, m.promptHistory, m.agent.ReadGrants())
	}
	if m.input.Value() != draft {
		t.Fatalf("stale approval did not restore draft: %q", m.input.Value())
	}
	if transcript := entryText(m.entries); !strings.Contains(transcript, "changed after approval preview") {
		t.Fatalf("stale identity error is not actionable: %s", transcript)
	}
}

func TestOrdinaryPromptExternalPathDenyAndCancelRestoreExactDraft(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "requested.txt")
	if err := os.WriteFile(target, []byte("requested"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		key  rune
	}{
		{name: "deny", key: 'n'},
		{name: "cancel", key: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newTestModel(t)
			m.agent.SetWorkDir(workspace)
			t.Cleanup(m.agent.Close)
			draft := "inspect " + target
			m.input.SetValue(draft)
			preflight := awaitCommandMessage[PromptPathPreflightResultMsg](t, commandMessages(m.submitInput()), 2*time.Second)
			updated, _ := m.Update(preflight)
			m = updated.(*Model)
			if test.key == 0 {
				updated, _ = m.Update(escKey())
			} else {
				updated, _ = m.Update(charKey(test.key))
			}
			m = updated.(*Model)
			if m.input.Value() != draft || m.readScopePrompt != nil || len(m.promptHistory) != 0 || len(m.agent.ReadGrants()) != 0 {
				t.Fatalf("%s changed draft or authority: input=%q prompt=%#v history=%#v grants=%#v", test.name, m.input.Value(), m.readScopePrompt, m.promptHistory, m.agent.ReadGrants())
			}
		})
	}
}

func TestOrdinaryPromptPreflightCanonicalizesTildeSymlinkWithSpaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tilde and symlink fixture uses Unix path syntax")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	target := filepath.Join(home, "notes with spaces.txt")
	alias := filepath.Join(home, "notes alias.txt")
	if err := os.WriteFile(target, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t)
	m.agent.SetWorkDir(workspace)
	t.Cleanup(m.agent.Close)
	draft := `read "~/notes alias.txt"`
	m.input.SetValue(draft)
	preflight := awaitCommandMessage[PromptPathPreflightResultMsg](t, commandMessages(m.submitInput()), 2*time.Second)
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(preflight.Grants) != 1 || preflight.Grants[0].Kind != agent.ReadGrantExactFile || preflight.Grants[0].Path != canonical {
		t.Fatalf("tilde symlink preflight = %#v, want %q", preflight.Grants, canonical)
	}
}

func TestOrdinaryPromptNoNewAuthorityStillSubmitsAfterPreflight(t *testing.T) {
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "inside.txt")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t)
	m.agent.SetWorkDir(workspace)
	t.Cleanup(m.agent.Close)
	draft := "read " + inside + " and " + filepath.Join(t.TempDir(), "missing.txt")
	m.input.SetValue(draft)
	preflight := awaitCommandMessage[PromptPathPreflightResultMsg](t, commandMessages(m.submitInput()), 2*time.Second)
	if len(preflight.Grants) != 0 {
		t.Fatalf("no-authority preflight = %#v", preflight.Grants)
	}
	updated, agentCmd := m.Update(preflight)
	m = updated.(*Model)
	if agentCmd == nil || m.state != StateWaiting || m.readScopePrompt != nil || len(m.promptHistory) != 1 {
		t.Fatalf("no-authority preflight did not submit: cmd=%v state=%v prompt=%#v history=%#v", agentCmd != nil, m.state, m.readScopePrompt, m.promptHistory)
	}
}

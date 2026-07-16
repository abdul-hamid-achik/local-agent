package ui

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/expertselector"
	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func expertProgressEvent(sequence uint64, phase expertteam.ProgressPhase, index int) expertteam.ProgressEvent {
	event := expertteam.ProgressEvent{
		Sequence: sequence, Phase: phase, Strategy: expertselector.StrategySwarm,
		Total: 2, Parallelism: 1, ExpertIndex: index,
	}
	switch sequence {
	case 1:
		event.ExpertIndex = -1
		event.Queued = 2
	case 2:
		event.Running, event.Queued = 1, 1
		event.Expert, event.Model, event.Location = "critic", "qwen3.5:2b", llm.OllamaModelLocationLocal
	case 3:
		event.Queued, event.Completed = 1, 1
		event.Expert, event.Model, event.Location = "critic", "qwen3.5:2b", llm.OllamaModelLocationLocal
		event.Status, event.EvalTokens = expertteam.ExpertCompleted, 42
	case 4:
		event.Running, event.Completed = 1, 1
		event.Expert, event.Model, event.Location = "verifier", "flash:cloud", llm.OllamaModelLocationCloud
	case 5:
		event.Completed, event.Failed = 1, 1
		event.Expert, event.Model, event.Location = "verifier", "flash:cloud", llm.OllamaModelLocationCloud
		event.Status, event.ErrorCode = expertteam.ExpertFailed, "no_visible_report"
	}
	return event
}

func updateExpertProgressTestModel(t *testing.T, model *Model, msg tea.Msg) *Model {
	t.Helper()
	updated, _ := model.Update(msg)
	result, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return result
}

func TestExpertProgressProducesOneBoundedInspectableSurface(t *testing.T) {
	m := newTestModel(t)
	m = updateExpertProgressTestModel(t, m, ToolCallStartMsg{
		ID: "experts-1", Name: "consult_experts", StartTime: time.Now(),
		Args: map[string]any{"objective": "private objective must not survive", "strategy": "swarm"},
	})
	if entry := m.toolEntries[0]; entry.Args != "" || entry.RawArgs != nil || entry.Collapsed {
		t.Fatalf("expert start retained private args or hid live surface: %#v", entry)
	}

	indices := []int{-1, 0, 0, 1, 1}
	for sequence := uint64(1); sequence <= 5; sequence++ {
		phase := []expertteam.ProgressPhase{
			expertteam.ProgressPlanned, expertteam.ProgressStarted, expertteam.ProgressCompleted,
			expertteam.ProgressStarted, expertteam.ProgressFailed,
		}[sequence-1]
		m = updateExpertProgressTestModel(t, m, ExpertProgressMsg{
			CallID: "experts-1", Event: expertProgressEvent(sequence, phase, indices[sequence-1]),
		})
	}

	progress := m.toolEntries[0].ExpertProgress
	if progress == nil || progress.Sequence != 5 || progress.Completed != 1 || progress.Failed != 1 {
		t.Fatalf("unexpected final progress: %#v", progress)
	}
	cardView := m.toolCardMgr.Cards[0]
	cardView.Expanded = !m.toolEntries[0].Collapsed
	view := cardView.View(96)
	for _, want := range []string{"critic", "qwen3.5:2b", "42 tok", "verifier", "flash:cloud", "no visible report"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expert surface missing %q:\n%s", want, view)
		}
	}
	activity, ok := m.currentWorkingActivity()
	if !ok || activity.label != "Consulting experts" || strings.Contains(activity.label, "Tool running") {
		t.Fatalf("expert activity = %#v, %v", activity, ok)
	}

	// A valid event for another call, a duplicate, a gap, and an ANSI-bearing
	// identity all fail closed without changing the accepted sequence.
	invalid := []ExpertProgressMsg{
		{CallID: "other", Event: expertProgressEvent(1, expertteam.ProgressPlanned, -1)},
		{CallID: "experts-1", Event: expertProgressEvent(5, expertteam.ProgressFailed, 1)},
		{CallID: "experts-1", Event: expertProgressEvent(7, expertteam.ProgressFailed, 1)},
	}
	malicious := expertProgressEvent(6, expertteam.ProgressStarted, 0)
	malicious.Expert = "critic\x1b[31m"
	invalid = append(invalid, ExpertProgressMsg{CallID: "experts-1", Event: malicious})
	for _, msg := range invalid {
		m = updateExpertProgressTestModel(t, m, msg)
	}
	if got := m.toolEntries[0].ExpertProgress.Sequence; got != 5 {
		t.Fatalf("invalid progress changed sequence to %d", got)
	}

	raw := "private objective must not survive\nprovider reasoning\n\x1b[31mraw report"
	m = updateExpertProgressTestModel(t, m, ToolCallResultMsg{
		ID: "experts-1", Name: "consult_experts", Result: raw, Duration: time.Second,
	})
	entry := m.toolEntries[0]
	if entry.Result != "" || entry.Args != "" || entry.ExpertProgress == nil {
		t.Fatalf("settled expert entry retained raw data or lost projection: %#v", entry)
	}
	if card := m.toolCardMgr.Cards[0]; card.Result != "" || card.State != ToolCardAttention {
		t.Fatalf("settled expert card = %#v", card)
	}
	// Progress arriving after settlement is stale, even with the right call ID.
	m = updateExpertProgressTestModel(t, m, ExpertProgressMsg{
		CallID: "experts-1", Event: expertProgressEvent(6, expertteam.ProgressStarted, 0),
	})
	if got := m.toolEntries[0].ExpertProgress.Sequence; got != 5 {
		t.Fatalf("post-settlement progress changed sequence to %d", got)
	}

	persisted := persistToolEntries(m.toolEntries)
	encoded, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private objective", "provider reasoning", "raw report", "\\u001b"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("persisted expert projection contains %q: %s", forbidden, encoded)
		}
	}
	restored := restoreToolEntries(persisted)
	if len(restored) != 1 || restored[0].ExpertProgress == nil || restored[0].Result != "" || restored[0].Args != "" {
		t.Fatalf("restored expert projection = %#v", restored)
	}
}

func TestExpertProgressRejectsUnsafeOrTamperedSnapshots(t *testing.T) {
	formatOnly := expertProgressEvent(2, expertteam.ProgressStarted, 0)
	formatOnly.Expert = "\u202e"
	if _, ok := normalizeExpertProgressEvent(formatOnly); ok {
		t.Fatal("format-control-only expert identity survived normalization")
	}
	overSequence := expertProgressEvent(6, expertteam.ProgressStarted, 0)
	overSequence.Expert, overSequence.Model = "critic", "qwen3.5:2b"
	if _, ok := normalizeExpertProgressEvent(overSequence); ok {
		t.Fatal("impossible progress sequence survived normalization")
	}

	state := newExpertProgressState(expertProgressEvent(1, expertteam.ProgressPlanned, -1))
	if state == nil {
		t.Fatal("planned state rejected")
	}
	started := expertProgressEvent(2, expertteam.ProgressStarted, 0)
	if !state.apply(started) {
		t.Fatal("valid start rejected")
	}
	tampered := cloneExpertProgressState(state)
	tampered.Experts[0].Model = "qwen\x1b[31m"
	if got := sanitizeExpertProgressState(tampered, false); got != nil {
		t.Fatalf("ANSI-bearing snapshot survived: %#v", got)
	}
	tampered = cloneExpertProgressState(state)
	tampered.Running, tampered.Queued = 0, 2
	if got := sanitizeExpertProgressState(tampered, false); got != nil {
		t.Fatalf("inconsistent counts survived: %#v", got)
	}
	maliciousPersisted := sanitizePersistedToolEntryArgs([]persistedToolEntry{{
		ID: "experts", Name: "consult_experts", Status: ToolStatusDone,
		Args: "objective=secret", Result: "raw report", ExpertProgress: tampered,
	}})
	if maliciousPersisted[0].Args != "" || maliciousPersisted[0].Result != "" || maliciousPersisted[0].ExpertProgress != nil {
		t.Fatalf("unsafe expert snapshot survived sanitization: %#v", maliciousPersisted[0])
	}
}

func TestExpertResultDropsIncompleteProgressProjection(t *testing.T) {
	m := newTestModel(t)
	m = updateExpertProgressTestModel(t, m, ToolCallStartMsg{
		ID: "experts-incomplete", Name: "consult_experts", StartTime: time.Now(),
		Args: map[string]any{"objective": "private", "strategy": "swarm"},
	})
	m = updateExpertProgressTestModel(t, m, ExpertProgressMsg{
		CallID: "experts-incomplete", Event: expertProgressEvent(1, expertteam.ProgressPlanned, -1),
	})
	m = updateExpertProgressTestModel(t, m, ToolCallResultMsg{
		ID: "experts-incomplete", Name: "consult_experts", Result: "untrusted aggregate report",
	})
	entry := m.toolEntries[0]
	if entry.ExpertProgress != nil || entry.Summary != "expert progress unavailable" || entry.Result != "" {
		t.Fatalf("incomplete progress was painted as settled: %#v", entry)
	}
	card := m.toolCardMgr.Cards[0]
	if card.ExpertProgress != nil || card.Result != "" {
		t.Fatalf("incomplete card retained progress or raw result: %#v", card)
	}
}

func TestExpertProgressCardIsNarrowAndCached(t *testing.T) {
	state := newExpertProgressState(expertProgressEvent(1, expertteam.ProgressPlanned, -1))
	for sequence, phase := range []expertteam.ProgressPhase{
		expertteam.ProgressStarted, expertteam.ProgressCompleted,
		expertteam.ProgressStarted, expertteam.ProgressFailed,
	} {
		if !state.apply(expertProgressEvent(uint64(sequence+2), phase, sequence/2)) {
			t.Fatalf("event %d rejected", sequence+2)
		}
	}
	card := NewToolCard("consult_experts", ToolCardGeneric, true)
	card.State = ToolCardAttention
	card.Expanded = true
	card.SetSummary(state.summary())
	card.setExpertProgress(state, 24)
	first := card.View(26)
	second := card.View(26)
	if first != second || card.expertProgressCache == "" || card.expertProgressCacheSequence != state.Sequence {
		t.Fatal("expert detail rendering was not stable and cached")
	}
	for _, line := range strings.Split(first, "\n") {
		if got := lipgloss.Width(line); got > 26 {
			t.Fatalf("narrow line width = %d: %q", got, line)
		}
	}
}

type expertProgressProbe struct {
	ready    chan struct{}
	messages chan ExpertProgressMsg
}

func (probe *expertProgressProbe) Init() tea.Cmd {
	close(probe.ready)
	return nil
}

func (probe *expertProgressProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if progress, ok := msg.(ExpertProgressMsg); ok {
		probe.messages <- progress
	}
	return probe, nil
}

func (probe *expertProgressProbe) View() tea.View { return tea.NewView("") }

func TestAdapterForwardsExpertProgressWithCallID(t *testing.T) {
	probe := &expertProgressProbe{ready: make(chan struct{}), messages: make(chan ExpertProgressMsg, 1)}
	program := tea.NewProgram(
		probe, tea.WithInput(strings.NewReader("")), tea.WithOutput(io.Discard),
		tea.WithoutRenderer(), tea.WithoutSignalHandler(),
	)
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	select {
	case <-probe.ready:
	case <-time.After(time.Second):
		t.Fatal("probe program did not start")
	}

	event := expertProgressEvent(1, expertteam.ProgressPlanned, -1)
	NewAdapter(program).ExpertProgress("experts-call", event)
	select {
	case got := <-probe.messages:
		if got.CallID != "experts-call" || got.Event != event {
			t.Fatalf("forwarded progress = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("adapter did not forward expert progress")
	}
	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe program did not stop")
	}
}

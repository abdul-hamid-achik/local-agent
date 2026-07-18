package ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/expertselector"
	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func settledAgentProgress(label string) *ExpertProgressState {
	return &ExpertProgressState{
		Sequence: 3, Strategy: expertselector.StrategyTeam, Total: 1, Parallelism: 1,
		Completed: 1,
		Experts: []ExpertProgressItem{{
			Index: 0, Expert: label, Model: "safe-model",
			Location: llm.OllamaModelLocationLocal,
			Phase:    expertteam.ProgressCompleted, Status: expertteam.ExpertCompleted,
			EvalTokens: 17,
		}},
	}
}

func liveAgentProgress(label string) *ExpertProgressState {
	return &ExpertProgressState{
		Sequence: 2, Strategy: expertselector.StrategySwarm, Total: 2, Parallelism: 1,
		Running: 1, Queued: 1,
		Experts: []ExpertProgressItem{{
			Index: 0, Expert: label, Model: "safe-model",
			Location: llm.OllamaModelLocationCloud,
			Phase:    expertteam.ProgressStarted,
		}, {}},
	}
}

func agentGroupFixture(index int, lifecycle BlockLifecycle, status ToolStatus, progress *ExpertProgressState) (ChatEntry, ToolEntry) {
	return ChatEntry{
			BlockID:   BlockID(fmt.Sprintf("agent_group_%03d", index)),
			TurnID:    TurnID(fmt.Sprintf("turn_%03d", index)),
			Revision:  1,
			Lifecycle: lifecycle,
			Kind:      "tool_group",
			ToolIndex: index,
		}, ToolEntry{
			ID:             fmt.Sprintf("provider-call-secret-%03d", index),
			Name:           "consult_experts",
			Status:         status,
			ExpertProgress: progress,
		}
}

func TestAgentSurfaceProjectsCausalGroupsWithGloballyUniqueStableNodeIDs(t *testing.T) {
	firstEntry, firstTool := agentGroupFixture(0, BlockSettled, ToolStatusDone, settledAgentProgress("critic"))
	secondEntry, secondTool := agentGroupFixture(1, BlockSettled, ToolStatusDone, settledAgentProgress("verifier"))
	surface, err := projectAgentSurface(
		[]ChatEntry{firstEntry, {Kind: "assistant", Content: "ignored"}, secondEntry},
		[]ToolEntry{firstTool, secondTool},
	)
	if err != nil {
		t.Fatalf("projectAgentSurface: %v", err)
	}
	if len(surface.Groups) != 2 || surface.OmittedGroups != 0 {
		t.Fatalf("surface bounds = %d groups, %d omitted", len(surface.Groups), surface.OmittedGroups)
	}
	if surface.Groups[0].ID != firstEntry.BlockID || surface.Groups[1].ID != secondEntry.BlockID {
		t.Fatalf("groups lost causal order: %#v", surface.Groups)
	}
	firstNode := surface.Groups[0].Nodes[0]
	secondNode := surface.Groups[1].Nodes[0]
	if firstNode.ID == secondNode.ID {
		t.Fatalf("node IDs collide across groups: %q", firstNode.ID)
	}
	if strings.Contains(firstNode.ID, firstTool.ID) || strings.Contains(secondNode.ID, secondTool.ID) {
		t.Fatalf("node ID inherited provider call identity: %q / %q", firstNode.ID, secondNode.ID)
	}
	reprojected, err := projectAgentSurface([]ChatEntry{firstEntry, secondEntry}, []ToolEntry{firstTool, secondTool})
	if err != nil {
		t.Fatalf("reproject: %v", err)
	}
	if got := reprojected.Groups[0].Nodes[0].ID; got != firstNode.ID {
		t.Fatalf("stable node ID changed: %q -> %q", firstNode.ID, got)
	}

	tampered := surface
	tampered.Groups = append([]AgentGroupProjection(nil), surface.Groups...)
	tampered.Groups[1].Nodes = append([]WorkNode(nil), surface.Groups[1].Nodes...)
	tampered.Groups[1].Nodes[0].ID = firstNode.ID
	if tampered.valid() {
		t.Fatal("surface accepted a cross-group node ID collision")
	}
}

func TestAgentSurfaceProjectionDoesNotMutateInputs(t *testing.T) {
	entry, tool := agentGroupFixture(0, BlockLive, ToolStatusRunning, liveAgentProgress("critic"))
	entries := []ChatEntry{entry}
	tools := []ToolEntry{tool}

	wantEntries := append([]ChatEntry(nil), entries...)
	wantTools := append([]ToolEntry(nil), tools...)
	wantTools[0].ExpertProgress = cloneExpertProgressState(tools[0].ExpertProgress)

	surface, err := projectAgentSurface(entries, tools)
	if err != nil {
		t.Fatalf("projectAgentSurface: %v", err)
	}
	if !reflect.DeepEqual(entries, wantEntries) {
		t.Fatalf("projection mutated transcript input:\n got: %#v\nwant: %#v", entries, wantEntries)
	}
	if !reflect.DeepEqual(tools, wantTools) {
		t.Fatalf("projection mutated tool input:\n got: %#v\nwant: %#v", tools, wantTools)
	}
	if tools[0].ExpertProgress.Experts[1].Location != "" {
		t.Fatalf("queued-slot normalization leaked into input: %#v", tools[0].ExpertProgress.Experts[1])
	}

	tools[0].ExpertProgress.Experts[0].Expert = "mutated-after-projection"
	if got := surface.Groups[0].Nodes[0].Label; got != "critic" {
		t.Fatalf("surface retained mutable input alias: got node label %q", got)
	}
}

func TestAgentSurfaceNodeIdentityAndOrderSurviveLifecycleChanges(t *testing.T) {
	entry, runningTool := agentGroupFixture(0, BlockLive, ToolStatusRunning, liveAgentProgress("critic"))
	live, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{runningTool})
	if err != nil {
		t.Fatalf("project live: %v", err)
	}
	if got := []WorkNodeStatus{live.Groups[0].Nodes[0].Status, live.Groups[0].Nodes[1].Status}; !reflect.DeepEqual(got, []WorkNodeStatus{WorkNodeRunning, WorkNodeQueued}) {
		t.Fatalf("live node order/status = %v", got)
	}

	settled := &ExpertProgressState{
		Sequence: 5, Strategy: expertselector.StrategySwarm, Total: 2, Parallelism: 1,
		Completed: 1, Failed: 1,
		Experts: []ExpertProgressItem{
			{
				Index: 0, Expert: "critic", Model: "safe-model",
				Location: llm.OllamaModelLocationCloud,
				Phase:    expertteam.ProgressCompleted, Status: expertteam.ExpertCompleted,
				EvalTokens: 23,
			},
			{
				Index: 1, Expert: "verifier", Model: "safe-model",
				Location: llm.OllamaModelLocationRemote,
				Phase:    expertteam.ProgressFailed, Status: expertteam.ExpertFailed,
				FailureCode: "timed_out",
			},
		},
	}
	entry.Lifecycle, entry.Revision = BlockSettled, 2
	runningTool.Status, runningTool.ExpertProgress = ToolStatusDone, settled
	done, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{runningTool})
	if err != nil {
		t.Fatalf("project settled: %v", err)
	}
	if done.Groups[0].Nodes[0].Index != 0 || done.Groups[0].Nodes[1].Index != 1 {
		t.Fatalf("settled nodes reordered: %#v", done.Groups[0].Nodes)
	}
	for index := range live.Groups[0].Nodes {
		if live.Groups[0].Nodes[index].ID != done.Groups[0].Nodes[index].ID {
			t.Fatalf("node %d identity changed across lifecycle", index)
		}
	}
	if done.Groups[0].Completed != 1 || done.Groups[0].Failed != 1 ||
		done.Groups[0].Running != 0 || done.Groups[0].Queued != 0 {
		t.Fatalf("settled counts = %#v", done.Groups[0])
	}

	reordered := done.Groups[0]
	reordered.Nodes = append([]WorkNode(nil), reordered.Nodes...)
	reordered.Nodes[0], reordered.Nodes[1] = reordered.Nodes[1], reordered.Nodes[0]
	if reordered.valid() {
		t.Fatal("group accepted node order that diverges from stable scheduler indexes")
	}
}

func TestAgentSurfaceIsBoundedAndRetainsEveryLiveGroup(t *testing.T) {
	const total = maxAgentSurfaceGroups + 2
	entries := make([]ChatEntry, total)
	tools := make([]ToolEntry, total)
	for index := range total {
		lifecycle, status := BlockSettled, ToolStatusDone
		progress := settledAgentProgress(fmt.Sprintf("expert-%d", index))
		if index == 0 {
			lifecycle, status = BlockLive, ToolStatusRunning
			progress = liveAgentProgress("active")
		}
		entries[index], tools[index] = agentGroupFixture(index, lifecycle, status, progress)
	}
	surface, err := projectAgentSurface(entries, tools)
	if err != nil {
		t.Fatalf("projectAgentSurface: %v", err)
	}
	if len(surface.Groups) != maxAgentSurfaceGroups || surface.OmittedGroups != 2 {
		t.Fatalf("bounded surface = %d groups, %d omitted", len(surface.Groups), surface.OmittedGroups)
	}
	if surface.Groups[0].ID != entries[0].BlockID || surface.Groups[0].Lifecycle != BlockLive {
		t.Fatalf("old live group was omitted: first = %#v", surface.Groups[0])
	}
	if got, want := surface.Groups[len(surface.Groups)-1].ID, entries[len(entries)-1].BlockID; got != want {
		t.Fatalf("newest settled group = %q, want %q", got, want)
	}
}

func TestAgentSurfaceHandlesUnavailableAndRestoredInterruptedProgressWithoutFabrication(t *testing.T) {
	t.Run("awaiting plan", func(t *testing.T) {
		entry, tool := agentGroupFixture(0, BlockLive, ToolStatusRunning, nil)
		surface, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
		if err != nil {
			t.Fatalf("projectAgentSurface: %v", err)
		}
		group := surface.Groups[0]
		if group.ProgressAvailable || group.Interrupted || group.Total != 0 || len(group.Nodes) != 0 {
			t.Fatalf("missing progress was fabricated: %#v", group)
		}
	})

	t.Run("settled restore", func(t *testing.T) {
		entry, tool := agentGroupFixture(0, BlockSettled, ToolStatusDone, settledAgentProgress("critic"))
		surface, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
		if err != nil {
			t.Fatalf("projectAgentSurface: %v", err)
		}
		if !surface.Groups[0].ProgressAvailable || surface.Groups[0].Interrupted ||
			surface.Groups[0].Nodes[0].Status != WorkNodeCompleted {
			t.Fatalf("settled restore projection = %#v", surface.Groups[0])
		}
	})

	for _, lifecycle := range []BlockLifecycle{BlockLive, BlockFailed} {
		t.Run(fmt.Sprintf("live restore lifecycle %d", lifecycle), func(t *testing.T) {
			entry, tool := agentGroupFixture(0, lifecycle, ToolStatusError, nil)
			tool.Projection = ecosystem.ToolProjection{
				Transport: ecosystem.TransportFailed,
				Domain:    ecosystem.DomainUnknown,
				Evidence:  ecosystem.EvidenceNone,
			}
			surface, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
			if err != nil {
				t.Fatalf("projectAgentSurface: %v", err)
			}
			group := surface.Groups[0]
			if group.Lifecycle != BlockFailed || !group.Interrupted || group.ProgressAvailable ||
				group.Running != 0 || group.Queued != 0 || len(group.Nodes) != 0 {
				t.Fatalf("restored live projection fabricated a terminal child outcome: %#v", group)
			}
		})
	}
}

func TestAgentSurfaceFailsClosedOnInconsistentAuthority(t *testing.T) {
	validEntry, validTool := agentGroupFixture(0, BlockSettled, ToolStatusDone, settledAgentProgress("critic"))
	tests := []struct {
		name    string
		entries []ChatEntry
		tools   []ToolEntry
	}{
		{
			name: "missing tool",
			entries: []ChatEntry{{
				BlockID: "group", TurnID: "turn", Revision: 1, Lifecycle: BlockSettled,
				Kind: "tool_group", ToolIndex: 1,
			}},
		},
		{
			name:    "invalid group ID",
			entries: []ChatEntry{{BlockID: "bad/id", TurnID: "turn", Revision: 1, Lifecycle: BlockSettled, Kind: "tool_group", ToolIndex: 0}},
			tools:   []ToolEntry{validTool},
		},
		{
			name: "duplicate group identity",
			entries: []ChatEntry{
				validEntry,
				{BlockID: validEntry.BlockID, TurnID: "turn_2", Revision: 1, Lifecycle: BlockSettled, Kind: "tool_group", ToolIndex: 1},
			},
			tools: []ToolEntry{validTool, func() ToolEntry {
				_, tool := agentGroupFixture(1, BlockSettled, ToolStatusDone, settledAgentProgress("verifier"))
				return tool
			}()},
		},
		{
			name:    "lifecycle mismatch",
			entries: []ChatEntry{{BlockID: "group", TurnID: "turn", Revision: 1, Lifecycle: BlockLive, Kind: "tool_group", ToolIndex: 0}},
			tools:   []ToolEntry{validTool},
		},
		{
			name:    "tampered progress",
			entries: []ChatEntry{validEntry},
			tools: []ToolEntry{func() ToolEntry {
				tool := validTool
				tool.ExpertProgress = cloneExpertProgressState(validTool.ExpertProgress)
				tool.ExpertProgress.Experts[0].Model = "model\x1b[31m"
				return tool
			}()},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if surface, err := projectAgentSurface(test.entries, test.tools); err == nil {
				t.Fatalf("unsafe surface survived: %#v", surface)
			}
		})
	}
}

func TestAgentSurfaceProjectionHasNoPrivateAuthority(t *testing.T) {
	entry, tool := agentGroupFixture(0, BlockSettled, ToolStatusDone, settledAgentProgress("critic"))
	entry.Content = "private objective in transcript field"
	tool.Args = "objective=secret"
	tool.Result = "private report and reasoning"
	tool.RawArgs = map[string]any{"prompt": "secret"}
	tool.ID = "provider-call-secret"
	tool.Projection = ecosystem.ToolProjection{
		Transport: ecosystem.TransportSucceeded,
		Domain:    ecosystem.DomainUnknown,
		Route:     ecosystem.ToolRoute{CallID: "downstream-call-secret"},
	}
	surface, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
	if err != nil {
		t.Fatalf("projectAgentSurface: %v", err)
	}
	encoded, err := json.Marshal(surface)
	if err != nil {
		t.Fatalf("marshal safe surface: %v", err)
	}
	for _, forbidden := range []string{
		"private objective", "objective=secret", "private report", "reasoning",
		"provider-call-secret", "downstream-call-secret", "prompt", "raw_args",
		"result", "structured_content", "path", "metadata",
	} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("agent surface serialized forbidden authority %q: %s", forbidden, encoded)
		}
	}

	typ := reflect.TypeFor[AgentGroupProjection]()
	forbiddenFields := map[string]bool{
		"prompt": true, "objective": true, "report": true, "reasoning": true,
		"error": true, "path": true, "transcript": true, "metadata": true,
		"args": true, "result": true, "rawargs": true, "structuredcontent": true,
	}
	for index := range typ.NumField() {
		field := typ.Field(index)
		if forbiddenFields[strings.ToLower(field.Name)] {
			t.Fatalf("AgentGroupProjection exposes forbidden field %q", field.Name)
		}
	}
}

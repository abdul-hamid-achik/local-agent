package ui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestWorkNodeValidationIsBoundedAndStatusSpecific(t *testing.T) {
	valid := WorkNode{
		ID: "expert-00", Index: 0, Label: "critic", Model: "qwen3.5:2b",
		Location: llm.OllamaModelLocationLocal, Status: WorkNodeRunning,
	}
	if !valid.valid(2) {
		t.Fatal("valid running node rejected")
	}

	tests := []struct {
		name string
		edit func(*WorkNode)
	}{
		{"unknown status", func(node *WorkNode) { node.Status = 0 }},
		{"invalid id", func(node *WorkNode) { node.ID = "private/path" }},
		{"out of range index", func(node *WorkNode) { node.Index = 2 }},
		{"oversized label", func(node *WorkNode) { node.Label = strings.Repeat("x", maxWorkNodeLabelBytes+1) }},
		{"oversized model", func(node *WorkNode) { node.Model = strings.Repeat("x", maxWorkNodeModelBytes+1) }},
		{"running tokens", func(node *WorkNode) { node.EvalTokens = 1 }},
		{"raw failure", func(node *WorkNode) {
			node.Status, node.FailureCode = WorkNodeFailed, "provider said secret"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := valid
			test.edit(&node)
			if node.valid(2) {
				t.Fatalf("unsafe node survived validation: %#v", node)
			}
		})
	}

	queued := WorkNode{
		ID: "expert-01", Index: 1, Status: WorkNodeQueued,
		Location: llm.OllamaModelLocationUnknown,
	}
	if !queued.valid(2) {
		t.Fatal("valid queued node rejected")
	}
	queued.Label = "untrusted"
	if queued.valid(2) {
		t.Fatal("queued node accepted child-controlled identity")
	}
}

func TestExpertProgressAdapterIsPureSafeAndIndexStable(t *testing.T) {
	state := &ExpertProgressState{
		Sequence: 5, Strategy: "swarm", Total: 4, Parallelism: 2,
		Running: 1, Queued: 1, Completed: 1, Failed: 1,
		Experts: []ExpertProgressItem{
			{Index: 0, Expert: "done", Model: "m0", Location: llm.OllamaModelLocationLocal, Phase: expertteam.ProgressCompleted, Status: expertteam.ExpertCompleted, EvalTokens: 9},
			{Index: 1, Expert: "active", Model: "m1", Location: llm.OllamaModelLocationCloud, Phase: expertteam.ProgressStarted},
			{},
			{Index: 3, Expert: "failed", Model: "m3", Location: llm.OllamaModelLocationRemote, Phase: expertteam.ProgressFailed, Status: expertteam.ExpertFailed, FailureCode: "timed_out"},
		},
	}
	before := cloneExpertProgressState(state)
	nodes, ok := workNodesFromExpertProgress(state)
	if !ok {
		t.Fatal("valid progress state rejected")
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("adapter mutated its input:\nbefore %#v\nafter  %#v", before, state)
	}
	got := make([]WorkNodeStatus, len(nodes))
	for index := range nodes {
		got[index] = nodes[index].Status
	}
	want := []WorkNodeStatus{WorkNodeCompleted, WorkNodeRunning, WorkNodeQueued, WorkNodeFailed}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status order = %v, want %v", got, want)
	}
	if nodes[0].ID != "expert-00" || nodes[2].ID != "expert-02" {
		t.Fatalf("IDs are not host-derived and stable: %#v", nodes)
	}
}

func TestWorkNodeOrderingStaysStableAcrossStatusChanges(t *testing.T) {
	nodes := []WorkNode{
		{ID: "node-z", Index: 2, Status: WorkNodeRunning},
		{ID: "node-b", Index: 1, Status: WorkNodeFailed},
		{ID: "node-a", Index: 1, Status: WorkNodeAttention},
		{ID: "node-c", Index: 0, Status: WorkNodeCompleted},
	}
	ordered := orderedWorkNodes(nodes)
	got := []string{ordered[0].ID, ordered[1].ID, ordered[2].ID, ordered[3].ID}
	want := []string{"node-c", "node-a", "node-b", "node-z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered IDs = %v, want %v", got, want)
	}
	if nodes[0].ID != "node-z" {
		t.Fatal("ordering mutated caller slice")
	}
}

func TestWorkNodeProjectionHasNoPrivateAuthority(t *testing.T) {
	typ := reflect.TypeOf(WorkNode{})
	forbiddenFields := map[string]bool{
		"prompt": true, "objective": true, "report": true, "reasoning": true,
		"error": true, "path": true, "transcript": true, "metadata": true,
	}
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if forbiddenFields[strings.ToLower(field.Name)] {
			t.Fatalf("WorkNode exposes forbidden field %q", field.Name)
		}
	}

	encoded, err := json.Marshal(WorkNode{
		ID: "expert-00", Index: 0, Label: "critic", Model: "safe-model",
		Location: llm.OllamaModelLocationLocal, Status: WorkNodeFailed,
		FailureCode: "timed_out",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"objective", "report", "raw_error", "reasoning", "transcript", "path", "metadata"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("safe projection serialized forbidden authority %q: %s", forbidden, encoded)
		}
	}
}

func TestExpertProgressDetailsKeepIndexOrderWithoutChangingGrammar(t *testing.T) {
	state := &ExpertProgressState{
		Sequence: 4, Strategy: "team", Total: 3, Parallelism: 1,
		Running: 1, Queued: 1, Failed: 1,
		Experts: []ExpertProgressItem{
			{Index: 0, Expert: "active", Model: "m0", Location: llm.OllamaModelLocationLocal, Phase: expertteam.ProgressStarted},
			{},
			{Index: 2, Expert: "failed", Model: "m2", Location: llm.OllamaModelLocationCloud, Phase: expertteam.ProgressFailed, Status: expertteam.ExpertFailed, FailureCode: "model_unavailable"},
		},
	}
	view := state.renderDetails(80, NewToolCardStyles(true))
	failedAt, runningAt, queuedAt := strings.Index(view, "failed"), strings.Index(view, "active"), strings.Index(view, "1 more queued")
	if failedAt < 0 || runningAt < 0 || queuedAt < 0 ||
		runningAt >= queuedAt || queuedAt >= failedAt {
		t.Fatalf("unexpected work-node order or grammar:\n%s", view)
	}
}

func TestExpertProgressAdapterDistinguishesCancellationFromFailure(t *testing.T) {
	state := &ExpertProgressState{
		Sequence: 3, Strategy: "team", Total: 1, Parallelism: 1,
		Failed: 1,
		Experts: []ExpertProgressItem{{
			Index: 0, Expert: "critic", Model: "m0",
			Location: llm.OllamaModelLocationLocal,
			Phase:    expertteam.ProgressFailed, Status: expertteam.ExpertFailed,
			FailureCode: "cancelled",
		}},
	}
	nodes, ok := workNodesFromExpertProgress(state)
	if !ok || len(nodes) != 1 || nodes[0].Status != WorkNodeCancelled {
		t.Fatalf("cancelled progress projection = %#v, ok=%v", nodes, ok)
	}
	if view := state.renderDetails(80, NewToolCardStyles(true)); !strings.Contains(view, "cancelled") {
		t.Fatalf("cancelled work node lacks distinct presentation:\n%s", view)
	}
}

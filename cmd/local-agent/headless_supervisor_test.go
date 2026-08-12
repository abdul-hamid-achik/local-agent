package main

import (
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/goal"
)

func TestHeadlessRefusesCortexLinkedGoalBeforeDispatch(t *testing.T) {
	if headlessRefusesCortexLinkedGoal(goal.Snapshot{}) {
		t.Fatal("an unlinked goal was refused")
	}
	if !headlessRefusesCortexLinkedGoal(goal.Snapshot{
		Cortex: goal.CortexCorrelation{TaskID: "task_1", Actor: "local-agent"},
	}) {
		t.Fatal("a Cortex-linked goal was admitted to a headless path with no evaluator")
	}
}

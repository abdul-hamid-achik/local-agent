package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/goal"
	"github.com/abdul-hamid-achik/local-agent/internal/goaladvisor"
)

const goalControlPlaneTimeout = 2 * time.Second

func (m *Model) recordCortexDecisionControlItem(snapshot goal.Snapshot, advice goaladvisor.Advice) error {
	if m.sessionStore == nil {
		// Pure UI embeddings may omit persistence. The production application
		// requires it before a Goal Runtime can be created.
		return nil
	}
	if m.executionLease == nil {
		return fmt.Errorf("durable Cortex decision requires the active session lease")
	}
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		return fmt.Errorf("resolve Cortex decision workspace: %w", err)
	}
	taskID := fallbackGoalText(advice.TaskID, snapshot.Cortex.TaskID)
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("cortex decision has no task identity")
	}
	identityHash := controlplane.HashText(fmt.Sprintf(
		"cortex-decision\x00%d\x00%s\x00%s\x00%d",
		snapshot.SessionID, snapshot.ID, taskID, advice.Revision,
	))
	payload, payloadDigest, err := controlplane.MarshalDocument(map[string]any{
		"task_id":          taskID,
		"revision":         advice.Revision,
		"phase":            boundGoalText(advice.Phase, controlplane.MaxDetailBytes),
		"summary":          boundGoalText(advice.Summary, controlplane.MaxSummaryBytes),
		"pending_decision": true,
	})
	if err != nil {
		return fmt.Errorf("encode Cortex decision: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), goalControlPlaneTimeout)
	defer cancel()
	_, _, err = m.sessionStore.AppendControlItem(ctx, m.executionLease, controlplane.Item{
		ItemID:         "ctrl_cortex_" + identityHash[:32],
		IdempotencyKey: "ctrlidem_cortex_" + identityHash[:32],
		Kind:           controlplane.KindCortexDecision,
		Identity: controlplane.Identity{
			SessionID: snapshot.SessionID, WorkspaceID: workspaceID,
			GoalID: snapshot.ID,
		},
		ExternalID:    taskID,
		Summary:       boundGoalText(fallbackGoalText(advice.Summary, "Cortex requires a human decision"), controlplane.MaxSummaryBytes),
		PayloadJSON:   payload,
		PayloadSHA256: payloadDigest,
	})
	if err != nil {
		return fmt.Errorf("persist Cortex decision: %w", err)
	}
	return nil
}

func (m *Model) resolveCortexDecisionControlItems(snapshot goal.Snapshot, advice goaladvisor.Advice) error {
	if m.sessionStore == nil {
		return nil
	}
	if m.executionLease == nil {
		return fmt.Errorf("durable Cortex decision resolution requires the active session lease")
	}
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		return fmt.Errorf("resolve Cortex decision workspace: %w", err)
	}
	taskID := fallbackGoalText(advice.TaskID, snapshot.Cortex.TaskID)
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("cortex decision resolution has no task identity")
	}
	if snapshot.Cortex.TaskID != "" && taskID != snapshot.Cortex.TaskID {
		return fmt.Errorf("cortex decision resolution task %q does not match goal task %q", taskID, snapshot.Cortex.TaskID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), goalControlPlaneTimeout)
	defer cancel()
	states, err := m.sessionStore.ListControlStates(ctx, controlplane.Query{
		SessionID: snapshot.SessionID, WorkspaceID: workspaceID,
		Kind: controlplane.KindCortexDecision, GoalID: snapshot.ID,
		PendingOnly: true, Limit: controlplane.MaxListLimit,
	})
	if err != nil {
		return fmt.Errorf("list pending Cortex decisions: %w", err)
	}
	if len(states) == 0 {
		return nil
	}
	for _, state := range states {
		if err := state.Item.Validate(); err != nil {
			return fmt.Errorf("validate pending Cortex decision %s: %w", state.Item.ItemID, err)
		}
		if state.Item.ExternalID != taskID {
			return fmt.Errorf("pending Cortex decision %s belongs to task %q, not %q", state.Item.ItemID, state.Item.ExternalID, taskID)
		}
		evidence, evidenceDigest, encodeErr := controlplane.MarshalDocument(map[string]any{
			"item_id":                    state.Item.ItemID,
			"decision_payload_sha256":    state.Item.PayloadSHA256,
			"task_id":                    taskID,
			"revision":                   advice.Revision,
			"phase":                      boundGoalText(advice.Phase, controlplane.MaxDetailBytes),
			"summary":                    boundGoalText(advice.Summary, controlplane.MaxSummaryBytes),
			"decision_no_longer_pending": true,
		})
		if encodeErr != nil {
			return fmt.Errorf("encode Cortex decision resolution: %w", encodeErr)
		}
		resolutionHash := controlplane.HashText(fmt.Sprintf(
			"cortex-resolution\x00%s\x00%s",
			state.Item.ItemID, evidenceDigest,
		))
		_, _, err := m.sessionStore.ResolveControlItem(ctx, m.executionLease, controlplane.Resolution{
			ResolutionID:   "ctrlres_cortex_" + resolutionHash[:32],
			IdempotencyKey: "ctrlidem_cortex_resolution_" + resolutionHash[:32],
			ItemID:         state.Item.ItemID,
			SessionID:      snapshot.SessionID,
			WorkspaceID:    workspaceID,
			Outcome:        controlplane.OutcomeAnswered,
			EvidenceJSON:   evidence,
			EvidenceSHA256: evidenceDigest,
			ResolvedBy:     goalActor,
			Detail:         boundGoalText("fresh Cortex status no longer reports a pending decision", controlplane.MaxDetailBytes),
		})
		if err != nil {
			return fmt.Errorf("resolve Cortex decision %s: %w", state.Item.ItemID, err)
		}
	}
	return nil
}

func (m *Model) protectControlPlaneFailure(operation string, controlErr error) {
	if controlErr == nil || m.goalRuntime == nil {
		return
	}
	current, snapshotErr := m.goalRuntime.Snapshot(context.Background())
	var blockErr error
	if snapshotErr == nil && !current.State.Terminal() {
		blockErr = ensureGoalBlock(m.goalRuntime, goal.BlockDecision,
			fallbackGoalText(current.Cortex.TaskID, current.ID),
			"durable Cortex decision evidence could not be recorded",
		)
	}
	persistErr := m.persistGoalSession()
	m.appendGoalError(operation + ": " + errors.Join(controlErr, snapshotErr, blockErr, persistErr).Error())
}

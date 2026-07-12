package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/reconciliation"
)

var (
	ErrExecutionReconciliationCorrupt = errors.New("execution reconciliation projection is corrupt")
	ErrExecutionHazardOverflow        = errors.New("execution hazard projection exceeds its safe bound")
)

const (
	effectiveProjectionPageSize = 128
	maxEffectiveProjectionScan  = 10_000
)

type effectiveExecutionKind uint8

const (
	effectiveUnresolved effectiveExecutionKind = iota + 1
	effectiveRecovery
	effectiveReconciliationTargets
)

type effectiveExecutionQuery struct {
	kind         effectiveExecutionKind
	sessionID    int64
	workspaceID  string
	turnID       string
	afterEventID int64
}

// listEffectiveExecutionStates overlays typed control-plane reconciliation on
// the immutable raw ledger. It pages raw candidates and validates each exact
// receipt before skipping it, so reconciled rows cannot consume the caller's
// limit and hide a later unresolved execution.
func (s *Store) listEffectiveExecutionStates(ctx context.Context, query effectiveExecutionQuery, limit int, failOnOverflow bool) ([]execution.State, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin effective execution projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateExecutionSessionScope(ctx, tx, query.sessionID, query.workspaceID); err != nil {
		return nil, err
	}

	wanted := limit
	if failOnOverflow {
		wanted++
	}
	states := make([]execution.State, 0, wanted)
	offset := 0
	for len(states) < wanted {
		if offset >= maxEffectiveProjectionScan {
			return nil, fmt.Errorf("%w: scanned at least %d raw candidates", ErrExecutionHazardOverflow, maxEffectiveProjectionScan)
		}
		pageLimit := effectiveProjectionPageSize
		if remaining := maxEffectiveProjectionScan - offset; pageLimit > remaining {
			pageLimit = remaining
		}
		page, err := queryRawExecutionProjectionPage(ctx, tx, query, pageLimit, offset)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		offset += len(page)
		for _, state := range page {
			if executionStateCanBeReconciled(state) {
				reconciled, err := executionStateEffectivelyReconciled(ctx, tx, state)
				if err != nil {
					return nil, err
				}
				if reconciled {
					continue
				}
			}
			states = append(states, state)
			if len(states) == wanted {
				break
			}
		}
		if len(page) < pageLimit {
			break
		}
	}
	if failOnOverflow && len(states) > limit {
		return nil, fmt.Errorf("%w: more than %d effective hazards", ErrExecutionHazardOverflow, limit)
	}
	if len(states) > limit {
		states = states[:limit]
	}
	return states, nil
}

func queryRawExecutionProjectionPage(ctx context.Context, tx *sql.Tx, query effectiveExecutionQuery, limit, offset int) ([]execution.State, error) {
	var statement string
	var args []any
	switch query.kind {
	case effectiveUnresolved:
		statement = `
			WITH ranked AS (
				SELECT e.*,
				       COUNT(*) OVER (PARTITION BY execution_id) AS event_count,
				       ROW_NUMBER() OVER (PARTITION BY execution_id ORDER BY id DESC) AS latest_rank
				  FROM execution_events e
				 WHERE session_id = ? AND workspace_id = ?
			)
			SELECT ` + executionEventColumns + `, event_count
			  FROM ranked
			 WHERE latest_rank = 1
			   AND event_type NOT IN ('denied', 'completed', 'failed', 'cancelled')
			 ORDER BY CASE
			              WHEN event_type = 'outcome_unknown' THEN 0
			              WHEN event_type = 'started' AND effect_class != 'read_only' THEN 0
			              ELSE 1
			          END,
			          id ASC
			 LIMIT ? OFFSET ?`
		args = []any{query.sessionID, query.workspaceID, limit, offset}
	case effectiveRecovery:
		statement = `
			WITH ranked AS (
				SELECT e.*,
				       COUNT(*) OVER (PARTITION BY execution_id) AS event_count,
				       ROW_NUMBER() OVER (PARTITION BY execution_id ORDER BY id DESC) AS latest_rank
				  FROM execution_events e
				 WHERE session_id = ? AND workspace_id = ?
			)
			SELECT ` + executionEventColumns + `, event_count
			  FROM ranked
			 WHERE latest_rank = 1
			   AND (
			       event_type = 'outcome_unknown'
			       OR (event_type = 'started' AND effect_class != 'read_only')
			       OR (event_type = 'completed' AND effect_class != 'read_only' AND id > ?)
			   )
			 ORDER BY CASE
			              WHEN event_type = 'outcome_unknown' THEN 0
			              WHEN event_type = 'started' AND effect_class != 'read_only' THEN 0
			              ELSE 1
			          END,
			          id ASC
			 LIMIT ? OFFSET ?`
		args = []any{query.sessionID, query.workspaceID, query.afterEventID, limit, offset}
	case effectiveReconciliationTargets:
		statement = `
			WITH ranked AS (
				SELECT e.*,
				       COUNT(*) OVER (PARTITION BY execution_id) AS event_count,
				       ROW_NUMBER() OVER (PARTITION BY execution_id ORDER BY id DESC) AS latest_rank
				  FROM execution_events e
				 WHERE session_id = ? AND workspace_id = ? AND turn_id = ?
			)
			SELECT ` + executionEventColumns + `, event_count
			  FROM ranked
			 WHERE latest_rank = 1
			   AND (
			       event_type = 'outcome_unknown'
			       OR (event_type = 'started' AND effect_class != 'read_only')
			   )
			 ORDER BY id ASC
			 LIMIT ? OFFSET ?`
		args = []any{query.sessionID, query.workspaceID, query.turnID, limit, offset}
	default:
		return nil, errors.New("invalid effective execution projection kind")
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query raw execution projection: %w", err)
	}
	defer func() { _ = rows.Close() }()
	states := make([]execution.State, 0, limit)
	for rows.Next() {
		event, count, err := scanExecutionState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, execution.State{Identity: event.Identity, Latest: event, EventCount: count})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read raw execution projection: %w", err)
	}
	return states, nil
}

func executionStateCanBeReconciled(state execution.State) bool {
	return state.Latest.Type == execution.EventOutcomeUnknown ||
		(state.Latest.Type == execution.EventStarted && state.Identity.EffectClass != execution.EffectReadOnly)
}

func executionStateEffectivelyReconciled(ctx context.Context, tx *sql.Tx, state execution.State) (bool, error) {
	rows, err := tx.QueryContext(ctx, controlStateSelect+`
		WHERE i.session_id = ? AND i.workspace_id = ? AND i.execution_id = ?
		  AND i.kind = 'execution_reconciliation'
		ORDER BY i.id ASC
		LIMIT 2`, state.Identity.SessionID, state.Identity.WorkspaceID, state.Identity.ExecutionID)
	if err != nil {
		return false, fmt.Errorf("query execution reconciliation overlay: %w", err)
	}
	controlStates := make([]controlplane.State, 0, 2)
	for rows.Next() {
		controlState, scanErr := scanControlState(rows)
		if scanErr != nil {
			_ = rows.Close()
			return false, fmt.Errorf("%w: scan control state: %v", ErrExecutionReconciliationCorrupt, scanErr)
		}
		controlStates = append(controlStates, controlState)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close execution reconciliation overlay: %w", err)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read execution reconciliation overlay: %w", err)
	}
	if len(controlStates) == 0 {
		return false, nil
	}
	if len(controlStates) != 1 {
		return false, fmt.Errorf("%w: execution %q has %d control items", ErrExecutionReconciliationCorrupt, state.Identity.ExecutionID, len(controlStates))
	}
	controlState := controlStates[0]
	item := controlState.Item
	if err := item.Validate(); err != nil {
		return false, fmt.Errorf("%w: invalid item %q: %v", ErrExecutionReconciliationCorrupt, item.ItemID, err)
	}
	if item.Identity.SessionID != state.Identity.SessionID ||
		item.Identity.WorkspaceID != state.Identity.WorkspaceID ||
		item.Identity.ExecutionID != state.Identity.ExecutionID ||
		item.Identity.TurnID != state.Identity.TurnID {
		return false, fmt.Errorf("%w: item %q does not match the immutable execution scope", ErrExecutionReconciliationCorrupt, item.ItemID)
	}
	if controlState.Resolution == nil {
		return false, nil
	}
	resolution := *controlState.Resolution
	if err := resolution.Validate(); err != nil {
		return false, fmt.Errorf("%w: invalid resolution %q: %v", ErrExecutionReconciliationCorrupt, resolution.ResolutionID, err)
	}
	if resolution.ItemID != item.ItemID || resolution.SessionID != item.Identity.SessionID ||
		resolution.WorkspaceID != item.Identity.WorkspaceID || resolution.Outcome != controlplane.OutcomeReconciled {
		return false, fmt.Errorf("%w: resolution %q does not exactly resolve item %q", ErrExecutionReconciliationCorrupt, resolution.ResolutionID, item.ItemID)
	}
	target, err := executionReconciliationTarget(item, state.Latest, resolution.ResolvedBy)
	if err != nil {
		return false, fmt.Errorf("%w: derive target: %v", ErrExecutionReconciliationCorrupt, err)
	}
	envelope, err := reconciliation.Parse(resolution.EvidenceJSON, resolution.EvidenceSHA256)
	if err != nil {
		return false, fmt.Errorf("%w: parse resolution %q: %v", ErrExecutionReconciliationCorrupt, resolution.ResolutionID, err)
	}
	if !envelope.MatchesTarget(target) {
		return false, fmt.Errorf("%w: resolution %q target binding differs from durable state", ErrExecutionReconciliationCorrupt, resolution.ResolutionID)
	}
	return true, nil
}

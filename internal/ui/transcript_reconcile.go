package ui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"math"
)

// transcriptRenderProbe is opt-in instrumentation for renderer hot-path
// tests. Production models leave it nil, so normal rendering pays only a
// predictable nil branch at semantic digest and layout publication seams.
type transcriptRenderProbe struct {
	semanticDigestCalls       int
	layoutRecordComparisons   int
	layoutRecordsMaterialized int
}

// reconcileTranscriptEntriesForRender keeps semantic admission out of visual
// ticks. Entry mutation paths invalidate the render cache; append-only paths
// are also detected by the reconciled length. A direct semantic projection
// such as transcriptBlocks still calls reconcileTranscriptEntries itself and
// therefore never relies on this paint cache.
func (m *Model) reconcileTranscriptEntriesForRender() (bool, error) {
	if m.transcriptReconcileValid && m.transcriptReconciledCount == len(m.entries) {
		return false, nil
	}
	if err := m.reconcileTranscriptEntries(); err != nil {
		m.transcriptReconcileValid = false
		m.transcriptReconciledCount = 0
		return true, err
	}
	m.transcriptReconcileValid = true
	m.transcriptReconciledCount = len(m.entries)
	if m.transcriptReconcileEpoch < math.MaxUint64 {
		m.transcriptReconcileEpoch++
	}
	return true, nil
}

// reconcileTranscriptEntries is the migration seam between the existing
// ChatEntry append sites and the semantic transcript model. It assigns missing
// identities once, preserves restored identities, advances only monotonic
// lifecycles, and updates semantic revisions without treating theme or width
// changes as content changes.
func (m *Model) reconcileTranscriptEntries() error {
	seen := make(map[BlockID]struct{}, len(m.entries))
	var currentTurn TurnID

	for index := range m.entries {
		entry := &m.entries[index]
		if entry.BlockID == "" {
			id, err := NewBlockID()
			if err != nil {
				return fmt.Errorf("entry %d: %w", index, err)
			}
			entry.BlockID = id
		}
		if !entry.BlockID.Valid() {
			return fmt.Errorf("entry %d has invalid block ID %q", index, entry.BlockID)
		}
		if _, duplicate := seen[entry.BlockID]; duplicate {
			return fmt.Errorf("entry %d repeats block ID %q", index, entry.BlockID)
		}
		seen[entry.BlockID] = struct{}{}

		kind := blockKindForChatEntry(*entry)
		needsTurn := !kind.turnOptional()
		switch {
		case entry.Kind == "user":
			if entry.TurnID == "" {
				turnID, err := NewTurnID()
				if err != nil {
					return fmt.Errorf("entry %d: %w", index, err)
				}
				entry.TurnID = turnID
			}
			currentTurn = entry.TurnID
		case needsTurn && entry.TurnID == "":
			if currentTurn == "" {
				turnID, err := NewTurnID()
				if err != nil {
					return fmt.Errorf("entry %d: %w", index, err)
				}
				currentTurn = turnID
			}
			entry.TurnID = currentTurn
		case needsTurn:
			currentTurn = entry.TurnID
		}
		if entry.TurnID != "" && !entry.TurnID.Valid() {
			return fmt.Errorf("entry %d has invalid turn ID %q", index, entry.TurnID)
		}

		nextLifecycle := m.chatEntryLifecycle(*entry)
		digest := m.chatEntrySemanticDigest(*entry)
		if entry.Revision == 0 {
			entry.Revision = 1
		} else {
			lifecycleChanged := entry.Lifecycle != nextLifecycle
			if lifecycleChanged && !entry.Lifecycle.CanTransitionTo(nextLifecycle) {
				return fmt.Errorf(
					"entry %d block %q lifecycle cannot move from %d to %d",
					index,
					entry.BlockID,
					entry.Lifecycle,
					nextLifecycle,
				)
			}
			semanticChanged := entry.semanticDigest != ([32]byte{}) && entry.semanticDigest != digest
			if lifecycleChanged || semanticChanged {
				if entry.Revision == math.MaxUint64 {
					return fmt.Errorf("entry %d block %q exhausted its semantic revision", index, entry.BlockID)
				}
				entry.Revision++
			}
		}
		entry.Lifecycle = nextLifecycle
		entry.semanticDigest = digest
	}
	return nil
}

func blockKindForChatEntry(entry ChatEntry) BlockKind {
	switch entry.Kind {
	case "user":
		return BlockKindUserMessage
	case "assistant":
		return BlockKindAssistantMessage
	case "tool_group":
		return BlockKindToolGroup
	case "error":
		return BlockKindErrorNotice
	case "system":
		return BlockKindSystemNotice
	default:
		return BlockKindSystemNotice
	}
}

func (m *Model) chatEntryLifecycle(entry ChatEntry) BlockLifecycle {
	switch entry.Kind {
	case "error":
		return BlockFailed
	case "tool_group":
		if entry.ToolIndex >= 0 && entry.ToolIndex < len(m.toolEntries) {
			switch m.toolEntries[entry.ToolIndex].Status {
			case ToolStatusRunning:
				return BlockLive
			case ToolStatusError:
				return BlockFailed
			default:
				return BlockSettled
			}
		}
		return BlockFailed
	default:
		return BlockSettled
	}
}

func (m *Model) chatEntrySemanticDigest(entry ChatEntry) [32]byte {
	if m.transcriptRenderProbe != nil {
		m.transcriptRenderProbe.semanticDigestCalls++
	}
	digest := sha256.New()
	writeTranscriptDigestPart(digest, entry.Kind)
	writeTranscriptDigestPart(digest, entry.Content)
	writeTranscriptDigestPart(digest, entry.Name)
	writeTranscriptDigestPart(digest, fmt.Sprintf("%t", entry.IsError))
	for _, attachment := range entry.Attachments {
		writeTranscriptDigestPart(digest, attachment.Digest)
		writeTranscriptDigestPart(digest, attachment.MIMEType)
		writeTranscriptDigestPart(digest, attachment.Name)
		writeTranscriptDigestPart(digest, fmt.Sprintf("%d:%d:%d", attachment.SizeBytes, attachment.Width, attachment.Height))
	}
	if entry.Kind == "tool_group" && entry.ToolIndex >= 0 && entry.ToolIndex < len(m.toolEntries) {
		tool := m.toolEntries[entry.ToolIndex]
		writeTranscriptDigestPart(digest, tool.ID)
		writeTranscriptDigestPart(digest, tool.Name)
		writeTranscriptDigestPart(digest, tool.Summary)
		writeTranscriptDigestPart(digest, tool.Args)
		writeTranscriptDigestPart(digest, tool.Result)
		writeTranscriptDigestPart(digest, fmt.Sprintf("%d:%t:%d", tool.Status, tool.IsError, tool.Duration))
		if projection, err := json.Marshal(tool.Projection); err == nil {
			writeTranscriptDigestPart(digest, string(projection))
		}
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func writeTranscriptDigestPart(digest hash.Hash, value string) {
	_, _ = digest.Write([]byte(value))
	_, _ = digest.Write([]byte{0})
}

// transcriptBlocks returns the provider-neutral semantic projection admitted
// to persistence, search, and future virtualized layout. Provider reasoning,
// raw MCP StructuredContent, credentials, and transient ANSI never enter it.
func (m *Model) transcriptBlocks() ([]TranscriptBlock, error) {
	if err := m.reconcileTranscriptEntries(); err != nil {
		return nil, err
	}
	blocks := make([]TranscriptBlock, 0, len(m.entries))
	for _, entry := range m.entries {
		var (
			payload BlockPayload
			err     error
		)
		if entry.Kind == "tool_group" && entry.ToolIndex >= 0 && entry.ToolIndex < len(m.toolEntries) {
			tool := m.toolEntries[entry.ToolIndex]
			payload, err = NewHostProjectedBlockPayload(
				sanitizeTerminalSingleLine(tool.Name),
				boundedToolCardSummary(tool.Summary),
			)
		} else {
			payload, err = NewVisibleTextBlockPayload(sanitizeTerminalMultiline(entry.Content))
		}
		if err != nil {
			return nil, fmt.Errorf("block %q payload: %w", entry.BlockID, err)
		}
		blocks = append(blocks, TranscriptBlock{
			ID:        entry.BlockID,
			TurnID:    entry.TurnID,
			Kind:      blockKindForChatEntry(entry),
			Revision:  entry.Revision,
			Lifecycle: entry.Lifecycle,
			Payload:   payload,
		})
	}
	if err := ValidateTranscriptBlocks(blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

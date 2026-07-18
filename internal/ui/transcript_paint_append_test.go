package ui

import (
	"fmt"
	"strings"
	"testing"
)

func TestTranscriptPaintAppendOnlyGrowthMeasuresOnlyDelta(t *testing.T) {
	m := newTestModel(t)
	const historyEntries = 128
	m.entries = make([]ChatEntry, 0, historyEntries+3)
	for index := 0; index < historyEntries; index++ {
		m.entries = append(m.entries, ChatEntry{
			Kind:    "user",
			Content: fmt.Sprintf("stable prefix %03d\ncontinued", index),
		})
	}
	m.invalidateEntryCache()
	m.pauseFollow()
	m.transcriptPaint.top = historyEntries / 2
	m.refreshTranscript()

	cache := &m.transcriptPaint.cache
	if !cache.valid || cache.entryCount != historyEntries {
		t.Fatalf(
			"cold paint cache = {valid:%t entries:%d}, want valid prefix of %d",
			cache.valid,
			cache.entryCount,
			historyEntries,
		)
	}
	if len(cache.stableBlocks) != historyEntries {
		t.Fatalf("cold stable blocks = %d, want %d", len(cache.stableBlocks), historyEntries)
	}
	prefixTurnID := m.entries[historyEntries-1].TurnID
	prefixBlockID := m.entries[historyEntries-1].BlockID
	lineIndexes := make([]*int, len(cache.stableBlocks))
	for index := range cache.stableBlocks {
		if len(cache.stableBlocks[index].lineStarts) == 0 {
			t.Fatalf("cold block %d has no row index", index)
		}
		lineIndexes[index] = &cache.stableBlocks[index].lineStarts[0]
	}

	appended := []ChatEntry{
		{Kind: "assistant", Content: "new answer with **Markdown**"},
		{Kind: "system", Content: "new bounded notice"},
		{Kind: "user", Content: "next prompt\nwith another row"},
	}
	m.entries = append(m.entries, appended...)
	probe := &transcriptRenderProbe{}
	m.transcriptRenderProbe = probe
	m.refreshTranscript()

	if got, want := probe.blocksMeasured, len(appended); got != want {
		t.Fatalf("append paint measured %d blocks, want only delta %d", got, want)
	}
	if got, want := probe.semanticDigestCalls, len(appended); got != want {
		t.Fatalf("append reconciliation hashed %d entries, want only delta %d", got, want)
	}
	if got := m.entries[historyEntries].TurnID; got != prefixTurnID {
		t.Fatalf("appended assistant turn = %q, want cached causal turn %q", got, prefixTurnID)
	}
	if got := m.entries[historyEntries-1].BlockID; got != prefixBlockID {
		t.Fatalf("append changed stable prefix block ID from %q to %q", prefixBlockID, got)
	}
	if cache.entryCount != len(m.entries) || cache.stableCount != len(m.entries) {
		t.Fatalf(
			"promoted cache counts = entries:%d stable:%d, want %d",
			cache.entryCount,
			cache.stableCount,
			len(m.entries),
		)
	}
	if got, want := len(cache.stableBlocks), historyEntries+len(appended); got != want {
		t.Fatalf("promoted stable blocks = %d, want %d", got, want)
	}
	for index, lineIndex := range lineIndexes {
		if got := &cache.stableBlocks[index].lineStarts[0]; got != lineIndex {
			t.Fatalf("append rebuilt row index for stable block %d", index)
		}
	}

	assertTranscriptPaintReferenceParity(t, m)
}

func TestTranscriptPaintMutationInvalidatesAppendReuse(t *testing.T) {
	m := newTestModel(t)
	m.toolEntries = []ToolEntry{{
		ID:        "append-cache-tool",
		Name:      "bash",
		Summary:   "task verify",
		Args:      "cmd=task verify",
		Result:    "first row\nsecond row",
		Status:    ToolStatusDone,
		Collapsed: true,
	}}
	m.entries = []ChatEntry{
		{Kind: "user", Content: "run verification"},
		{Kind: "tool_group", ToolIndex: 0},
		{Kind: "assistant", Content: "verification complete"},
	}
	m.invalidateEntryCache()
	m.refreshTranscript()

	m.toolEntries[0].Collapsed = false
	m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "receipt expanded"})
	m.invalidateEntryCache()
	if m.transcriptPaint.cache.valid {
		t.Fatal("tool geometry mutation left the paint cache valid")
	}

	probe := &transcriptRenderProbe{}
	m.transcriptRenderProbe = probe
	m.refreshTranscript()
	if got, want := probe.blocksMeasured, len(m.entries); got != want {
		t.Fatalf("mutated append measured %d blocks, want full rebuild of %d", got, want)
	}
	if got, want := probe.semanticDigestCalls, len(m.entries); got != want {
		t.Fatalf("mutated append hashed %d entries, want full reconciliation of %d", got, want)
	}
	assertTranscriptPaintReferenceParity(t, m)
}

func assertTranscriptPaintReferenceParity(t *testing.T, m *Model) {
	t.Helper()

	m.transcriptRenderProbe = nil
	referenceRows := strings.Split(m.renderEntries(), "\n")
	if got, want := m.transcriptPaint.document.totalRows, len(referenceRows); got != want {
		t.Fatalf("paint rows = %d, complete reference rows = %d", got, want)
	}

	tops := []int{0, m.transcriptMaxTop() / 2, m.transcriptMaxTop()}
	for _, top := range tops {
		m.pauseFollow()
		m.setTranscriptYOffset(top)
		start := m.transcriptPaint.windowStart
		end := m.transcriptPaint.windowEnd
		want := strings.Join(referenceRows[start:end], "\n")
		if got := m.viewport.GetContent(); got != want {
			t.Fatalf(
				"paint window at top %d rows [%d,%d) diverged from complete reference:\n--- got ---\n%s\n--- want ---\n%s",
				top,
				start,
				end,
				got,
				want,
			)
		}
	}
}

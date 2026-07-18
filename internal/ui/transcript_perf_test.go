package ui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestWarmLiveToolRenderDoesNotRehashOrRepublishStableHistory(t *testing.T) {
	m := newTestModel(t)
	m.ready = true
	m.now = func() time.Time {
		return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	}

	const historyEntries = 320
	for index := 0; index < historyEntries; index++ {
		kind := "assistant"
		if index%2 == 0 {
			kind = "user"
		}
		m.entries = append(m.entries, ChatEntry{
			Kind: kind,
			Content: fmt.Sprintf(
				"history-%03d %s",
				index,
				strings.Repeat("semantic transcript payload ", 12),
			),
		})
	}
	m.toolEntries = []ToolEntry{{
		ID: "live-tool", Name: "bash", Status: ToolStatusRunning,
	}}
	m.entries = append(m.entries, ChatEntry{Kind: "tool_group", ToolIndex: 0})
	m.toolsPending = 1

	cold := m.renderEntries()
	if m.cachedStableCount != historyEntries {
		t.Fatalf("stable prefix = %d, want %d", m.cachedStableCount, historyEntries)
	}
	if len(m.transcriptLayout.Records) != len(m.entries) {
		t.Fatalf("layout records = %d, want %d", len(m.transcriptLayout.Records), len(m.entries))
	}
	if len(m.transcriptLayout.Records[0].LineMap) == 0 {
		t.Fatal("precondition: first stable block has no semantic line map")
	}
	recordsAddress := &m.transcriptLayout.Records[0]
	lineMapAddress := &m.transcriptLayout.Records[0].LineMap[0]

	probe := &transcriptRenderProbe{}
	m.transcriptRenderProbe = probe
	warm := m.renderEntries()

	if warm != cold {
		t.Fatalf("warm live-tool paint diverged from cold paint")
	}
	if probe.semanticDigestCalls != 0 {
		t.Fatalf("warm paint rehashed %d semantic entries, want 0", probe.semanticDigestCalls)
	}
	if probe.layoutRecordsMaterialized != 0 {
		t.Fatalf("warm paint materialized %d layout records, want 0", probe.layoutRecordsMaterialized)
	}
	// The immutable 320-block prefix is shared. Only the thawed final prefix
	// record and the running ToolCard record need equality checks.
	if probe.layoutRecordComparisons > 2 {
		t.Fatalf("warm paint compared %d layout records, want at most 2", probe.layoutRecordComparisons)
	}
	if recordsAddress != &m.transcriptLayout.Records[0] {
		t.Fatal("warm paint replaced the immutable transcript snapshot")
	}
	if lineMapAddress != &m.transcriptLayout.Records[0].LineMap[0] {
		t.Fatal("warm paint cloned the stable prefix LineMap")
	}
}

func TestPausedStreamingTailKeepsTransientBlockAcrossResizeAndTheme(t *testing.T) {
	m := newTestModel(t)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 82, Height: 14}, nil)
	m.entries = []ChatEntry{{Kind: "user", Content: "stream a detailed response"}}
	m.state = StateStreaming
	for index := 0; index < 70; index++ {
		fmt.Fprintf(
			&m.streamBuf,
			"stream row %02d keeps a semantic coordinate while the terminal reflows this long line\n",
			index,
		)
	}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())

	if got, want := len(m.transcriptLayout.Records), len(m.entries)+1; got != want {
		t.Fatalf("live layout records = %d, want %d (one transient live-tail block)", got, want)
	}
	live := m.transcriptLayout.Records[len(m.transcriptLayout.Records)-1]
	if !strings.HasPrefix(string(live.BlockID), "transient_live_") || live.TurnID == "" {
		t.Fatalf("live-tail identity = block %q turn %q", live.BlockID, live.TurnID)
	}
	if len(live.LineMap) < 12 {
		t.Fatalf("live-tail LineMap has %d rows, want enough to pause inside it", len(live.LineMap))
	}

	m.viewport.SetYOffset(live.StartRow + 8)
	m.pauseFollow()
	resizeCapture := m.captureTranscriptReflowAnchor()
	if resizeCapture.Intent.Manual.BlockID != live.BlockID {
		t.Fatalf("resize anchor block = %q, want live tail %q", resizeCapture.Intent.Manual.BlockID, live.BlockID)
	}

	m.handleWindowSize(tea.WindowSizeMsg{Width: 47, Height: 14}, nil)
	assertCapturedTranscriptPosition(t, m, resizeCapture, live.BlockID, "width resize")

	current := transcriptLayoutRecordByID(t, m.transcriptLayout, live.BlockID)
	m.viewport.SetYOffset(current.StartRow + 6)
	m.pauseFollow()
	themeCapture := m.captureTranscriptReflowAnchor()
	if themeCapture.Intent.Manual.BlockID != live.BlockID {
		t.Fatalf("theme anchor block = %q, want live tail %q", themeCapture.Intent.Manual.BlockID, live.BlockID)
	}

	var background color.Color = color.White
	if !m.isDark {
		background = color.Black
	}
	m.handleThemeChange(tea.BackgroundColorMsg{Color: background})
	assertCapturedTranscriptPosition(t, m, themeCapture, live.BlockID, "theme change")
}

func TestLiveTailIdentityAvoidsPersistedCollisionAndEmptyTurnReuse(t *testing.T) {
	t.Run("persisted collision", func(t *testing.T) {
		m := newTestModel(t)
		m.ready = true
		const turnID = TurnID("turn_adversarial")
		collision := liveTailLayoutCandidate(0, turnID, 1, 0)
		m.entries = []ChatEntry{{
			BlockID:   collision,
			TurnID:    turnID,
			Revision:  1,
			Lifecycle: BlockSettled,
			Kind:      "user",
			Content:   "persisted opaque identity",
		}}
		m.state = StateStreaming
		m.streamBuf.WriteString("live response")

		m.viewport.SetContent(m.renderEntries())
		live := m.transcriptLayout.Records[len(m.transcriptLayout.Records)-1]
		if live.BlockID == collision {
			t.Fatalf("transient live block reused persisted ID %q", collision)
		}
		if want := liveTailLayoutCandidate(0, turnID, 1, 1); live.BlockID != want {
			t.Fatalf("collision probe selected %q, want deterministic next candidate %q", live.BlockID, want)
		}
		if _, _, err := indexCurrentTranscriptLayout(m.transcriptLayout.Records); err != nil {
			t.Fatalf("collision-probed layout is invalid: %v", err)
		}
	})

	t.Run("empty turn starts a new episode", func(t *testing.T) {
		m := newTestModel(t)
		m.ready = true
		m.state = StateStreaming
		m.streamBuf.WriteString("first unowned live tail")
		_ = m.renderEntries()
		first := m.transcriptLayout.Records[len(m.transcriptLayout.Records)-1].BlockID

		m.streamBuf.Reset()
		m.state = StateIdle
		_ = m.renderEntries()
		if len(m.transcriptLayout.Records) != 0 {
			t.Fatalf("settled empty transcript retained layout: %#v", m.transcriptLayout.Records)
		}

		m.state = StateStreaming
		m.streamBuf.WriteString("second unowned live tail")
		_ = m.renderEntries()
		second := m.transcriptLayout.Records[len(m.transcriptLayout.Records)-1].BlockID
		if first == second {
			t.Fatalf("empty-turn live identity %q was reused across episodes", first)
		}
	})
}

func assertCapturedTranscriptPosition(
	t *testing.T,
	m *Model,
	capture transcriptReflowAnchor,
	blockID BlockID,
	operation string,
) {
	t.Helper()
	resolution, err := ResolveTranscriptAnchor(
		capture.Intent,
		capture.Previous,
		m.transcriptLayout,
		max(1, m.viewport.Height()),
	)
	if err != nil {
		t.Fatalf("%s anchor resolution: %v", operation, err)
	}
	if resolution.BlockID != blockID {
		t.Fatalf("%s resolved block = %q, want %q", operation, resolution.BlockID, blockID)
	}
	if got := m.viewport.YOffset(); got != resolution.ViewportTop {
		t.Fatalf("%s viewport top = %d, semantic resolution = %d", operation, got, resolution.ViewportTop)
	}
	current := transcriptLayoutRecordByID(t, m.transcriptLayout, blockID)
	if top := m.viewport.YOffset(); top < current.StartRow || top >= current.StartRow+current.Height {
		t.Fatalf(
			"%s viewport top %d escaped live block rows [%d,%d)",
			operation,
			top,
			current.StartRow,
			current.StartRow+current.Height,
		)
	}
	if m.followPaused() == false {
		t.Fatalf("%s resumed follow while reader was paused in the stream", operation)
	}
}

func transcriptLayoutRecordByID(
	t *testing.T,
	snapshot TranscriptLayoutSnapshot,
	blockID BlockID,
) TranscriptLayoutRecord {
	t.Helper()
	for _, record := range snapshot.Records {
		if record.BlockID == blockID {
			return record
		}
	}
	t.Fatalf("layout block %q not found", blockID)
	return TranscriptLayoutRecord{}
}

package ui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
)

// transcriptPaintOverscanRows keeps a small runway around the visible rows so
// wheel and line-at-a-time movement do not rebuild the Bubbles paint surface on
// every event. The bound is intentionally independent of transcript length.
const transcriptPaintOverscanRows = 4

// transcriptPaintBlock is one already-measured transcript fragment. Content
// remains owned by the per-entry renderer/memo; lineStarts lets paint extract a
// row inside a very large block without splitting or joining that whole block.
type transcriptPaintBlock struct {
	startRow   int
	content    string
	lineStarts []int
}

func newTranscriptPaintBlock(startRow int, content string) transcriptPaintBlock {
	starts := make([]int, 1, strings.Count(content, "\n")+1)
	starts[0] = 0
	for index := 0; index < len(content); index++ {
		if content[index] == '\n' {
			starts = append(starts, index+1)
		}
	}
	return transcriptPaintBlock{
		startRow:   max(0, startRow),
		content:    content,
		lineStarts: starts,
	}
}

func (block transcriptPaintBlock) rowCount() int {
	if block.content == "" {
		return 0
	}
	return len(block.lineStarts)
}

func (block transcriptPaintBlock) endRow() int {
	return block.startRow + block.rowCount()
}

func (block transcriptPaintBlock) row(index int) string {
	if index < 0 || index >= block.rowCount() {
		return ""
	}
	start := block.lineStarts[index]
	end := len(block.content)
	if index+1 < len(block.lineStarts) {
		end = block.lineStarts[index+1] - 1
		if end > start && block.content[end-1] == '\r' {
			end--
		}
	}
	return block.content[start:end]
}

// transcriptPaintDocument is the renderer-owned logical document. Stable
// blocks are shared with the cache; tail blocks contain only entries after the
// cached prefix plus the current streaming/activity block.
//
// Blank separator rows do not need fragments. Their geometry is already
// represented by block start rows and TranscriptLayoutRecord heights, and the
// bounded row materializer leaves those cells empty.
type transcriptPaintDocument struct {
	base      []transcriptPaintBlock
	tail      []transcriptPaintBlock
	totalRows int
}

func (document transcriptPaintDocument) materializeRows(start, end int) ([]string, int) {
	start = min(max(0, start), document.totalRows)
	end = min(max(start, end), document.totalRows)
	rows := make([]string, end-start)
	painted := materializeTranscriptBlocks(rows, start, end, document.base)
	painted += materializeTranscriptBlocks(rows, start, end, document.tail)
	return rows, painted
}

func materializeTranscriptBlocks(
	destination []string,
	start, end int,
	blocks []transcriptPaintBlock,
) int {
	if len(destination) == 0 || len(blocks) == 0 {
		return 0
	}
	first := sort.Search(len(blocks), func(index int) bool {
		return blocks[index].endRow() > start
	})
	painted := 0
	for index := first; index < len(blocks); index++ {
		block := blocks[index]
		if block.startRow >= end {
			break
		}
		from := max(start, block.startRow)
		to := min(end, block.endRow())
		if from >= to {
			continue
		}
		painted++
		for row := from; row < to; row++ {
			destination[row-start] = block.row(row - block.startRow)
		}
	}
	return painted
}

type transcriptPaintCache struct {
	valid              bool
	entryCount         int
	stableCount        int
	prefixLayoutCount  int
	prefixState        entryRenderState
	stableBlocks       []transcriptPaintBlock
	toolHitRegions     []toolHitRegion
	thinkingHitRegions []thinkingHitRegion
}

// transcriptPaintState keeps global document coordinates out of Bubbles.
// Bubbles owns only the bounded rows in [windowStart, windowEnd); top remains
// the canonical document offset used by follow, anchors, jumps, and pointer
// translation.
type transcriptPaintState struct {
	active              bool
	top                 int
	document            transcriptPaintDocument
	documentGeneration  uint64
	windowGeneration    uint64
	windowStart         int
	windowEnd           int
	windowHeight        int
	cache               transcriptPaintCache
	liveCache           transcriptLivePaintCache
	streamSourceEpoch   uint64
	streamSourceRev     uint64
	streamSourceLen     int
	streamSourceTracked bool
}

type transcriptPaintBuild struct {
	document   transcriptPaintDocument
	dirtyStart int
}

// transcriptLivePaintCache owns the append-only, plain-text projection of the
// provider's live tail. Each visible row is an independent measured fragment,
// so appending a token only replaces the current raw line and adds newly
// wrapped rows. Stable rows, their line index, and semantic coordinates remain
// shared with the previous paint.
//
// Structured Markdown, reasoning, terminal-unsafe input, geometry changes, and
// non-append mutations deliberately miss this cache and use renderLiveTail.
// That keeps this optimization a narrow implementation detail rather than a
// second, incomplete Markdown renderer.
type transcriptLivePaintCache struct {
	valid               bool
	streamSourceEpoch   uint64
	streamSourceRev     uint64
	rawLen              int
	startRow            int
	contentWidth        int
	wrapWidth           int
	usesWorkWidth       bool
	showHeader          bool
	isDark              bool
	markdownRenderer    *MarkdownRenderer
	blockID             BlockID
	turnID              TurnID
	rows                []string
	blocks              []transcriptPaintBlock
	lineMap             LineMap
	lastRawStart        int
	lastRawLogical      int
	lastRenderedStart   int
	lastBlockStart      int
	lastLineMapStart    int
	previousLineHasPipe bool
}

// refreshTranscript is the only production boundary that remeasures semantic
// transcript state and stages a bounded paint window. renderEntries remains a
// complete reference materializer for exports and tests, but production event
// handlers must not call it.
func (m *Model) refreshTranscript() {
	paint := &m.transcriptPaint
	reflowAnchor := m.captureTranscriptReflowAnchor()
	oldActive := paint.active
	oldTop := paint.top
	oldDocumentGeneration := paint.documentGeneration
	oldWindowGeneration := paint.windowGeneration
	oldWindowStart := paint.windowStart
	oldWindowEnd := paint.windowEnd
	oldWindowHeight := paint.windowHeight

	build := m.buildTranscriptPaintDocument()
	paint.active = true
	paint.document = build.document
	if paint.documentGeneration < ^uint64(0) {
		paint.documentGeneration++
	}
	paint.windowGeneration = 0

	if !m.ready || m.anchorActive {
		paint.top = m.transcriptMaxTop()
	} else {
		paint.top = min(max(0, paint.top), m.transcriptMaxTop())
	}

	height := max(1, m.viewport.Height())
	visibleEnd := min(paint.document.totalRows, paint.top+height)
	canReuseStableWindow := oldActive &&
		m.followPaused() &&
		oldTop == paint.top &&
		oldWindowGeneration == oldDocumentGeneration &&
		oldWindowHeight == height &&
		oldWindowStart <= paint.top &&
		visibleEnd <= oldWindowEnd &&
		oldWindowEnd <= build.dirtyStart &&
		oldWindowEnd <= paint.document.totalRows
	if canReuseStableWindow {
		paint.windowGeneration = paint.documentGeneration
		paint.windowStart = oldWindowStart
		paint.windowEnd = oldWindowEnd
		paint.windowHeight = oldWindowHeight
		m.syncTranscriptPaintWindow()
		return
	}

	if reflowAnchor.Valid && reflowAnchor.Intent.Mode == TranscriptAnchorManual {
		m.restoreTranscriptReflowAnchor(reflowAnchor)
		return
	}
	m.syncTranscriptPaintWindow()
}

func (m *Model) buildTranscriptPaintDocument() transcriptPaintBuild {
	if m.transcriptRenderProbe != nil {
		m.transcriptRenderProbe.documentBuilds++
	}
	contentW := m.chatContentWidth()
	proseW := m.chatProseWidth()
	if !m.transcriptHasConversation() && !m.hasVisibleLiveTurn() {
		return transcriptPaintBuild{
			document:   m.buildEmptyTranscriptPaintDocument(contentW, proseW),
			dirtyStart: 0,
		}
	}

	identityReady := true
	reconciled, err := m.reconcileTranscriptEntriesForRender()
	if err != nil {
		identityReady = false
		m.resetEntryMemo()
		if m.logger != nil {
			m.logger.Error("reconcile transcript identity for paint", "error", err)
		}
	} else if reconciled {
		liveIDs := make(map[BlockID]struct{}, len(m.entries))
		for _, entry := range m.entries {
			liveIDs[entry.BlockID] = struct{}{}
		}
		for id := range m.entryMemo {
			if _, live := liveIDs[id]; !live {
				delete(m.entryMemo, id)
			}
		}
	}

	cache := &m.transcriptPaint.cache
	if build, ok := m.buildTranscriptFromPaintCache(cache, contentW, identityReady); ok {
		return build
	}

	stableCount := m.stableEntryPrefixLen()
	blocks := make([]transcriptPaintBlock, 0, len(m.entries))
	m.toolHitRegions = m.toolHitRegions[:0]
	m.thinkingHitRegions = m.thinkingHitRegions[:0]
	var state entryRenderState

	snapshotPrefix := func() {
		state.freezeLayoutPrefix()
		cache.valid = true
		cache.entryCount = len(m.entries)
		cache.stableCount = stableCount
		cache.prefixLayoutCount = state.layoutLen()
		cache.prefixState = state
		cache.stableBlocks = append(cache.stableBlocks[:0], blocks...)
		cache.stableBlocks = cache.stableBlocks[:len(cache.stableBlocks):len(cache.stableBlocks)]
		cache.toolHitRegions = append(cache.toolHitRegions[:0], m.toolHitRegions...)
		cache.thinkingHitRegions = append(cache.thinkingHitRegions[:0], m.thinkingHitRegions...)
	}

	for index := range m.entries {
		if index == stableCount {
			snapshotPrefix()
		}
		if block, ok := m.renderTranscriptPaintEntry(index, contentW, identityReady, &state); ok {
			blocks = append(blocks, block)
		}
	}
	if stableCount == len(m.entries) {
		snapshotPrefix()
	}

	tail := m.renderTranscriptPaintLiveTail(contentW, &state)
	totalRows := transcriptRenderStateHeight(&state)
	m.finalizeTranscriptLayoutHeight(&state, totalRows)
	m.bindCachedTranscriptPaintLayoutPrefix()
	return transcriptPaintBuild{
		document: transcriptPaintDocument{
			base:      cache.stableBlocks,
			tail:      tail,
			totalRows: totalRows,
		},
		dirtyStart: 0,
	}
}

// buildTranscriptFromPaintCache admits both an unchanged transcript and strict
// append-only growth. Existing blocks may be reused only while every formerly
// measured entry remains inside the stable prefix. All mutation paths must call
// invalidateEntryCache, which clears cache.valid before this boundary.
//
// Appended entries are promoted into the immutable prefix before the live tail
// is rendered. A later stream repaint therefore measures only that live block,
// while another append starts from the newly promoted prefix.
func (m *Model) buildTranscriptFromPaintCache(
	cache *transcriptPaintCache,
	contentW int,
	identityReady bool,
) (transcriptPaintBuild, bool) {
	if cache == nil ||
		!cache.valid ||
		cache.entryCount > len(m.entries) ||
		cache.stableCount != cache.entryCount ||
		m.stableEntryPrefixLen() != len(m.entries) {
		return transcriptPaintBuild{}, false
	}

	dirtyStart := transcriptRenderStateHeight(&cache.prefixState)
	oldEntryCount := cache.entryCount
	m.toolHitRegions = append(m.toolHitRegions[:0], cache.toolHitRegions...)
	m.thinkingHitRegions = append(m.thinkingHitRegions[:0], cache.thinkingHitRegions...)
	state := cache.prefixState
	appendedBlocks := make(
		[]transcriptPaintBlock,
		0,
		len(m.entries)-oldEntryCount,
	)
	for index := oldEntryCount; index < len(m.entries); index++ {
		if block, ok := m.renderTranscriptPaintEntry(index, contentW, identityReady, &state); ok {
			appendedBlocks = append(appendedBlocks, block)
		}
	}

	if oldEntryCount != len(m.entries) {
		state.freezeLayoutPrefix()
		promotedBlocks := make(
			[]transcriptPaintBlock,
			0,
			len(cache.stableBlocks)+len(appendedBlocks),
		)
		promotedBlocks = append(promotedBlocks, cache.stableBlocks...)
		promotedBlocks = append(promotedBlocks, appendedBlocks...)

		cache.entryCount = len(m.entries)
		cache.stableCount = len(m.entries)
		cache.prefixLayoutCount = state.layoutLen()
		cache.prefixState = state
		cache.stableBlocks = promotedBlocks[:len(promotedBlocks):len(promotedBlocks)]
		cache.toolHitRegions = append(cache.toolHitRegions[:0], m.toolHitRegions...)
		cache.thinkingHitRegions = append(cache.thinkingHitRegions[:0], m.thinkingHitRegions...)
	}

	state = cache.prefixState
	tail := m.renderTranscriptPaintLiveTail(contentW, &state)
	totalRows := transcriptRenderStateHeight(&state)
	m.finalizeTranscriptLayoutHeight(&state, totalRows)
	m.bindCachedTranscriptPaintLayoutPrefix()
	return transcriptPaintBuild{
		document: transcriptPaintDocument{
			base:      cache.stableBlocks,
			tail:      tail,
			totalRows: totalRows,
		},
		dirtyStart: dirtyStart,
	}, true
}

func (m *Model) buildEmptyTranscriptPaintDocument(contentW, proseW int) transcriptPaintDocument {
	var welcomeBuilder strings.Builder
	m.renderWelcome(&welcomeBuilder)
	welcome := welcomeBuilder.String()

	hasNotice := false
	for _, entry := range m.entries {
		if entry.Kind == "system" || entry.Kind == "error" {
			hasNotice = true
			break
		}
	}

	m.endLiveTailLayoutEpisode()
	m.publishTranscriptLayout(nil)
	if !hasNotice {
		welcome = strings.TrimRight(welcome, "\n")
		top := max(0, (m.viewport.Height()-lipgloss.Height(welcome))/2)
		if welcome == "" {
			return transcriptPaintDocument{}
		}
		block := m.measureTranscriptPaintBlock(top, welcome)
		return transcriptPaintDocument{
			base:      []transcriptPaintBlock{block},
			totalRows: top + block.rowCount(),
		}
	}

	blocks := make([]transcriptPaintBlock, 0, len(m.entries)+1)
	row := 0
	appendChunk := func(chunk string) {
		if chunk == "" {
			return
		}
		blocks = append(blocks, m.measureTranscriptPaintBlock(row, chunk))
		row += strings.Count(chunk, "\n")
	}
	appendChunk(welcome + "\n")
	for _, entry := range m.entries {
		switch entry.Kind {
		case "system":
			appendChunk(m.renderSystemNotice(entry.Content, proseW) + "\n\n")
		case "error":
			if notice, ok := compactOllamaStartupNotice(entry.Content, contentW, m.ollamaOffline); ok {
				appendChunk(m.styles.ErrorText.Render(notice) + "\n")
			} else if isOllamaStartupRecovery(entry.Content, m.ollamaOffline) {
				appendChunk(m.renderSystemNotice(entry.Content, proseW) + "\n\n")
			} else {
				var rendered strings.Builder
				m.renderEntryError(&rendered, entry.Content, contentW)
				appendChunk(rendered.String())
			}
		}
	}
	return transcriptPaintDocument{
		base:      blocks,
		totalRows: row + 1,
	}
}

func (m *Model) transcriptHasConversation() bool {
	for _, entry := range m.entries {
		if entry.Kind == "user" || entry.Kind == "assistant" {
			return true
		}
	}
	return false
}

func (m *Model) renderTranscriptPaintEntry(
	entryIndex, contentW int,
	memoAllowed bool,
	state *entryRenderState,
) (transcriptPaintBlock, bool) {
	separator := ""
	if state.renderedAny {
		separator = transcriptEntrySeparator(state.previousKind, m.entries[entryIndex].Kind)
	}
	layoutCount := state.layoutLen()
	var rendered strings.Builder
	m.renderEntryInto(&rendered, entryIndex, contentW, memoAllowed, state)
	if state.layoutLen() == layoutCount {
		return transcriptPaintBlock{}, false
	}
	chunk := strings.TrimPrefix(rendered.String(), separator)
	record, ok := lastTranscriptLayoutRecord(state)
	if !ok || chunk == "" {
		return transcriptPaintBlock{}, false
	}
	return m.measureTranscriptPaintBlock(record.StartRow, chunk), true
}

func (m *Model) renderTranscriptPaintLiveTail(
	contentW int,
	state *entryRenderState,
) []transcriptPaintBlock {
	if blocks, ok := m.renderIncrementalPlainLiveTail(contentW, state); ok {
		return blocks
	}
	m.transcriptPaint.liveCache.valid = false

	separator := ""
	if state.renderedAny {
		separator = transcriptEntrySeparator(state.previousKind, "assistant")
	}
	layoutCount := state.layoutLen()
	var rendered strings.Builder
	m.renderLiveTail(&rendered, contentW, state)
	if state.layoutLen() == layoutCount {
		return nil
	}
	chunk := strings.TrimPrefix(rendered.String(), separator)
	record, ok := lastTranscriptLayoutRecord(state)
	if !ok || chunk == "" {
		return nil
	}
	return []transcriptPaintBlock{
		m.measureTranscriptPaintBlock(record.StartRow, chunk),
	}
}

func (m *Model) renderIncrementalPlainLiveTail(
	contentW int,
	state *entryRenderState,
) ([]transcriptPaintBlock, bool) {
	if state == nil ||
		strings.TrimSpace(m.thinkBuf.String()) != "" {
		return nil, false
	}
	raw := m.streamBuf.String()
	if raw == "" {
		return nil, false
	}

	separatorRows := 0
	if state.renderedAny {
		separatorRows = strings.Count(
			transcriptEntrySeparator(state.previousKind, "assistant"),
			"\n",
		)
	}
	startRow := state.renderedLines + separatorRows
	showHeader := !state.assistantStarted

	appendTrusted := m.syncTranscriptStreamSource()
	m.beginLiveTailLayoutEpisode()
	blockID, turnID := m.liveTailLayoutIdentity()
	cache := &m.transcriptPaint.liveCache
	if cache.valid &&
		!appendTrusted &&
		cache.streamSourceEpoch != m.transcriptPaint.streamSourceEpoch {
		// A direct builder mutation is not an append proof. Let the canonical
		// renderer rebuild this frame instead of deriving a segmented suffix
		// from untrusted provenance.
		return nil, false
	}
	sameProjection := cache.valid &&
		cache.streamSourceEpoch == m.transcriptPaint.streamSourceEpoch &&
		cache.startRow == startRow &&
		cache.contentWidth == contentW &&
		cache.showHeader == showHeader &&
		cache.isDark == m.isDark &&
		cache.markdownRenderer == m.md &&
		cache.blockID == blockID &&
		cache.turnID == turnID

	switch {
	case sameProjection &&
		cache.streamSourceRev == m.transcriptPaint.streamSourceRev &&
		cache.rawLen == len(raw):
		// A non-transcript event requested a frame while the live source and
		// geometry stayed fixed. Reuse every measured row and semantic point.
	case sameProjection &&
		appendTrusted &&
		cache.streamSourceRev < m.transcriptPaint.streamSourceRev &&
		cache.rawLen < len(raw) &&
		m.updateIncrementalPlainLiveTail(cache, raw):
		// The append updater changed only the final raw line and any rows added
		// after it. The stable row fragments remain shared.
	default:
		if strings.TrimSpace(raw) == "" ||
			sanitizeTerminalMultiline(raw) != raw ||
			findSafeMarkdownBoundary(raw) > 0 {
			return nil, false
		}
		messageWidth := min(contentW, m.chatProseWidth())
		usesWorkWidth := markdownUsesWorkWidth(raw)
		if usesWorkWidth {
			messageWidth = contentW
		}
		wrapWidth := max(10, messageWidth-2)
		m.buildIncrementalPlainLiveTail(
			cache,
			raw,
			startRow,
			contentW,
			wrapWidth,
			usesWorkWidth,
			showHeader,
			blockID,
			turnID,
		)
	}

	if state.renderedAny {
		state.renderedLines += separatorRows
	}
	state.appendLayoutRecord(TranscriptLayoutRecord{
		BlockID:  blockID,
		TurnID:   turnID,
		Revision: 1,
		Height:   max(1, len(cache.rows)),
		StartRow: state.renderedLines,
		Exact:    false,
		LineMap:  cache.lineMap,
	})
	state.renderedLines += len(cache.rows)
	state.previousKind = "assistant"
	state.renderedAny = true
	return cache.blocks, true
}

func (m *Model) buildIncrementalPlainLiveTail(
	cache *transcriptLivePaintCache,
	raw string,
	startRow, contentW, wrapWidth int,
	usesWorkWidth, showHeader bool,
	blockID BlockID,
	turnID TurnID,
) {
	rows, lastRenderedStart, previousLineHasPipe := m.plainLiveTailRows(
		raw,
		wrapWidth,
		showHeader,
	)
	blocks := make([]transcriptPaintBlock, 0, len(rows))
	lastBlockStart := 0
	for index, row := range rows {
		if index == lastRenderedStart {
			lastBlockStart = len(blocks)
		}
		if row != "" {
			blocks = append(
				blocks,
				m.measureTranscriptPaintBlock(startRow+index, row),
			)
		}
	}
	if lastRenderedStart == len(rows) {
		lastBlockStart = len(blocks)
	}

	mapRows := trimTrailingTranscriptPaintRows(rows)
	lineMap := semanticTranscriptLineMapFromSource(
		strings.Join(mapRows, "\n"),
		raw,
	)
	// Most token appends only add semantic rows. Keep spare immutable tail
	// capacity so extending the map does not repeatedly copy its stable prefix.
	lineMapCapacity := len(lineMap) + max(64, len(lineMap)/2)
	reservedLineMap := make(LineMap, len(lineMap), lineMapCapacity)
	copy(reservedLineMap, lineMap)
	lastLineMapStart := transcriptLineMapRowStart(
		reservedLineMap,
		lastRenderedStart,
	)
	lastRawStart := strings.LastIndex(raw, "\n") + 1

	*cache = transcriptLivePaintCache{
		valid:               true,
		streamSourceEpoch:   m.transcriptPaint.streamSourceEpoch,
		streamSourceRev:     m.transcriptPaint.streamSourceRev,
		rawLen:              len(raw),
		startRow:            startRow,
		contentWidth:        contentW,
		wrapWidth:           wrapWidth,
		usesWorkWidth:       usesWorkWidth,
		showHeader:          showHeader,
		isDark:              m.isDark,
		markdownRenderer:    m.md,
		blockID:             blockID,
		turnID:              turnID,
		rows:                rows,
		blocks:              blocks,
		lineMap:             reservedLineMap,
		lastRawStart:        lastRawStart,
		lastRawLogical:      uniseg.GraphemeClusterCount(raw[:lastRawStart]),
		lastRenderedStart:   lastRenderedStart,
		lastBlockStart:      lastBlockStart,
		lastLineMapStart:    lastLineMapStart,
		previousLineHasPipe: previousLineHasPipe,
	}
}

func (m *Model) updateIncrementalPlainLiveTail(
	cache *transcriptLivePaintCache,
	raw string,
) bool {
	if cache == nil ||
		!cache.valid ||
		cache.rawLen < 0 ||
		cache.rawLen >= len(raw) ||
		cache.lastRawStart < 0 ||
		cache.lastRawStart > cache.rawLen ||
		cache.lastRenderedStart < 0 ||
		cache.lastRenderedStart > len(cache.rows) ||
		cache.lastBlockStart < 0 ||
		cache.lastBlockStart > len(cache.blocks) ||
		cache.lastLineMapStart < 0 ||
		cache.lastLineMapStart > len(cache.lineMap) {
		return false
	}
	delta := raw[cache.rawLen:]
	if sanitizeTerminalMultiline(delta) != delta ||
		strings.Contains(delta, "\n\n") ||
		(cache.rawLen > 0 &&
			raw[cache.rawLen-1] == '\n' &&
			len(delta) > 0 &&
			delta[0] == '\n') {
		return false
	}

	sourceSuffix := raw[cache.lastRawStart:]
	// At prose widths the work/prose renderers have the same measure. At wider
	// terminals, inspect only the mutable raw-line suffix (plus the preceding
	// table signal) before trusting the prior renderer choice.
	if !cache.usesWorkWidth && cache.contentWidth > m.chatProseWidth() {
		workProbe := sourceSuffix
		if cache.previousLineHasPipe {
			workProbe = "|\n" + workProbe
		}
		if markdownUsesWorkWidth(workProbe) {
			return false
		}
	}

	suffixRows, nextLastRenderedRelative, previousLineHasPipe :=
		m.plainLiveTailRows(sourceSuffix, cache.wrapWidth, false)
	if !strings.Contains(sourceSuffix, "\n") {
		previousLineHasPipe = cache.previousLineHasPipe
	}
	nextLastRenderedStart := cache.lastRenderedStart + nextLastRenderedRelative
	mapRows := trimTrailingTranscriptPaintRows(suffixRows)
	suffixLineMap := semanticTranscriptLineMapFromSource(
		strings.Join(mapRows, "\n"),
		sourceSuffix,
	)
	for index := range suffixLineMap {
		suffixLineMap[index].Row += cache.lastRenderedStart
		suffixLineMap[index].LogicalOffset += cache.lastRawLogical
	}
	previousSuffixMap := cache.lineMap[cache.lastLineMapStart:]
	if len(suffixLineMap) < len(previousSuffixMap) ||
		!transcriptLineMapPrefixEqual(suffixLineMap, previousSuffixMap) {
		return false
	}

	nextLineMap := cache.lineMap
	nextLineMap = append(nextLineMap, suffixLineMap[len(previousSuffixMap):]...)
	nextRows := cache.rows[:cache.lastRenderedStart]
	nextRows = append(nextRows, suffixRows...)
	nextBlocks := cache.blocks[:cache.lastBlockStart]
	nextLastBlockStart := len(nextBlocks)
	for relativeRow, row := range suffixRows {
		if relativeRow == nextLastRenderedRelative {
			nextLastBlockStart = len(nextBlocks)
		}
		if row != "" {
			nextBlocks = append(
				nextBlocks,
				m.measureTranscriptPaintBlock(
					cache.startRow+cache.lastRenderedStart+relativeRow,
					row,
				),
			)
		}
	}
	if nextLastRenderedRelative == len(suffixRows) {
		nextLastBlockStart = len(nextBlocks)
	}

	nextLastRawRelative := strings.LastIndex(sourceSuffix, "\n") + 1
	nextLastRawLogical := cache.lastRawLogical +
		uniseg.GraphemeClusterCount(sourceSuffix[:nextLastRawRelative])
	cache.streamSourceRev = m.transcriptPaint.streamSourceRev
	cache.rawLen = len(raw)
	cache.rows = nextRows
	cache.blocks = nextBlocks
	cache.lineMap = nextLineMap
	cache.lastRawStart += nextLastRawRelative
	cache.lastRawLogical = nextLastRawLogical
	cache.lastRenderedStart = nextLastRenderedStart
	cache.lastBlockStart = nextLastBlockStart
	cache.lastLineMapStart = transcriptLineMapRowStart(
		nextLineMap,
		nextLastRenderedStart,
	)
	cache.previousLineHasPipe = previousLineHasPipe
	return true
}

func (m *Model) plainLiveTailRows(
	raw string,
	wrapWidth int,
	showHeader bool,
) ([]string, int, bool) {
	rawLines := strings.Split(raw, "\n")
	rows := make([]string, 0, len(rawLines)+1)
	if showHeader {
		rows = append(rows, m.styles.AsstLabel.Render("assistant"))
	}
	lastRenderedStart := len(rows)
	for index, rawLine := range rawLines {
		if index == len(rawLines)-1 {
			lastRenderedStart = len(rows)
		}
		wrapped := wrapLine(rawLine, wrapWidth)
		for _, row := range strings.Split(wrapped, "\n") {
			if row != "" {
				row = "  " + row
			}
			rows = append(rows, row)
		}
	}
	previousLineHasPipe := len(rawLines) > 1 &&
		strings.Contains(rawLines[len(rawLines)-2], "|")
	return rows, lastRenderedStart, previousLineHasPipe
}

func trimTrailingTranscriptPaintRows(rows []string) []string {
	end := len(rows)
	for end > 0 && rows[end-1] == "" {
		end--
	}
	return rows[:end]
}

func transcriptLineMapRowStart(lineMap LineMap, row int) int {
	return sort.Search(len(lineMap), func(index int) bool {
		return lineMap[index].Row >= row
	})
}

func transcriptLineMapPrefixEqual(current, previous LineMap) bool {
	if len(current) < len(previous) {
		return false
	}
	for index := range previous {
		if current[index] != previous[index] {
			return false
		}
	}
	return true
}

func (m *Model) measureTranscriptPaintBlock(startRow int, content string) transcriptPaintBlock {
	block := newTranscriptPaintBlock(startRow, content)
	if m.transcriptRenderProbe != nil {
		m.transcriptRenderProbe.blocksMeasured++
		m.transcriptRenderProbe.measureBytesMaterialized += len(content)
		m.transcriptRenderProbe.lineIndexRowsBuilt += block.rowCount()
	}
	return block
}

func lastTranscriptLayoutRecord(state *entryRenderState) (TranscriptLayoutRecord, bool) {
	if state == nil {
		return TranscriptLayoutRecord{}, false
	}
	if len(state.layoutRecords) > 0 {
		return state.layoutRecords[len(state.layoutRecords)-1], true
	}
	if len(state.layoutBase) > 0 {
		return state.layoutBase[len(state.layoutBase)-1], true
	}
	return TranscriptLayoutRecord{}, false
}

func transcriptRenderStateHeight(state *entryRenderState) int {
	if state == nil || !state.renderedAny {
		return 0
	}
	return max(1, state.renderedLines+1)
}

func (m *Model) bindCachedTranscriptPaintLayoutPrefix() {
	cache := &m.transcriptPaint.cache
	if !cache.valid ||
		cache.prefixLayoutCount < 0 ||
		cache.prefixLayoutCount > len(m.transcriptLayout.Records) {
		return
	}
	count := cache.prefixLayoutCount
	cache.prefixState.layoutBase = m.transcriptLayout.Records[:count:count]
	cache.prefixState.layoutRecords = nil
}

func (m *Model) syncTranscriptPaintWindow() {
	paint := &m.transcriptPaint
	if !paint.active {
		return
	}
	paint.top = min(max(0, paint.top), m.transcriptMaxTop())
	height := max(1, m.viewport.Height())
	visibleEnd := min(paint.document.totalRows, paint.top+height)
	if paint.windowGeneration == paint.documentGeneration &&
		paint.windowHeight == height &&
		paint.windowStart <= paint.top &&
		visibleEnd <= paint.windowEnd {
		m.viewport.SetYOffset(paint.top - paint.windowStart)
		return
	}

	start := max(0, paint.top-transcriptPaintOverscanRows)
	end := min(
		paint.document.totalRows,
		paint.top+height+transcriptPaintOverscanRows,
	)
	rows, blocksPainted := paint.document.materializeRows(start, end)
	m.viewport.SetContentLines(rows)
	m.viewport.SetYOffset(paint.top - start)
	paint.windowStart = start
	paint.windowEnd = end
	paint.windowHeight = height
	paint.windowGeneration = paint.documentGeneration

	if m.transcriptRenderProbe != nil {
		stagedBytes := max(0, len(rows)-1)
		for _, row := range rows {
			stagedBytes += len(row)
		}
		m.transcriptRenderProbe.paintBytesStaged += stagedBytes
		m.transcriptRenderProbe.paintRowsStaged += len(rows)
		m.transcriptRenderProbe.viewportRowsStaged += len(rows)
		m.transcriptRenderProbe.blocksPainted += blocksPainted
		m.transcriptRenderProbe.windowStart = start
		m.transcriptRenderProbe.windowEnd = end
	}
}

func (m *Model) transcriptVirtualized() bool {
	return m != nil && m.transcriptPaint.active
}

func (m *Model) transcriptYOffset() int {
	if m.transcriptVirtualized() {
		return m.transcriptPaint.top
	}
	return m.viewport.YOffset()
}

func (m *Model) transcriptMaxTop() int {
	if !m.transcriptVirtualized() {
		return max(0, m.viewport.TotalLineCount()-m.viewport.Height())
	}
	return max(0, m.transcriptPaint.document.totalRows-m.viewport.Height())
}

func (m *Model) setTranscriptYOffset(offset int) {
	if !m.transcriptVirtualized() {
		m.viewport.SetYOffset(offset)
		return
	}
	m.transcriptPaint.top = min(max(0, offset), m.transcriptMaxTop())
	m.syncTranscriptPaintWindow()
}

func (m *Model) transcriptAtBottom() bool {
	if !m.transcriptVirtualized() {
		return m.viewport.AtBottom()
	}
	return m.transcriptPaint.top >= m.transcriptMaxTop()
}

func (m *Model) transcriptPastBottom() bool {
	if !m.transcriptVirtualized() {
		return m.viewport.PastBottom()
	}
	return m.transcriptPaint.top > m.transcriptMaxTop()
}

func (m *Model) transcriptGotoTop() {
	m.setTranscriptYOffset(0)
}

func (m *Model) transcriptGotoBottom() {
	m.setTranscriptYOffset(m.transcriptMaxTop())
}

func (m *Model) scrollTranscriptBy(delta int) {
	if delta == 0 {
		return
	}
	m.setTranscriptYOffset(m.transcriptYOffset() + delta)
}

// appendTranscriptStreamText is the sole production writer for provider prose.
// Tracking the buffer length at this boundary lets paint prove append-only
// growth without comparing or hashing the complete accumulated response.
func (m *Model) appendTranscriptStreamText(value string) {
	if value == "" {
		return
	}
	m.adoptTranscriptStreamSource()
	m.streamBuf.WriteString(value)
	m.transcriptPaint.streamSourceLen += len(value)
	incrementTranscriptPaintGeneration(&m.transcriptPaint.streamSourceRev)
}

// resetTranscriptStreamText starts a new append episode. A reset is never
// admitted as an append even if the replacement happens to share the old
// response's length or prefix.
func (m *Model) resetTranscriptStreamText() {
	m.streamBuf.Reset()
	incrementTranscriptPaintGeneration(&m.transcriptPaint.streamSourceEpoch)
	incrementTranscriptPaintGeneration(&m.transcriptPaint.streamSourceRev)
	m.transcriptPaint.streamSourceTracked = true
	m.transcriptPaint.streamSourceLen = 0
	m.transcriptPaint.liveCache.valid = false
}

// syncTranscriptStreamSource reports whether every mutation since the previous
// paint passed through appendTranscriptStreamText. Tests and restore paths may
// write the builder directly; they are adopted as a fresh cold episode rather
// than being trusted as append-only.
func (m *Model) syncTranscriptStreamSource() bool {
	paint := &m.transcriptPaint
	if !paint.streamSourceTracked || paint.streamSourceLen != m.streamBuf.Len() {
		incrementTranscriptPaintGeneration(&paint.streamSourceEpoch)
		incrementTranscriptPaintGeneration(&paint.streamSourceRev)
		paint.streamSourceTracked = true
		paint.streamSourceLen = m.streamBuf.Len()
		return false
	}
	return true
}

func (m *Model) adoptTranscriptStreamSource() {
	paint := &m.transcriptPaint
	if paint.streamSourceTracked && paint.streamSourceLen == m.streamBuf.Len() {
		return
	}
	incrementTranscriptPaintGeneration(&paint.streamSourceEpoch)
	incrementTranscriptPaintGeneration(&paint.streamSourceRev)
	paint.streamSourceTracked = true
	paint.streamSourceLen = m.streamBuf.Len()
	paint.liveCache.valid = false
}

func incrementTranscriptPaintGeneration(generation *uint64) {
	if generation != nil && *generation < ^uint64(0) {
		*generation++
	}
}

// updateTranscriptViewport is the smart-parent input boundary for transcript
// navigation. Vertical messages update global logical coordinates; Bubbles
// receives only horizontal input and paints the already-bounded local rows.
func (m *Model) updateTranscriptViewport(msg tea.Msg) tea.Cmd {
	if !m.transcriptVirtualized() {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return cmd
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.viewport.KeyMap.PageDown):
			m.scrollTranscriptBy(m.viewport.Height())
		case key.Matches(msg, m.viewport.KeyMap.PageUp):
			m.scrollTranscriptBy(-m.viewport.Height())
		case key.Matches(msg, m.viewport.KeyMap.HalfPageDown):
			m.scrollTranscriptBy(m.viewport.Height() / 2) //nolint:mnd
		case key.Matches(msg, m.viewport.KeyMap.HalfPageUp):
			m.scrollTranscriptBy(-m.viewport.Height() / 2) //nolint:mnd
		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return cmd
		}
	case tea.MouseWheelMsg:
		if !m.viewport.MouseWheelEnabled {
			return nil
		}
		if msg.Mod.Contains(tea.ModShift) {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return cmd
		}
		switch msg.Button {
		case tea.MouseWheelDown:
			m.scrollTranscriptBy(m.viewport.MouseWheelDelta)
		case tea.MouseWheelUp:
			m.scrollTranscriptBy(-m.viewport.MouseWheelDelta)
		case tea.MouseWheelLeft, tea.MouseWheelRight:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return cmd
		}
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return cmd
	}
	return nil
}

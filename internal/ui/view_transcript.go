package ui

import (
	"fmt"
	"hash/fnv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/local-agent/internal/imageasset"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func (m *Model) renderSystemNotice(content string, contentW int) string {
	const label = "notice · "
	available := max(1, contentW-m.styles.SystemText.GetPaddingLeft())
	plain := label + sanitizeTerminalMultiline(content)
	return m.styles.SystemText.Render(wrapText(plain, available))
}

// renderEntries builds the full chat content for the viewport.
// Uses an incremental cache: during streaming, only the streaming tail is
// re-rendered while the entries prefix is reused from cache.
// entriesFromMessages rebuilds the visible chat transcript from a restored
// agent message history. User and assistant text become chat entries; tool
// messages are omitted from the visual (they remain in the agent's context for
// the model) since re-rendering them as cards would need the tool-entry state
// that the snapshot doesn't carry.
func entriesFromMessages(msgs []llm.Message) []ChatEntry {
	var entries []ChatEntry
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			if msg.Content != "" {
				entries = append(entries, ChatEntry{Kind: "user", Content: msg.Content, Attachments: imageRefsFromMessages(msg.Images)})
			}
		case "assistant":
			if msg.Content != "" {
				entries = append(entries, ChatEntry{Kind: "assistant", Content: msg.Content})
			}
		}
	}
	return entries
}

func (m *Model) renderEntries() string {
	contentW := m.chatContentWidth()

	// The memo is keyed by entry index, so a shrunken slice may leave stale
	// higher-index chunks behind. Keys self-validate, but drop them wholesale
	// so the map never outgrows the transcript it describes.
	if len(m.entries) < m.entryMemoLen {
		m.resetEntryMemo()
	}
	m.entryMemoLen = len(m.entries)

	// Welcome message when no user messages yet
	hasUserMsg := false
	for _, e := range m.entries {
		if e.Kind == "user" || e.Kind == "assistant" {
			hasUserMsg = true
			break
		}
	}
	if !hasUserMsg && !m.hasVisibleLiveTurn() {
		var b strings.Builder
		m.renderWelcome(&b)
		hasNotice := false
		for _, entry := range m.entries {
			if entry.Kind == "system" || entry.Kind == "error" {
				hasNotice = true
				break
			}
		}
		if !hasNotice {
			welcome := strings.TrimRight(b.String(), "\n")
			top := max(0, (m.viewport.Height()-lipgloss.Height(welcome))/2)
			return strings.Repeat("\n", top) + welcome
		}
		// PlaceHorizontal owns a rectangular block and does not retain the
		// welcome builder's trailing newline. Start notices on a real row so a
		// long left padding cannot push their first line beyond the viewport.
		b.WriteByte('\n')
		// Append any system entries (e.g. failed server notices) below welcome
		for _, e := range m.entries {
			switch e.Kind {
			case "system":
				b.WriteString(m.renderSystemNotice(e.Content, contentW))
				b.WriteString("\n\n")
			case "error":
				if notice, ok := compactOllamaStartupNotice(e.Content, contentW, m.ollamaOffline); ok {
					// At the supported 30-column tier the generic error frame can
					// consume the whole viewport and hide the empty-state recovery
					// paths. Keep the raw ChatEntry unchanged and project only this
					// host-authored startup diagnostic into a bounded notice.
					b.WriteString(m.styles.ErrorText.Render(notice))
					b.WriteByte('\n')
				} else if isOllamaStartupRecovery(e.Content, m.ollamaOffline) {
					// Missing startup inventory is an actionable empty state, not a
					// failed user operation. Preserve the detailed host recovery copy
					// at ordinary widths without adding the generic red error label.
					b.WriteString(m.renderSystemNotice(e.Content, contentW))
					b.WriteString("\n\n")
				} else {
					m.renderEntryError(&b, e.Content, contentW)
				}
			}
		}
		return b.String()
	}

	// Fast path: the cached stable prefix is current. Only live entries (a
	// still-running tool group and anything after it) and the streaming tail
	// render per frame, so spinner ticks and stream chunks never walk the
	// whole transcript again.
	if m.entryCacheValid && len(m.entries) == m.cachedEntryCount {
		m.toolHitRegions = append(m.toolHitRegions[:0], m.cachedToolHitRegions...)
		m.thinkingHitRegions = append(m.thinkingHitRegions[:0], m.cachedThinkingHitRegions...)
		var b strings.Builder
		b.WriteString(m.cachedEntriesRender)
		state := m.cachedPrefixState
		for index := m.cachedStableCount; index < len(m.entries); index++ {
			m.renderEntryInto(&b, index, contentW, &state)
		}
		m.renderLiveTail(&b, contentW, &state)
		return b.String()
	}

	// Full render: cache the stable prefix (everything before the first
	// still-running tool group, whose card animates a glyph and elapsed time),
	// then render live entries and the streaming tail outside the cache.
	stableCount := m.stableEntryPrefixLen()
	var b strings.Builder
	m.toolHitRegions = m.toolHitRegions[:0]
	m.thinkingHitRegions = m.thinkingHitRegions[:0]
	var state entryRenderState
	snapshotPrefix := func() {
		m.cachedEntriesRender = b.String()
		m.cachedEntryCount = len(m.entries)
		m.cachedStableCount = stableCount
		m.cachedPrefixState = state
		m.cachedToolHitRegions = append(m.cachedToolHitRegions[:0], m.toolHitRegions...)
		m.cachedThinkingHitRegions = append(m.cachedThinkingHitRegions[:0], m.thinkingHitRegions...)
		m.entryCacheValid = true
	}
	for index := range m.entries {
		if index == stableCount {
			snapshotPrefix()
		}
		m.renderEntryInto(&b, index, contentW, &state)
	}
	if stableCount == len(m.entries) {
		snapshotPrefix()
	}
	m.renderLiveTail(&b, contentW, &state)
	return b.String()
}

// entryRenderState carries the transcript loop state across the cached stable
// prefix so live entries and the streaming tail continue the exact separator,
// role-header, and hit-region arithmetic of a full render.
type entryRenderState struct {
	renderedLines    int
	previousKind     string
	renderedAny      bool
	assistantStarted bool
}

// stableEntryPrefixLen returns how many leading entries render identically
// between appends. Everything from the first still-running tool group on is
// live: its card animates every spinner tick and must stay out of the cache.
func (m *Model) stableEntryPrefixLen() int {
	for index, entry := range m.entries {
		if entry.Kind == "tool_group" && m.toolGroupLive(entry.ToolIndex) {
			return index
		}
	}
	return len(m.entries)
}

func (m *Model) toolGroupLive(toolIdx int) bool {
	if toolIdx < 0 || toolIdx >= len(m.toolEntries) {
		return false
	}
	te := m.toolEntries[toolIdx]
	if te.Status == ToolStatusRunning {
		return true
	}
	for i := len(m.toolCardMgr.Cards) - 1; i >= 0; i-- {
		card := &m.toolCardMgr.Cards[i]
		if toolCallMatches(te.ID, te.Name, card.ID, card.Name) {
			return card.State == ToolCardRunning
		}
	}
	return false
}

// entryRenderMemo is one settled entry's rendered chunk plus the composite
// key it was rendered under. A key mismatch means the memo is stale and the
// entry renders fresh; hit regions are recomputed from the chunk either way.
type entryRenderMemo struct {
	key   string
	chunk string
}

func (m *Model) renderEntryInto(b *strings.Builder, entryIndex, contentW int, state *entryRenderState) {
	entry := m.entries[entryIndex]
	showHeader := !state.assistantStarted
	memoKey := m.entryMemoKey(entry, contentW, showHeader)
	var chunk string
	hit := false
	if memoKey != "" {
		if memo, ok := m.entryMemo[entryIndex]; ok && memo.key == memoKey {
			chunk = memo.chunk
			hit = true
		}
	}
	if !hit {
		var entryView strings.Builder
		switch entry.Kind {
		case "user":
			m.renderUserMsg(&entryView, entry.Content, entry.Attachments, contentW)
		case "assistant":
			m.renderAssistantMsg(&entryView, entry, contentW, showHeader)
		case "tool_group":
			m.renderToolGroup(&entryView, entry.ToolIndex)
		case "error":
			m.renderEntryError(&entryView, entry.Content, contentW)
		case "system":
			entryView.WriteString(m.renderSystemNotice(entry.Content, contentW))
			entryView.WriteString("\n")
		}
		chunk = strings.TrimRight(entryView.String(), "\n")
		if memoKey != "" {
			if m.entryMemo == nil {
				m.entryMemo = make(map[int]entryRenderMemo)
			}
			m.entryMemo[entryIndex] = entryRenderMemo{key: memoKey, chunk: chunk}
		}
	}
	if entry.Kind == "user" {
		state.assistantStarted = false
	}
	if chunk == "" {
		return
	}
	if entry.Kind == "assistant" {
		state.assistantStarted = true
	}
	m.appendEntryChunk(b, entryIndex, entry, chunk, state)
}

// appendEntryChunk applies one rendered entry chunk to the transcript
// builder: separator, hit regions derived from the chunk and the running
// line count, and loop-state bookkeeping. Fresh renders and memo hits share
// this path exactly, so a memoized frame is byte- and region-identical.
func (m *Model) appendEntryChunk(b *strings.Builder, entryIndex int, entry ChatEntry, chunk string, state *entryRenderState) {
	if state.renderedAny {
		separator := transcriptEntrySeparator(state.previousKind, entry.Kind)
		b.WriteString(separator)
		state.renderedLines += strings.Count(separator, "\n")
	}
	if entry.Kind == "tool_group" {
		header, _, _ := strings.Cut(chunk, "\n")
		m.toolHitRegions = append(m.toolHitRegions, toolHitRegion{
			ToolIndex: entry.ToolIndex,
			Row:       state.renderedLines,
			EndCol:    lipgloss.Width(header),
		})
	}
	if entry.Kind == "assistant" && strings.TrimSpace(entry.ThinkingContent) != "" {
		if rowOffset, endCol, ok := completedThinkingHeaderRegion(chunk); ok {
			m.thinkingHitRegions = append(m.thinkingHitRegions, thinkingHitRegion{
				EntryIndex: entryIndex,
				Row:        state.renderedLines + rowOffset,
				EndCol:     endCol,
				Digest:     reasoningReceiptDigest(entry.ThinkingContent),
			})
		}
	}
	b.WriteString(chunk)
	state.renderedLines += strings.Count(chunk, "\n")
	state.previousKind = entry.Kind
	state.renderedAny = true
}

func fnv64(parts ...string) uint64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
	}
	return h.Sum64()
}

// entryMemoKey builds the composite key capturing every input that affects
// one entry's rendered chunk. An empty key means the entry must not be
// memoized this frame (a live tool group animates every spinner tick).
func (m *Model) entryMemoKey(entry ChatEntry, contentW int, showHeader bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%d|%t|%t", entry.Kind, contentW, m.isDark, m.toolsCollapsed)
	switch entry.Kind {
	case "user":
		fmt.Fprintf(&b, "|%d|%x|%d", len(entry.Content), fnv64(entry.Content), len(entry.Attachments))
	case "assistant":
		fmt.Fprintf(&b, "|%x|%t|%t|%t",
			fnv64(entry.Content, "\x00", entry.ThinkingContent),
			entry.ThinkingCollapsed, entry.RenderedContent != "", showHeader)
	case "tool_group":
		toolKey, ok := m.toolGroupMemoKey(entry.ToolIndex)
		if !ok {
			return ""
		}
		b.WriteString(toolKey)
	case "error", "system":
		fmt.Fprintf(&b, "|%x", fnv64(entry.Content))
	}
	return b.String()
}

// toolGroupMemoKey summarizes every ToolEntry and matched-card input that
// renderToolGroup reads for a settled receipt. It reports ok=false for live
// groups, which must re-render every frame for the spinner and elapsed time.
func (m *Model) toolGroupMemoKey(toolIdx int) (string, bool) {
	if toolIdx < 0 || toolIdx >= len(m.toolEntries) {
		return "", false
	}
	te := m.toolEntries[toolIdx]
	if te.Status == ToolStatusRunning {
		return "", false
	}
	var card *ToolCard
	for i := len(m.toolCardMgr.Cards) - 1; i >= 0; i-- {
		if toolCallMatches(te.ID, te.Name, m.toolCardMgr.Cards[i].ID, m.toolCardMgr.Cards[i].Name) {
			card = &m.toolCardMgr.Cards[i]
			break
		}
	}
	if card != nil && card.State == ToolCardRunning {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "|%x|%d|%t|%d|%t|%t|%d|%d|%d|%d",
		fnv64(te.ID, "\x00", te.Name, "\x00", te.Summary),
		te.Status, te.Collapsed, te.Duration, te.IsError, te.DiffPending,
		te.DiffGeneration, len(te.DiffLines), len(te.Result), m.chatPaneWidth())
	p := te.Projection
	fmt.Fprintf(&b, "|%s|%s|%s|%s|%s|%t|%s|%+v",
		p.Specialist, p.Operation, p.Role, p.Transport, p.Domain, p.DomainTyped, p.Evidence, p.Route)
	if p.Digest != nil {
		fmt.Fprintf(&b, "|%+v", *p.Digest)
	}
	if p.Artifact != nil {
		fmt.Fprintf(&b, "|%+v", *p.Artifact)
	}
	if te.ExpertProgress != nil {
		fmt.Fprintf(&b, "|ep%d", te.ExpertProgress.Sequence)
	}
	if card != nil {
		fmt.Fprintf(&b, "|c%d|%t", card.State, card.Expanded)
		if card.ExpertProgress != nil {
			fmt.Fprintf(&b, "|cep%d", card.ExpertProgress.Sequence)
		}
	}
	return b.String(), true
}

// renderLiveTail renders the in-flight provider turn (streaming text or the
// inline waiting label). Provider calls can legitimately produce no tokens
// while compacting, awaiting permission, or continuing after a tool receipt;
// keeping that phase next to the last transcript event avoids a tall blank
// viewport above the footer.
func (m *Model) renderLiveTail(b *strings.Builder, contentW int, state *entryRenderState) {
	if m.hasLiveTurnContent() {
		if state.renderedAny {
			b.WriteString(transcriptEntrySeparator(state.previousKind, "assistant"))
		}
		m.renderStreamingMsg(b, m.streamBuf.String(), contentW, !state.assistantStarted)
	} else if label := m.inlineTurnActivity(); label != "" {
		if state.renderedAny {
			b.WriteString(transcriptEntrySeparator(state.previousKind, "assistant"))
		}
		m.renderInlineTurnActivity(b, label, contentW, !state.assistantStarted)
	}
}

func completedThinkingHeaderRegion(rendered string) (rowOffset, endCol int, ok bool) {
	for row, line := range strings.Split(rendered, "\n") {
		plain := strings.TrimLeft(ansi.Strip(line), " ")
		if strings.HasPrefix(plain, "│ ▸") || strings.HasPrefix(plain, "│ ▾") {
			return row, lipgloss.Width(line), true
		}
	}
	return 0, 0, false
}

func (m *Model) hasLiveTurn() bool {
	return m.hasLiveTurnContent()
}

func (m *Model) hasVisibleLiveTurn() bool {
	return m.hasLiveTurnContent() || m.inlineTurnActivity() != ""
}

func (m *Model) hasLiveTurnContent() bool {
	return strings.TrimSpace(m.streamBuf.String()) != "" || strings.TrimSpace(m.thinkBuf.String()) != ""
}

func (m *Model) inlineTurnActivity() string {
	if m == nil || m.hasLiveTurnContent() || m.toolsPending > 0 {
		return ""
	}
	if m.pendingApproval != nil {
		return "Waiting for permission below…"
	}
	if m.compactingContext {
		return "Preparing context…"
	}
	if m.state == StateWaiting || m.state == StateStreaming {
		return "Waiting for model…"
	}
	return ""
}

func (m *Model) renderInlineTurnActivity(b *strings.Builder, label string, contentW int, showHeader bool) {
	label = sanitizeTerminalSingleLine(label)
	if label == "" {
		return
	}
	if showHeader {
		m.renderAssistantHeader(b, contentW)
	}
	b.WriteString(indentBlock(m.styles.StreamHint.Render(label), "  "))
	b.WriteString("\n")
}

// transcriptEntrySeparator is the single owner of vertical rhythm between
// transcript entries. Consecutive compact receipts form a dense stack; every
// other semantic boundary gets exactly one blank row.
func transcriptEntrySeparator(previous, current string) string {
	if previous == "tool_group" && current == "tool_group" {
		return "\n"
	}
	if previous == "system" && current == "system" {
		return "\n"
	}
	return "\n\n"
}

func (m *Model) renderEntryError(b *strings.Builder, content string, contentW int) {
	content = strings.TrimSpace(sanitizeTerminalMultiline(content))
	if content == "" {
		content = "The operation failed without an error message."
	}
	b.WriteString("  " + m.styles.ErrorChip.Render("✗ error"))
	b.WriteString("\n")
	b.WriteString(m.styles.ToolErrorText.Render(indentBlock(wrapText(content, max(1, contentW-2)), "  ")))
	b.WriteString("\n\n")
}

// renderUserMsg renders a user message block: a compact role label above an
// accent-guttered content block. The gutter, not a full-width rule, carries
// the visual identity so the transcript keeps a calm vertical rhythm.
func (m *Model) renderUserMsg(b *strings.Builder, content string, attachments []imageasset.Ref, contentW int) {
	content = sanitizeTerminalMultiline(content)
	b.WriteString(m.styles.UserLabel.Render("you"))
	b.WriteString("\n")
	gutter := "  " + m.styles.UserGutter.Render("▌") + " "
	text := m.styles.UserContent.UnsetPaddingLeft()
	for _, line := range strings.Split(wrapText(content, max(10, contentW-4)), "\n") {
		b.WriteString(gutter + text.Render(line))
		b.WriteString("\n")
	}
	if len(attachments) > 0 {
		b.WriteString(m.renderImageAttachmentSummary(attachments, contentW))
		b.WriteString("\n")
	}
}

// renderAssistantMsg renders a completed assistant message block.
// Uses cached RenderedContent if available (snap-into-place pattern).
func (m *Model) renderAssistantMsg(b *strings.Builder, entry ChatEntry, contentW int, showHeader bool) {
	content := sanitizeTerminalMultiline(entry.Content)
	hasContent := strings.TrimSpace(content) != ""
	hasThinking := strings.TrimSpace(entry.ThinkingContent) != ""
	if !hasContent && !hasThinking {
		return
	}

	if showHeader {
		m.renderAssistantHeader(b, contentW)
	}

	// Reasoning belongs to this assistant turn, so its disclosure follows the
	// role header instead of appearing as an unowned block above it.
	if hasThinking {
		thinkBox := m.renderThinkingBox(entry.ThinkingContent, entry.ThinkingCollapsed)
		b.WriteString(indentBlock(thinkBox, "  "))
		b.WriteString("\n")
	}
	if !hasContent {
		return
	}

	// Use cached rendered content if available.
	rendered := entry.RenderedContent
	if content != entry.Content {
		// Cached Glamour output is trusted only when it was derived from the same
		// sanitized source. Restored or synthetic entries must be rendered again.
		rendered = ""
	}
	if rendered == "" {
		rendered = content
		if m.md != nil {
			rendered = m.md.RenderFull(rendered)
		}
	}
	// Trim excessive trailing whitespace from Glamour output.
	rendered = strings.TrimRight(rendered, " \t\n")
	rendered = indentBlock(rendered, "  ")
	b.WriteString(rendered)
	b.WriteString("\n")
}

// renderStreamingMsg renders the in-progress assistant message (plain text).
func (m *Model) renderStreamingMsg(b *strings.Builder, content string, contentW int, showHeader bool) {
	content = sanitizeTerminalMultiline(content)
	hasContent := strings.TrimSpace(content) != ""
	hasThinking := strings.TrimSpace(m.thinkBuf.String()) != ""
	if !hasContent && !hasThinking {
		return
	}

	if showHeader {
		m.renderAssistantHeader(b, contentW)
	}

	// Live reasoning uses the same assistant-owned hierarchy as the completed
	// disclosure. Keeping it compact prevents token-by-token height jitter; the
	// full receipt becomes expandable only after the turn settles.
	if hasThinking {
		b.WriteString(indentBlock(m.renderLiveThinkingBox(m.thinkBuf.String()), "  "))
		b.WriteString("\n")
	}
	if !hasContent {
		return
	}

	// During streaming: render the stable markdown prefix with Glamour (cached)
	// and only the trailing partial paragraph as plain wrapped text. This shows
	// formatted output live instead of popping into shape on completion, while
	// avoiding the jitter of re-rendering incomplete markdown.
	wrapWidth := contentW - 2
	if wrapWidth < 10 {
		wrapWidth = 10
	}

	var formatted, tail string
	if m.md != nil {
		formatted, tail = m.md.RenderStreamingFormatted(content)
	} else {
		tail = content
	}

	if formatted != "" {
		b.WriteString(indentBlock(strings.TrimRight(formatted, " \t\n"), "  "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(tail) != "" {
		b.WriteString(indentBlock(wrapText(tail, wrapWidth), "  "))
		b.WriteString("\n")
	}
}

func (m *Model) renderAssistantHeader(b *strings.Builder, _ int) {
	// The operational footer owns the one active animation. Keeping the role
	// header static makes streamed reasoning feel like transcript content rather
	// than a second competing progress indicator. A compact label without a
	// full-width rule keeps consecutive turns readable without heavy chrome.
	b.WriteString(m.styles.AsstLabel.Render("assistant"))
	b.WriteString("\n")
}

// renderToolGroup renders one tight tool receipt. The parent transcript owns
// all spacing between this block and its neighbors.
func (m *Model) renderToolGroup(b *strings.Builder, toolIdx int) {
	if toolIdx < 0 || toolIdx >= len(m.toolEntries) {
		return
	}
	te := m.toolEntries[toolIdx]
	layout := m.currentLayout()

	// Find corresponding tool card
	var card *ToolCard
	for i := len(m.toolCardMgr.Cards) - 1; i >= 0; i-- {
		if toolCallMatches(te.ID, te.Name, m.toolCardMgr.Cards[i].ID, m.toolCardMgr.Cards[i].Name) {
			card = &m.toolCardMgr.Cards[i]
			break
		}
	}

	if card != nil {
		// Use fancy tool card rendering
		card.Expanded = !te.Collapsed
		// Keep the card inside the actual viewport; the two-column left indent is
		// applied immediately below.
		availableWidth := max(4, m.chatPaneWidth()-4)
		if card.ExpertProgress != nil &&
			(card.expertProgressCacheWidth != availableWidth-2 ||
				card.expertProgressCacheSequence != card.ExpertProgress.Sequence) {
			card.setExpertProgress(card.ExpertProgress, availableWidth-2)
		}
		cardView := card.View(availableWidth)
		if card.State == ToolCardRunning {
			glyph := "…"
			if !m.reducedMotion {
				glyph = m.spin.View()
			}
			elapsed := m.nowTime().Sub(card.StartTime)
			if elapsed < 0 {
				elapsed = 0
			}
			cardView = card.ViewWithActivity(availableWidth, glyph, elapsed)
		}
		if card.Expanded && card.State != ToolCardRunning {
			var diffView string
			if te.DiffPending {
				diffView = renderDiffLoadingAtWidth(te.Summary, m.styles, availableWidth)
			} else if len(te.DiffLines) > 0 {
				diffView = strings.TrimRight(
					renderUnifiedDiffAtWidth(te.Summary, te.DiffLines, m.styles, 0, availableWidth), "\n",
				)
			}
			if diffView != "" {
				cardView += "\n" + diffView
			}
		}
		// Add left padding to align with message content
		cardView = indentBlock(cardView, "  ")
		b.WriteString(cardView)
	} else {
		// Fallback to basic rendering if no card exists
		tt := classifyTool(te.Name)
		toolName := safeToolIdentifier(te.Name)
		projectedState := toolCardStateFromProjection(te.Projection)
		if te.Status == ToolStatusDone && projectedState != ToolCardSuccess {
			// A missing live/restored card must not erase the bounded semantic
			// projection. In particular, transport success with an unknown domain
			// outcome remains attention-colored instead of falling through to the
			// legacy green completion receipt.
			kind := toolCardKindForTool(te.Name)
			fallback := NewToolCard(te.Name, kind, m.isDark)
			fallback.ID = te.ID
			fallback.State = projectedState
			fallback.SetSummary(te.Summary)
			fallback.Args = te.Args
			fallback.ResultLanguage = te.ResultLanguage
			fallback.Result = te.Result
			fallback.Duration = te.Duration
			fallback.Expanded = !te.Collapsed
			fallback.Projection = te.Projection
			fallback.setExpertProgress(te.ExpertProgress, max(1, m.chatPaneWidth()-6))
			fallback.State = te.ExpertProgress.cardState(fallback.State)
			cardView := fallback.View(max(4, m.chatPaneWidth()-4))
			if fallback.Expanded {
				var diffView string
				if te.DiffPending {
					diffView = renderDiffLoadingAtWidth(te.Summary, m.styles, max(4, m.chatPaneWidth()-4))
				} else if len(te.DiffLines) > 0 {
					diffView = strings.TrimRight(renderUnifiedDiffAtWidth(
						te.Summary, te.DiffLines, m.styles, 0, max(4, m.chatPaneWidth()-4),
					), "\n")
				}
				if diffView != "" {
					cardView += "\n" + diffView
				}
			}
			b.WriteString(indentBlock(cardView, "  "))
			return
		}

		switch te.Status {
		case ToolStatusRunning:
			// Running: show spinner with type-specific icon
			icon := m.styles.ToolCallIcon.Render(toolIcon(tt, te.Status))
			spinView := "…"
			if !m.reducedMotion {
				spinView = m.spin.View()
			}
			text := m.styles.ToolCallText.Render(fmt.Sprintf(" %s ", toolName))
			hint := m.styles.ToolRunningText.Render(spinView + " running...")
			b.WriteString(icon + text + hint)
			// For running bash tools, show command inline
			if tt == ToolTypeBash {
				if summary := sanitizeTerminalSingleLine(toolSummary(tt, te)); summary != "" {
					b.WriteString("\n")
					b.WriteString(m.styles.ToolBashCmd.Render(layout.ToolIndent + "$ " + summary))
				}
			}
			b.WriteString("\n")

		case ToolStatusDone:
			dur := formatDuration(te.Duration)
			icon := m.styles.ToolDoneIcon.Render(toolIcon(tt, te.Status))
			if te.Collapsed {
				// Collapsed: single line with type-specific summary
				text := m.styles.ToolDoneText.Render(fmt.Sprintf(" %s (%s)", toolName, dur))
				b.WriteString(icon + text)
				if summary := sanitizeTerminalSingleLine(toolSummary(tt, te)); summary != "" {
					summ := truncate(summary, layout.ToolSummaryMax)
					b.WriteString(m.styles.ToolBashCmd.Render(" " + summ))
				}
				b.WriteString("\n")
			} else {
				// Expanded: show args + result (or diff for file writes)
				text := m.styles.ToolDoneText.Render(fmt.Sprintf(" %s (%s)", toolName, dur))
				b.WriteString(icon + text)
				b.WriteString("\n")
				// Args
				args := truncate(sanitizeTerminalSingleLine(te.Args), layout.ArgsTruncMax)
				b.WriteString(m.styles.ToolDetailText.Render(layout.ToolIndent + "args: " + args))
				b.WriteString("\n")
				// Diff or result
				diffWidth := max(1, m.chatPaneWidth()-4)
				if te.DiffPending {
					b.WriteString(renderDiffLoadingAtWidth(te.Summary, m.styles, diffWidth))
					b.WriteString("\n")
				} else if len(te.DiffLines) > 0 {
					b.WriteString(renderUnifiedDiffAtWidth(te.Summary, te.DiffLines, m.styles, 0, diffWidth))
				} else {
					// Use smart result formatting with truncation
					result := formatToolResult(te.Result, 20, layout.ResultTruncMax)
					resultLines := strings.Count(result, "\n") + 1
					if resultLines > 20 {
						b.WriteString(m.styles.ToolDetailText.Render(layout.ToolIndent + "result (truncated, expand to see more):\n"))
						b.WriteString(m.styles.ToolDetailText.Render(indentBlock(truncate(result, layout.ResultTruncMax), layout.ToolIndent)))
					} else {
						b.WriteString(m.styles.ToolDetailText.Render(layout.ToolIndent + "result:\n"))
						b.WriteString(m.styles.ToolDetailText.Render(indentBlock(result, layout.ToolIndent)))
					}
					b.WriteString("\n")
				}
			}

		case ToolStatusError:
			// Error: always expanded regardless of collapse state
			dur := formatDuration(te.Duration)
			icon := m.styles.ToolErrorIcon.Render(toolIcon(tt, te.Status))
			text := m.styles.ToolErrorText.Render(fmt.Sprintf(" %s (%s)", toolName, dur))
			b.WriteString(icon + text)
			b.WriteString("\n")
			// Error result always shown
			result := truncate(sanitizeTerminalMultiline(te.Result), layout.ResultTruncMax)
			b.WriteString(m.styles.ToolErrorText.Render(layout.ToolIndent + result))
			b.WriteString("\n")
		}
	}
}

// indentBlock adds a prefix to each line of a multi-line string.
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

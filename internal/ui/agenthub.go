package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const agentHubMaximumWidth = 68

type agentHubMode uint8

const (
	agentHubListMode agentHubMode = iota
	agentHubViewerMode
)

type agentHubItem struct {
	ordinal int
	group   AgentGroupProjection
}

func (item agentHubItem) Title() string {
	return sanitizeTerminalSingleLine(fmt.Sprintf(
		"Consultation %d · %s",
		item.ordinal,
		agentGroupStatusLabel(item.group),
	))
}

func (item agentHubItem) Description() string {
	return sanitizeTerminalSingleLine(agentGroupSummary(item.group))
}

func (item agentHubItem) FilterValue() string {
	parts := []string{
		item.Title(),
		item.Description(),
		string(item.group.Strategy),
	}
	for _, node := range item.group.Nodes {
		parts = append(parts, node.Label, node.Model, node.FailureCode, string(node.Location))
	}
	return sanitizeTerminalSingleLine(strings.Join(parts, " "))
}

// AgentHubState is a presentation-only Bubbles surface. The parent Model owns
// every message, projection refresh, focus transition, and transcript jump.
type AgentHubState struct {
	List             list.Model
	Viewer           viewport.Model
	Mode             agentHubMode
	Surface          AgentSurfaceProjection
	ViewerGroupID    BlockID
	Unavailable      bool
	ItemHeight       int
	ItemSpacing      int
	width            int
	height           int
	isDark           bool
	reducedMotion    bool
	compact          bool
	viewerContentKey string
	viewerRows       []agentViewerRowAnchor
}

// agentViewerRowAnchor keeps scroll ownership attached to semantic work rather
// than to a physical row number. A node can gain a metadata row while running,
// or collapse from two rows to one at a wider terminal width.
type agentViewerRowAnchor struct {
	key    string
	nodeID string
	subrow int
}

type agentViewerLayout struct {
	content string
	rows    []agentViewerRowAnchor
}

func newAgentHubState(
	surface AgentSurfaceProjection,
	unavailable bool,
	terminalWidth int,
	terminalHeight int,
	isDark bool,
	reducedMotion bool,
) *AgentHubState {
	if !surface.valid() {
		surface = AgentSurfaceProjection{}
		unavailable = true
	}
	surface = cloneAgentSurfaceProjection(surface)
	items := agentHubItems(surface)
	compact := compactAgentHub(terminalWidth, terminalHeight)
	delegate := newPickerDelegate(isDark, compact)
	l := list.New(items, delegate, 1, 1)
	configurePickerList(&l, isDark, reducedMotion)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(len(items) > 0)
	l.DisableQuitKeybindings()
	l.SetStatusBarItemName("agent consultation", "agent consultations")

	state := &AgentHubState{
		List:          l,
		Viewer:        viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		Mode:          agentHubListMode,
		Surface:       surface,
		Unavailable:   unavailable,
		ItemHeight:    delegate.Height(),
		ItemSpacing:   delegate.Spacing(),
		width:         terminalWidth,
		height:        terminalHeight,
		isDark:        isDark,
		reducedMotion: reducedMotion,
		compact:       compact,
	}
	state.configureListTitle()
	state.SetSize(terminalWidth, terminalHeight)
	state.selectDefaultGroup()
	return state
}

func cloneAgentSurfaceProjection(surface AgentSurfaceProjection) AgentSurfaceProjection {
	cloned := AgentSurfaceProjection{
		Groups:        make([]AgentGroupProjection, len(surface.Groups)),
		OmittedGroups: surface.OmittedGroups,
	}
	for index, group := range surface.Groups {
		cloned.Groups[index] = group
		cloned.Groups[index].Nodes = append([]WorkNode(nil), group.Nodes...)
	}
	return cloned
}

func agentHubItems(surface AgentSurfaceProjection) []list.Item {
	items := make([]list.Item, len(surface.Groups))
	for index, group := range surface.Groups {
		items[index] = agentHubItem{ordinal: index + 1, group: group}
	}
	return items
}

func compactAgentHub(width, height int) bool {
	return width <= 40 || height <= 16
}

func (state *AgentHubState) configureListTitle() {
	if state == nil {
		return
	}
	title := "Agents"
	if count := len(state.Surface.Groups); count > 0 {
		title = fmt.Sprintf("Agents · %d %s", count, pluralizeNoun(count, "consultation", "consultations"))
	}
	if state.Surface.OmittedGroups > 0 {
		title += fmt.Sprintf(" · +%d older", state.Surface.OmittedGroups)
	}
	state.List.Title = title
	setSettingsTitleDensity(&state.List, state.compact)
}

func (state *AgentHubState) SetProjection(surface AgentSurfaceProjection, unavailable bool) tea.Cmd {
	if state == nil {
		return nil
	}
	if !surface.valid() {
		surface = AgentSurfaceProjection{}
		unavailable = true
	}
	surface = cloneAgentSurfaceProjection(surface)
	selectedID := state.selectedGroupID()
	viewerID := state.ViewerGroupID
	filterState := state.List.FilterState()
	filterText := state.List.FilterInput.Value()
	filterCursor := state.List.FilterInput.Position()

	state.Surface = surface
	state.Unavailable = unavailable
	items := agentHubItems(surface)
	state.List.SetFilteringEnabled(len(items) > 0)
	_ = state.List.SetItems(items)
	if len(items) == 0 {
		state.List.ResetFilter()
	} else {
		state.applyFilterSynchronously(filterState, filterText)
		if filterState == list.Filtering {
			state.List.FilterInput.SetCursor(min(
				max(0, filterCursor),
				utf8.RuneCountInString(filterText),
			))
		}
	}
	state.configureListTitle()
	state.SetSize(state.width, state.height)

	if state.Mode == agentHubViewerMode {
		if _, ok := state.groupByID(viewerID); !ok {
			state.Mode = agentHubListMode
			state.ViewerGroupID = ""
			state.Viewer.GotoTop()
		}
	}
	if selectedID != "" {
		if index := state.visibleGroupIndex(selectedID); index >= 0 {
			state.List.Select(index)
			return nil
		}
	}
	if selectedID == "" || state.groupIndex(selectedID) < 0 {
		state.selectDefaultGroup()
	}
	return nil
}

func (state *AgentHubState) SetSize(terminalWidth, terminalHeight int) {
	if state == nil {
		return
	}
	state.width = max(1, terminalWidth)
	state.height = max(1, terminalHeight)
	compact := compactAgentHub(state.width, state.height)
	if compact != state.compact {
		state.compact = compact
		delegate := newPickerDelegate(state.isDark, compact)
		state.List.SetDelegate(delegate)
		state.ItemHeight = delegate.Height()
		state.ItemSpacing = delegate.Spacing()
	}
	state.configureListTitle()

	listWidth := pickerListWidth(state.width, agentHubMaximumWidth)
	desiredRows := max(5, len(state.List.Items())*(state.ItemHeight+max(0, state.ItemSpacing))+2)
	listHeight := pickerListHeight(state.height, desiredRows, 4)
	state.List.SetSize(listWidth, listHeight)
	state.List.SetShowPagination(!state.compact && desiredRows > listHeight)

	state.Viewer.SetWidth(listWidth)
	state.Viewer.SetHeight(max(1, state.height-6))
	state.rebuildViewerContent(true)
}

func (state *AgentHubState) SetTheme(isDark bool, reducedMotion bool) {
	if state == nil {
		return
	}
	state.isDark = isDark
	state.reducedMotion = reducedMotion
	delegate := newPickerDelegate(isDark, state.compact)
	state.List.SetDelegate(delegate)
	state.ItemHeight = delegate.Height()
	state.ItemSpacing = delegate.Spacing()
	configurePickerList(&state.List, isDark, reducedMotion)
	state.configureListTitle()
	state.rebuildViewerContent(true)
}

func (state *AgentHubState) selectedGroupID() BlockID {
	if state == nil {
		return ""
	}
	if state.Mode == agentHubViewerMode && state.ViewerGroupID != "" {
		return state.ViewerGroupID
	}
	item, ok := state.List.SelectedItem().(agentHubItem)
	if !ok {
		return ""
	}
	return item.group.ID
}

func (state *AgentHubState) selectedGroup() (AgentGroupProjection, bool) {
	if state == nil {
		return AgentGroupProjection{}, false
	}
	if state.Mode == agentHubViewerMode {
		return state.groupByID(state.ViewerGroupID)
	}
	item, ok := state.List.SelectedItem().(agentHubItem)
	if !ok {
		return AgentGroupProjection{}, false
	}
	return item.group, true
}

func (state *AgentHubState) groupByID(id BlockID) (AgentGroupProjection, bool) {
	if state == nil || id == "" {
		return AgentGroupProjection{}, false
	}
	for _, group := range state.Surface.Groups {
		if group.ID == id {
			return group, true
		}
	}
	return AgentGroupProjection{}, false
}

func (state *AgentHubState) groupIndex(id BlockID) int {
	if state == nil || id == "" {
		return -1
	}
	for index, group := range state.Surface.Groups {
		if group.ID == id {
			return index
		}
	}
	return -1
}

func (state *AgentHubState) visibleGroupIndex(id BlockID) int {
	if state == nil || id == "" {
		return -1
	}
	for index, raw := range state.List.VisibleItems() {
		item, ok := raw.(agentHubItem)
		if ok && item.group.ID == id {
			return index
		}
	}
	return -1
}

func (state *AgentHubState) selectDefaultGroup() {
	if state == nil {
		return
	}
	visible := state.List.VisibleItems()
	if len(visible) == 0 {
		return
	}
	selected := len(visible) - 1
	for index := len(visible) - 1; index >= 0; index-- {
		item, ok := visible[index].(agentHubItem)
		if ok && item.group.Lifecycle == BlockLive {
			selected = index
			break
		}
	}
	state.List.Select(selected)
}

func (state *AgentHubState) openSelectedViewer() bool {
	group, ok := state.selectedGroup()
	if !ok {
		return false
	}
	state.Mode = agentHubViewerMode
	state.ViewerGroupID = group.ID
	state.Viewer.GotoTop()
	state.rebuildViewerContent(false)
	return true
}

// Back consumes Escape when it first needs to clear a filter or return from
// the Viewer. False means the parent should close the modal.
func (state *AgentHubState) Back() bool {
	if state == nil {
		return false
	}
	if state.Mode == agentHubViewerMode {
		state.Mode = agentHubListMode
		state.ViewerGroupID = ""
		state.Viewer.GotoTop()
		return true
	}
	if state.List.FilterState() != list.Unfiltered {
		state.List.ResetFilter()
		return true
	}
	return false
}

func (state *AgentHubState) UpdateKey(msg tea.KeyPressMsg, keys KeyMap) tea.Cmd {
	if state == nil {
		return nil
	}
	if state.Mode == agentHubViewerMode {
		navigateReadOnlyViewport(&state.Viewer, msg.String())
		return nil
	}
	if state.List.FilterState() == list.Filtering {
		state.List, _ = state.List.Update(msg)
		state.applyCurrentFilterSynchronously()
		return nil
	}
	if key.Matches(msg, keys.CompleteSelect) {
		state.openSelectedViewer()
		return nil
	}
	var cmd tea.Cmd
	state.List, cmd = state.List.Update(msg)
	if state.List.FilterState() != list.Unfiltered {
		state.applyCurrentFilterSynchronously()
		return nil
	}
	return cmd
}

func (state *AgentHubState) Update(msg tea.Msg) tea.Cmd {
	if state == nil {
		return nil
	}
	if _, staleFilterResult := msg.(list.FilterMatchesMsg); staleFilterResult {
		// Agent Hub filtering is bounded to maxAgentSurfaceGroups and applied
		// synchronously. Ignore generation-less Bubbles filter receipts so a
		// delayed result cannot replace a newer live projection.
		return nil
	}
	if state.Mode == agentHubViewerMode {
		var cmd tea.Cmd
		state.Viewer, cmd = state.Viewer.Update(msg)
		return cmd
	}
	filterState := state.List.FilterState()
	filterText := state.List.FilterInput.Value()
	var cmd tea.Cmd
	state.List, cmd = state.List.Update(msg)
	if state.List.FilterState() != list.Unfiltered &&
		(filterState != state.List.FilterState() || filterText != state.List.FilterInput.Value()) {
		// Paste and other non-key input can mutate FilterInput while returning
		// Bubbles' generation-less asynchronous matcher. Apply the bounded
		// filter now so a later receipt can never install stale matches.
		state.applyCurrentFilterSynchronously()
		return nil
	}
	return cmd
}

func (state *AgentHubState) applyCurrentFilterSynchronously() {
	if state == nil {
		return
	}
	cursor := state.List.FilterInput.Position()
	state.applyFilterSynchronously(state.List.FilterState(), state.List.FilterInput.Value())
	if state.List.FilterState() == list.Filtering {
		state.List.FilterInput.SetCursor(min(
			max(0, cursor),
			utf8.RuneCountInString(state.List.FilterInput.Value()),
		))
	}
}

func (state *AgentHubState) applyFilterSynchronously(filterState list.FilterState, value string) {
	if state == nil || filterState == list.Unfiltered {
		return
	}
	state.List.SetFilterText(value)
	if filterState == list.Filtering {
		state.List.SetFilterState(list.Filtering)
	}
}

func (state *AgentHubState) rebuildViewerContent(preserveOffset bool) {
	if state == nil || state.Mode != agentHubViewerMode {
		return
	}
	offset := 0
	var anchor agentViewerRowAnchor
	hasAnchor := false
	if preserveOffset {
		offset = state.Viewer.YOffset()
		if offset >= 0 && offset < len(state.viewerRows) {
			anchor = state.viewerRows[offset]
			hasAnchor = true
		}
	}
	group, ok := state.groupByID(state.ViewerGroupID)
	if !ok {
		state.Viewer.SetContent("")
		state.Viewer.GotoTop()
		state.viewerContentKey = ""
		state.viewerRows = nil
		return
	}
	key := fmt.Sprintf("%s:%d:%d:%t:%t", group.ID, group.Revision, state.Viewer.Width(), state.isDark, noColor)
	if key == state.viewerContentKey && len(state.viewerRows) > 0 {
		state.Viewer.SetYOffset(offset)
		return
	}
	layout := renderAgentViewerLayout(group, state.Viewer.Width(), state.isDark)
	state.Viewer.SetContent(layout.content)
	state.viewerContentKey = key
	state.viewerRows = layout.rows
	if hasAnchor {
		offset = resolveAgentViewerRowOffset(anchor, layout.rows, offset)
	}
	state.Viewer.SetYOffset(offset)
}

func renderAgentViewerBody(group AgentGroupProjection, width int, isDark bool) string {
	return renderAgentViewerLayout(group, width, isDark).content
}

func renderAgentViewerLayout(group AgentGroupProjection, width int, isDark bool) agentViewerLayout {
	width = max(1, width)
	styles := NewStyles(isDark)
	nodeStyles := NewToolCardStyles(isDark)
	lines := make([]string, 0, 10+len(group.Nodes)*2)
	rows := make([]agentViewerRowAnchor, 0, cap(lines))
	appendRow := func(line string, row agentViewerRowAnchor) {
		lines = append(lines, line)
		rows = append(rows, row)
	}
	appendRow(
		styles.OverlayAccent.Render(truncateDisplay("Status · "+agentGroupStatusLabel(group), width)),
		agentViewerRowAnchor{key: "status"},
	)
	appendRow(
		styles.OverlayDim.Render(truncateDisplay(agentGroupSummary(group), width)),
		agentViewerRowAnchor{key: "summary"},
	)
	if group.Interrupted {
		appendRow(styles.StatusWarning.Render(truncateDisplay(
			"Restored after interruption; child outcomes are unknown.",
			width,
		)), agentViewerRowAnchor{key: "interrupted"})
	}
	appendRow("", agentViewerRowAnchor{key: "before-subagents"})
	appendRow(
		styles.OverlayAccent.Render(truncateDisplay("Subagents", width)),
		agentViewerRowAnchor{key: "subagents"},
	)

	if !group.ProgressAvailable {
		appendRow(styles.OverlayDim.Render(truncateDisplay(
			"No public subagent progress is available.",
			width,
		)), agentViewerRowAnchor{key: "no-progress"})
	} else {
		for _, node := range group.Nodes {
			glyph, status, style := agentNodePresentation(node, nodeStyles)
			label := node.Label
			if node.Status == WorkNodeQueued {
				label = fmt.Sprintf("Agent %d", node.Index+1)
			}
			role := fmt.Sprintf("%s %s · %s", glyph, label, status)
			if node.EvalTokens > 0 {
				role += fmt.Sprintf(" · %d tok", node.EvalTokens)
			}
			meta := ""
			if node.Status != WorkNodeQueued {
				meta = node.Model + " · " + string(node.Location)
			}
			if width >= 54 && meta != "" {
				appendRow(
					style.Render(truncateDisplay(role+" · "+meta, width)),
					agentViewerRowAnchor{nodeID: node.ID},
				)
				continue
			}
			appendRow(
				style.Render(truncateDisplay(role, width)),
				agentViewerRowAnchor{nodeID: node.ID},
			)
			if meta != "" && width >= 12 {
				appendRow(
					styles.OverlayDim.Render(truncateDisplay("  "+meta, width)),
					agentViewerRowAnchor{nodeID: node.ID, subrow: 1},
				)
			}
		}
	}

	appendRow("", agentViewerRowAnchor{key: "before-activity"})
	appendRow(
		styles.OverlayAccent.Render(truncateDisplay("Activity", width)),
		agentViewerRowAnchor{key: "activity"},
	)
	appendRow(
		styles.OverlayDim.Render(truncateDisplay(
			"No public child events are available for this runtime.",
			width,
		)),
		agentViewerRowAnchor{key: "no-events"},
	)
	return agentViewerLayout{content: strings.Join(lines, "\n"), rows: rows}
}

func resolveAgentViewerRowOffset(
	anchor agentViewerRowAnchor,
	rows []agentViewerRowAnchor,
	fallback int,
) int {
	if anchor.nodeID != "" {
		firstNodeRow := -1
		for index, candidate := range rows {
			if candidate.nodeID != anchor.nodeID {
				continue
			}
			if firstNodeRow < 0 {
				firstNodeRow = index
			}
			if candidate.subrow == anchor.subrow {
				return index
			}
		}
		if firstNodeRow >= 0 {
			return firstNodeRow
		}
	} else if anchor.key != "" {
		for index, candidate := range rows {
			if candidate.key == anchor.key {
				return index
			}
		}
	}
	return min(max(0, fallback), max(0, len(rows)-1))
}

func agentNodePresentation(node WorkNode, styles ToolCardStyles) (string, string, lipgloss.Style) {
	switch node.Status {
	case WorkNodeQueued:
		return "○", "queued", styles.Dimmed
	case WorkNodeRunning:
		return "…", "running", styles.TitleRunning
	case WorkNodeWaiting:
		return "○", "waiting", styles.TitleAttention
	case WorkNodeCompleted:
		return "✓", "completed", styles.TitleSuccess
	case WorkNodeAttention:
		return "!", expertFailureLabel(node.FailureCode), styles.TitleAttention
	case WorkNodeFailed:
		return "✗", expertFailureLabel(node.FailureCode), styles.TitleError
	case WorkNodeCancelled:
		return "–", "cancelled", styles.Dimmed
	default:
		return "?", "unknown", styles.Dimmed
	}
}

func agentGroupStatusLabel(group AgentGroupProjection) string {
	if group.Interrupted {
		return "interrupted"
	}
	switch group.Lifecycle {
	case BlockLive:
		if !group.ProgressAvailable {
			return "awaiting plan"
		}
		return "active"
	case BlockSettled:
		return "settled"
	case BlockFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func agentGroupSummary(group AgentGroupProjection) string {
	if !group.ProgressAvailable {
		return "No public subagent progress yet"
	}
	parts := []string{
		string(group.Strategy),
		fmt.Sprintf("%d %s", group.Total, pluralizeNoun(group.Total, "agent", "agents")),
	}
	for _, count := range []struct {
		value int
		label string
	}{
		{group.Running, "active"},
		{group.Waiting, "waiting"},
		{group.Queued, "queued"},
		{group.Completed, "completed"},
		{group.Attention, "attention"},
		{group.Failed, "failed"},
		{group.Cancelled, "cancelled"},
	} {
		if count.value > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count.value, count.label))
		}
	}
	return strings.Join(parts, " · ")
}

func (state *AgentHubState) hubContent(styles Styles) string {
	if state == nil {
		return ""
	}
	if state.Unavailable {
		width := state.List.Width()
		content := styles.OverlayTitle.Render("Agents") + "\n\n" +
			renderAgentHubWrapped(styles.ErrorText, "Agent activity is unavailable.", width) + "\n" +
			renderAgentHubWrapped(styles.OverlayDim, "The safe runtime projection was rejected.", width)
		return lipgloss.NewStyle().Width(width).Height(state.List.Height()).Render(content)
	}
	if len(state.Surface.Groups) == 0 {
		width := state.List.Width()
		content := styles.OverlayTitle.Render("Agents") + "\n\n" +
			renderAgentHubWrapped(styles.OverlayDim, "No agent consultations yet.", width) + "\n" +
			renderAgentHubWrapped(styles.OverlayDim, "Agents created during a run will appear here.", width)
		return lipgloss.NewStyle().Width(width).Height(state.List.Height()).Render(content)
	}
	return state.List.View()
}

func renderAgentHubWrapped(style lipgloss.Style, value string, width int) string {
	lines := strings.Split(wrapText(value, max(1, width)), "\n")
	for index := range lines {
		lines[index] = style.Render(lines[index])
	}
	return strings.Join(lines, "\n")
}

func (state *AgentHubState) viewerContent(styles Styles) string {
	if state == nil {
		return ""
	}
	group, ok := state.groupByID(state.ViewerGroupID)
	if !ok {
		return styles.ErrorText.Render("Agent activity is unavailable.")
	}
	width := state.Viewer.Width()
	title := styles.OverlayTitle.Render(truncateDisplay("Agent Viewer", width))
	subtitle := styles.OverlayDim.Render(truncateDisplay(
		fmt.Sprintf("Consultation · %s", agentGroupStatusLabel(group)),
		width,
	))
	return title + "\n" + subtitle + "\n" + state.Viewer.View()
}

func (m *Model) openAgentHub() {
	if m == nil {
		return
	}
	anchor := m.captureTranscriptReflowAnchor()
	surface, err := m.agentSurfaceProjection()
	m.agentHubState = newAgentHubState(
		surface,
		err != nil,
		m.width,
		m.height,
		m.isDark,
		m.reducedMotion,
	)
	m.overlay = OverlayAgents
	m.input.Blur()
	m.recalcViewportHeight()
	m.restoreTranscriptReflowAnchor(anchor)
}

func (m *Model) closeAgentHub() {
	if m == nil {
		return
	}
	anchor := m.captureTranscriptReflowAnchor()
	m.agentHubState = nil
	m.closeOverlayToParent()
	if !m.composerEditable() {
		m.input.Blur()
	}
	m.recalcViewportHeight()
	m.restoreTranscriptReflowAnchor(anchor)
}

func (m *Model) agentSurfaceProjection() (AgentSurfaceProjection, error) {
	if err := m.reconcileTranscriptEntries(); err != nil {
		return AgentSurfaceProjection{}, err
	}
	return projectAgentSurface(m.entries, m.toolEntries)
}

// refreshedAgentSurfaceProjection is used only after transcript mutation paths
// have invalidated and repainted the semantic transcript. It reuses that
// renderer-owned admission result instead of reconciling the full transcript a
// second time for the visible Hub. Opening the Hub still uses the direct
// projection above and never depends on a paint cache.
func (m *Model) refreshedAgentSurfaceProjection() (AgentSurfaceProjection, error) {
	if _, err := m.reconcileTranscriptEntriesForRender(); err != nil {
		return AgentSurfaceProjection{}, err
	}
	return projectAgentSurface(m.entries, m.toolEntries)
}

func (m *Model) refreshAgentHub() tea.Cmd {
	if m == nil || m.overlay != OverlayAgents || m.agentHubState == nil {
		return nil
	}
	surface, err := m.refreshedAgentSurfaceProjection()
	return m.agentHubState.SetProjection(surface, err != nil)
}

func (m *Model) resizeAgentHub() {
	if m != nil && m.overlay == OverlayAgents && m.agentHubState != nil {
		m.agentHubState.SetSize(m.width, m.height)
	}
}

func (m *Model) restyleAgentHub() {
	if m != nil && m.overlay == OverlayAgents && m.agentHubState != nil {
		m.agentHubState.SetTheme(m.isDark, m.reducedMotion)
	}
}

func (m *Model) renderAgentHub() string {
	if m == nil || m.agentHubState == nil {
		return ""
	}
	width := pickerListWidth(m.width, agentHubMaximumWidth)
	var content string
	var hints []keyHint
	if m.agentHubState.Mode == agentHubViewerMode {
		content = m.agentHubState.viewerContent(m.styles)
		hints = []keyHint{
			{Key: "esc", Action: "back"},
			{Key: "enter", Action: "jump"},
			{Key: "j/k", Action: "scroll"},
			{Key: "pgup/pgdn", Action: "page"},
		}
	} else {
		content = m.agentHubState.hubContent(m.styles)
		switch {
		case m.agentHubState.Unavailable || len(m.agentHubState.Surface.Groups) == 0:
			hints = []keyHint{{Key: "esc", Action: "close"}}
		case m.agentHubState.List.FilterState() == list.Filtering:
			hints = []keyHint{{Key: "esc", Action: "cancel"}}
			if strings.TrimSpace(m.agentHubState.List.FilterInput.Value()) != "" {
				hints = append(hints, keyHint{Key: "enter", Action: "apply"})
			}
		case m.agentHubState.List.FilterState() == list.FilterApplied &&
			len(m.agentHubState.List.VisibleItems()) == 0:
			hints = []keyHint{{Key: "esc", Action: "clear"}}
		case m.agentHubState.List.FilterState() == list.FilterApplied:
			hints = []keyHint{
				{Key: "esc", Action: "clear"},
				{Key: "enter", Action: "view"},
				{Key: "↑/↓", Action: "move"},
			}
		default:
			hints = []keyHint{
				{Key: "esc", Action: "close"},
				{Key: "enter", Action: "view"},
				{Key: "↑/↓", Action: "move"},
				{Key: "/", Action: "filter"},
			}
		}
	}
	return m.renderPickerFrame(content, agentHubMaximumWidth, m.renderAgentHubHints(width, hints))
}

func (m *Model) renderAgentHubHints(width int, hints []keyHint) string {
	for keep := len(hints); keep > 0; keep-- {
		rendered := m.renderKeyHintSet(hints[:keep], -1)
		if lipgloss.Width(rendered) <= width {
			return rendered
		}
	}
	return m.renderKeyHints(width, hints...)
}

func (m *Model) agentHubBack() bool {
	return m != nil && m.agentHubState != nil && m.agentHubState.Back()
}

func (m *Model) handleAgentHubKey(msg tea.KeyPressMsg) tea.Cmd {
	if m == nil {
		return nil
	}
	if m.agentHubState == nil {
		m.closeAgentHub()
		return nil
	}
	if key.Matches(msg, m.keys.AgentHub) {
		m.closeAgentHub()
		return tea.ClearScreen
	}
	if m.agentHubState.Mode == agentHubViewerMode && key.Matches(msg, m.keys.CompleteSelect) {
		if group, ok := m.agentHubState.selectedGroup(); ok && m.jumpToAgentGroup(group) {
			m.closeAgentHub()
			return tea.ClearScreen
		}
		return nil
	}
	return m.agentHubState.UpdateKey(msg, m.keys)
}

func (m *Model) updateAgentHubMessage(msg tea.Msg) tea.Cmd {
	if m == nil || m.agentHubState == nil {
		return nil
	}
	switch msg.(type) {
	case tea.WindowSizeMsg, tea.BackgroundColorMsg,
		ToolCallStartMsg, ExpertProgressMsg, ToolCallResultMsg:
		// The smart parent already resized, restyled, or reprojected the Hub
		// while handling these messages. Delivering them to the child again
		// would duplicate list/filter work without adding state.
		return nil
	}
	return m.agentHubState.Update(msg)
}

func (m *Model) jumpToAgentGroup(group AgentGroupProjection) bool {
	if m == nil || !group.ID.Valid() || len(m.transcriptLayout.Records) == 0 {
		return false
	}
	viewportHeight := max(1, m.viewport.Height())
	screenRow := min(2, max(0, viewportHeight/3))
	resolution, err := ResolveTranscriptAnchor(
		ManualTranscriptAnchor(SemanticAnchor{
			SessionID: m.transcriptLayout.SessionID,
			BlockID:   group.ID,
			TurnID:    group.TurnID,
			ScreenRow: screenRow,
			Bias:      AnchorBiasNext,
		}),
		m.transcriptLayout,
		m.transcriptLayout,
		viewportHeight,
	)
	if err != nil || resolution.BlockID != group.ID {
		return false
	}
	m.viewport.SetYOffset(resolution.ViewportTop)
	m.pauseFollow()
	return true
}

type agentHubPointerProjection struct {
	lines    []string
	startY   int
	baseRows int
	width    int
}

func (m *Model) projectAgentHubPointer() (agentHubPointerProjection, bool) {
	if m == nil || m.agentHubState == nil || m.overlay != OverlayAgents || m.width <= 0 {
		return agentHubPointerProjection{}, false
	}
	overlay := m.renderAgentHub()
	if overlay == "" {
		return agentHubPointerProjection{}, false
	}
	frame := m.projectFrame()
	base := m.viewport.View() + "\n" + frame.Footer.Content
	return agentHubPointerProjection{
		lines:    strings.Split(overlay, "\n"),
		startY:   centeredOverlayStartY(base, overlay),
		baseRows: len(strings.Split(base, "\n")),
		width:    m.width,
	}, true
}

func (projection agentHubPointerProjection) rowRect(localY int) CellRect {
	if localY < 0 || localY >= len(projection.lines) || projection.startY+localY >= projection.baseRows {
		return CellRect{}
	}
	lineWidth := lipgloss.Width(projection.lines[localY])
	if lineWidth <= 0 {
		return CellRect{}
	}
	startX := centeredOverlayLineX(projection.width, projection.lines[localY])
	return NewCellRect(
		startX,
		projection.startY+localY,
		startX+lineWidth,
		projection.startY+localY+1,
	)
}

func (projection agentHubPointerProjection) contains(x, y int) bool {
	return projection.rowRect(y-projection.startY).Contains(x, y)
}

func agentHubTitleRows(state *AgentHubState) int {
	if state == nil {
		return 0
	}
	title := state.List.Styles.Title.Render(state.List.Title)
	if state.List.FilterState() == list.Filtering {
		title = state.List.FilterInput.View()
	}
	return lipgloss.Height(state.List.Styles.TitleBar.Render(title))
}

func (m *Model) agentHubItemAt(x, y int) (int, bool) {
	state := m.agentHubState
	projection, ok := m.projectAgentHubPointer()
	if !ok || state.Mode != agentHubListMode || state.ItemHeight <= 0 {
		return 0, false
	}
	localY := y - projection.startY
	itemStartY := 1 + agentHubTitleRows(state)
	itemY := localY - itemStartY
	stride := state.ItemHeight + max(0, state.ItemSpacing)
	if itemY < 0 || stride <= 0 || itemY%stride >= state.ItemHeight {
		return 0, false
	}
	itemRow := Inset(projection.rowRect(localY), Insets{
		Left:  pickerFrameCursorX,
		Right: pickerFrameCursorX,
	})
	if !itemRow.Contains(x, y) {
		return 0, false
	}
	rowOnPage := itemY / stride
	if rowOnPage < 0 || rowOnPage >= state.List.Paginator.PerPage {
		return 0, false
	}
	index := state.List.Paginator.Page*state.List.Paginator.PerPage + rowOnPage
	if index < 0 || index >= len(state.List.VisibleItems()) {
		return 0, false
	}
	if _, ok := state.List.VisibleItems()[index].(agentHubItem); !ok {
		return 0, false
	}
	return index, true
}

func (m *Model) updateAgentHubWheel(msg tea.MouseWheelMsg) tea.Cmd {
	state := m.agentHubState
	projection, ok := m.projectAgentHubPointer()
	if !ok || !projection.contains(msg.X, msg.Y) {
		return nil
	}
	if state.Mode == agentHubViewerMode {
		var cmd tea.Cmd
		state.Viewer, cmd = state.Viewer.Update(msg)
		return cmd
	}
	if len(state.List.VisibleItems()) == 0 {
		return nil
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		state.List.CursorUp()
	case tea.MouseWheelDown:
		state.List.CursorDown()
	}
	return nil
}

func (m *Model) selectAgentHubPointer(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.agentHubState == nil {
		return nil
	}
	index, ok := m.agentHubItemAt(msg.X, msg.Y)
	if ok {
		m.agentHubState.List.Select(index)
	}
	return nil
}

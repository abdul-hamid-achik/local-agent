package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/expertselector"
	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
)

func installAgentHubFixture(t *testing.T, m *Model) (BlockID, BlockID) {
	t.Helper()
	liveEntry, liveTool := agentGroupFixture(
		0,
		BlockLive,
		ToolStatusRunning,
		liveAgentProgress("generalist"),
	)
	settledEntry, settledTool := agentGroupFixture(
		1,
		BlockSettled,
		ToolStatusDone,
		settledAgentProgress("verifier"),
	)
	settledTool.Args = "private objective sentinel"
	settledTool.Result = "private report sentinel"
	m.entries = []ChatEntry{liveEntry, settledEntry}
	m.toolEntries = []ToolEntry{liveTool, settledTool}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	return liveEntry.BlockID, settledEntry.BlockID
}

func TestAgentHubShortcutCommandAndDraftOwnership(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(ctrlKey('g'))
	m = updated.(*Model)
	if m.overlay != OverlayAgents || m.agentHubState == nil || m.input.Focused() {
		t.Fatalf("Ctrl+G did not open the Hub with owned focus: overlay=%v state=%v focused=%v",
			m.overlay, m.agentHubState != nil, m.input.Focused())
	}

	updated, _ = m.Update(ctrlKey('g'))
	m = updated.(*Model)
	if m.overlay != OverlayNone || m.agentHubState != nil || !m.input.Focused() {
		t.Fatalf("second Ctrl+G did not close cleanly: overlay=%v state=%v focused=%v",
			m.overlay, m.agentHubState != nil, m.input.Focused())
	}

	m.state = StateStreaming
	updated, _ = m.Update(ctrlKey('g'))
	m = updated.(*Model)
	if m.overlay != OverlayAgents {
		t.Fatal("Ctrl+G did not keep agent activity inspectable during streaming")
	}
	m.closeAgentHub()

	for _, draft := range []string{"unsent draft", "   "} {
		m.input.SetValue(draft)
		updated, _ = m.Update(ctrlKey('g'))
		m = updated.(*Model)
		if m.overlay != OverlayNone || m.agentHubState != nil || m.input.Value() != draft {
			t.Fatalf("Ctrl+G hid or changed non-empty draft %q", draft)
		}
	}

	m.input.Reset()
	m.state = StateIdle
	m.handleCommandAction(command.Result{Action: command.ActionShowAgents})
	if m.overlay != OverlayAgents || m.agentHubState == nil {
		t.Fatal("ActionShowAgents did not open the Hub")
	}
}

func TestAgentHubEmptyAndResponsiveSurfacesFit(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{
		{30, 12},
		{40, 16},
		{72, 24},
		{112, 40},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = updated.(*Model)
			m.openAgentHub()
			view := m.renderAgentHub()
			plain := ansi.Strip(view)
			normalized := strings.Join(strings.Fields(strings.ReplaceAll(plain, "│", "")), " ")
			for _, want := range []string{
				"Agents",
				"No agent consultations yet.",
				"Agents created during a run will appear here.",
			} {
				if !strings.Contains(normalized, want) {
					t.Fatalf("%dx%d empty Hub missing %q:\n%s", size.width, size.height, want, plain)
				}
			}
			assertRenderedLinesFit(t, view, size.width)
			assertRenderedHeightFits(t, view, size.height)
		})
	}
}

func TestAgentHubEscapeFilterViewerAndPrivacy(t *testing.T) {
	m := newTestModel(t)
	liveID, _ := installAgentHubFixture(t, m)
	m.openAgentHub()
	if selected := m.agentHubState.selectedGroupID(); selected != liveID {
		t.Fatalf("default selection = %q, want live group %q", selected, liveID)
	}

	updated, _ := m.Update(charKey('/'))
	m = updated.(*Model)
	if m.agentHubState.List.FilterState() == list.Unfiltered {
		t.Fatal("slash did not give the Bubbles filter ownership")
	}
	updated, _ = m.Update(charKey('g'))
	m = updated.(*Model)
	updated, _ = m.Update(escKey())
	m = updated.(*Model)
	if m.overlay != OverlayAgents || m.agentHubState.List.FilterState() != list.Unfiltered {
		t.Fatal("first Escape did not clear the active filter while keeping the Hub open")
	}

	updated, _ = m.Update(enterKey())
	m = updated.(*Model)
	if m.agentHubState.Mode != agentHubViewerMode || m.agentHubState.ViewerGroupID != liveID {
		t.Fatalf("Enter did not open the selected Viewer: %#v", m.agentHubState)
	}
	viewer := ansi.Strip(m.renderAgentHub())
	for _, want := range []string{
		"Agent Viewer",
		"generalist",
		"No public child events are available for this runtime.",
	} {
		if !strings.Contains(viewer, want) {
			t.Fatalf("Viewer missing %q:\n%s", want, viewer)
		}
	}
	for _, forbidden := range []string{"private objective sentinel", "private report sentinel"} {
		if strings.Contains(viewer, forbidden) {
			t.Fatalf("Viewer exposed %q:\n%s", forbidden, viewer)
		}
	}

	updated, _ = m.Update(escKey())
	m = updated.(*Model)
	if m.overlay != OverlayAgents || m.agentHubState.Mode != agentHubListMode {
		t.Fatal("second Escape did not return Viewer to Hub")
	}
	updated, _ = m.Update(escKey())
	m = updated.(*Model)
	if m.overlay != OverlayNone || m.agentHubState != nil {
		t.Fatal("third Escape did not close the Hub")
	}
}

func TestAgentHubViewerTracksStableGroupAcrossLifecycleRefresh(t *testing.T) {
	entry, tool := agentGroupFixture(0, BlockLive, ToolStatusRunning, liveAgentProgress("critic"))
	live, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
	if err != nil {
		t.Fatal(err)
	}
	state := newAgentHubState(live, false, 72, 24, true, false)
	if !state.openSelectedViewer() {
		t.Fatal("could not open live group")
	}
	groupID := state.ViewerGroupID
	nodeIDs := []string{live.Groups[0].Nodes[0].ID, live.Groups[0].Nodes[1].ID}
	state.Viewer.ScrollDown(2)

	entry.Lifecycle = BlockSettled
	entry.Revision++
	tool.Status = ToolStatusDone
	tool.ExpertProgress = &ExpertProgressState{
		Sequence: 5, Strategy: expertselector.StrategySwarm, Total: 2, Parallelism: 1,
		Completed: 1, Failed: 1,
		Experts: []ExpertProgressItem{
			{
				Index: 0, Expert: "critic", Model: "safe-model",
				Location: llm.OllamaModelLocationCloud,
				Phase:    expertteam.ProgressCompleted, Status: expertteam.ExpertCompleted,
				EvalTokens: 23,
			},
			{
				Index: 1, Expert: "verifier", Model: "safe-model",
				Location: llm.OllamaModelLocationRemote,
				Phase:    expertteam.ProgressFailed, Status: expertteam.ExpertFailed,
				FailureCode: "timed_out",
			},
		},
	}
	settled, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
	if err != nil {
		t.Fatal(err)
	}
	_ = state.SetProjection(settled, false)
	if state.Mode != agentHubViewerMode || state.ViewerGroupID != groupID {
		t.Fatalf("refresh lost Viewer identity: mode=%v group=%q", state.Mode, state.ViewerGroupID)
	}
	for index, want := range nodeIDs {
		if got := settled.Groups[0].Nodes[index].ID; got != want {
			t.Fatalf("node %d identity changed: %q -> %q", index, want, got)
		}
	}
	body := ansi.Strip(state.Viewer.View())
	for _, want := range []string{"settled", "critic · completed", "verifier · timed out"} {
		if !strings.Contains(body, want) {
			t.Fatalf("settled Viewer missing %q:\n%s", want, body)
		}
	}
}

func TestAgentHubFilterStaysCurrentAcrossViewerRefresh(t *testing.T) {
	liveEntry, liveTool := agentGroupFixture(0, BlockLive, ToolStatusRunning, liveAgentProgress("generalist"))
	settledEntry, settledTool := agentGroupFixture(1, BlockSettled, ToolStatusDone, settledAgentProgress("verifier"))
	surface, err := projectAgentSurface(
		[]ChatEntry{liveEntry, settledEntry},
		[]ToolEntry{liveTool, settledTool},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := newAgentHubState(surface, false, 72, 24, true, false)
	keys := DefaultKeyMap()
	state.UpdateKey(charKey('/'), keys)
	for _, character := range "verifier" {
		state.UpdateKey(charKey(character), keys)
	}
	state.UpdateKey(enterKey(), keys) // apply filter
	if state.List.FilterState() != list.FilterApplied || len(state.List.VisibleItems()) != 1 {
		t.Fatalf("synchronous filter = state %v, %d items", state.List.FilterState(), len(state.List.VisibleItems()))
	}
	state.UpdateKey(enterKey(), keys) // inspect the one match
	if state.Mode != agentHubViewerMode || state.ViewerGroupID != settledEntry.BlockID {
		t.Fatalf("filtered Viewer = mode %v, group %q", state.Mode, state.ViewerGroupID)
	}

	settledTool.ExpertProgress.Experts[0].EvalTokens = 31
	settledEntry.Revision++
	refreshed, err := projectAgentSurface(
		[]ChatEntry{liveEntry, settledEntry},
		[]ToolEntry{liveTool, settledTool},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cmd := state.SetProjection(refreshed, false); cmd != nil {
		t.Fatal("bounded synchronous filter refresh returned an asynchronous command")
	}
	// Bubbles filter receipts have no generation. A delayed old receipt must
	// not overwrite the synchronously filtered current projection.
	state.Update(list.FilterMatchesMsg{})
	if !state.Back() || state.Mode != agentHubListMode ||
		state.List.FilterState() != list.FilterApplied ||
		len(state.List.VisibleItems()) != 1 ||
		state.selectedGroupID() != settledEntry.BlockID {
		t.Fatalf("Viewer refresh lost filtered selection: mode=%v filter=%v visible=%d selected=%q",
			state.Mode, state.List.FilterState(), len(state.List.VisibleItems()), state.selectedGroupID())
	}
}

func TestAgentHubPasteFiltersSynchronouslyWithoutStaleReceipt(t *testing.T) {
	liveEntry, liveTool := agentGroupFixture(0, BlockLive, ToolStatusRunning, liveAgentProgress("generalist"))
	settledEntry, settledTool := agentGroupFixture(1, BlockSettled, ToolStatusDone, settledAgentProgress("verifier"))
	surface, err := projectAgentSurface(
		[]ChatEntry{liveEntry, settledEntry},
		[]ToolEntry{liveTool, settledTool},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := newAgentHubState(surface, false, 72, 24, true, false)
	state.UpdateKey(charKey('/'), DefaultKeyMap())
	if cmd := state.Update(tea.PasteMsg{Content: "verifier"}); cmd != nil {
		t.Fatal("bounded paste filter returned an asynchronous command")
	}
	if got := state.List.FilterInput.Value(); got != "verifier" {
		t.Fatalf("filter paste = %q, want verifier", got)
	}
	if visible := state.List.VisibleItems(); len(visible) != 1 {
		t.Fatalf("paste left %d visible items, want one", len(visible))
	}
	if selected := state.selectedGroupID(); selected != settledEntry.BlockID {
		t.Fatalf("paste selected %q, want %q", selected, settledEntry.BlockID)
	}

	state.Update(list.FilterMatchesMsg{})
	if len(state.List.VisibleItems()) != 1 || state.selectedGroupID() != settledEntry.BlockID {
		t.Fatal("stale asynchronous receipt replaced pasted filter matches")
	}
}

func TestAgentHubLiveRefreshPreservesFilterCursorAndHonestEmptyHints(t *testing.T) {
	liveEntry, liveTool := agentGroupFixture(0, BlockLive, ToolStatusRunning, liveAgentProgress("generalist"))
	surface, err := projectAgentSurface([]ChatEntry{liveEntry}, []ToolEntry{liveTool})
	if err != nil {
		t.Fatal(err)
	}
	state := newAgentHubState(surface, false, 40, 16, true, false)
	keys := DefaultKeyMap()
	state.UpdateKey(charKey('/'), keys)
	if cmd := state.Update(tea.PasteMsg{Content: "generalist"}); cmd != nil {
		t.Fatal("paste filter returned async work")
	}
	state.List.FilterInput.SetCursor(3)

	liveEntry.Revision++
	refreshed, err := projectAgentSurface([]ChatEntry{liveEntry}, []ToolEntry{liveTool})
	if err != nil {
		t.Fatal(err)
	}
	_ = state.SetProjection(refreshed, false)
	if got := state.List.FilterInput.Position(); got != 3 {
		t.Fatalf("live refresh moved filter cursor to %d, want 3", got)
	}
	state.UpdateKey(charKey('X'), keys)
	if got := state.List.FilterInput.Value(); got != "genXeralist" {
		t.Fatalf("middle edit = %q, want genXeralist", got)
	}
	if got := state.List.FilterInput.Position(); got != 4 {
		t.Fatalf("middle edit cursor = %d, want 4", got)
	}
	state.UpdateKey(tea.KeyPressMsg{Code: tea.KeyBackspace}, keys)
	if got := state.List.FilterInput.Value(); got != "generalist" {
		t.Fatalf("restored filter = %q, want generalist", got)
	}

	state.UpdateKey(enterKey(), keys)
	if state.List.FilterState() != list.FilterApplied {
		t.Fatalf("filter state = %v, want applied", state.List.FilterState())
	}
	liveTool.ExpertProgress = liveAgentProgress("renamed")
	liveEntry.Revision++
	noMatch, err := projectAgentSurface([]ChatEntry{liveEntry}, []ToolEntry{liveTool})
	if err != nil {
		t.Fatal(err)
	}
	_ = state.SetProjection(noMatch, false)
	if state.List.FilterState() != list.FilterApplied || len(state.List.VisibleItems()) != 0 {
		t.Fatalf("live no-match refresh = state %v, visible %d",
			state.List.FilterState(), len(state.List.VisibleItems()))
	}
	m := newTestModel(t)
	m.width, m.height = 40, 16
	m.overlay = OverlayAgents
	m.agentHubState = state
	hints := ansi.Strip(m.renderAgentHub())
	if !strings.Contains(hints, "esc clear") || strings.Contains(hints, "enter view") {
		t.Fatalf("empty applied filter advertises unavailable actions:\n%s", hints)
	}
}

func TestAgentHubFailsClosedInsteadOfRetainingLastGoodProjection(t *testing.T) {
	entry, tool := agentGroupFixture(0, BlockSettled, ToolStatusDone, settledAgentProgress("critic"))
	surface, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
	if err != nil {
		t.Fatal(err)
	}
	state := newAgentHubState(surface, false, 72, 24, true, false)
	state.UpdateKey(charKey('/'), DefaultKeyMap())
	state.UpdateKey(charKey('c'), DefaultKeyMap())
	_ = state.SetProjection(AgentSurfaceProjection{OmittedGroups: -1}, false)
	if !state.Unavailable || len(state.Surface.Groups) != 0 || len(state.List.Items()) != 0 {
		t.Fatalf("invalid refresh retained prior data: %#v", state.Surface)
	}
	if state.List.FilterState() != list.Unfiltered || state.Back() {
		t.Fatal("unavailable projection retained an invisible filter or consumed close")
	}
	content := ansi.Strip(state.hubContent(NewStyles(true)))
	if !strings.Contains(content, "Agent activity is unavailable.") ||
		!strings.Contains(content, "safe runtime projection was rejected") ||
		strings.Contains(content, "critic") {
		t.Fatalf("fail-closed surface is misleading:\n%s", content)
	}
}

func TestAgentViewerPreservesSemanticTopNodeAcrossResizeAndProgress(t *testing.T) {
	entry, tool := agentGroupFixture(
		0,
		BlockLive,
		ToolStatusRunning,
		scrollingAgentProgress(16, true),
	)
	surface, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
	if err != nil {
		t.Fatal(err)
	}
	state := newAgentHubState(surface, false, 40, 12, true, false)
	if !state.openSelectedViewer() {
		t.Fatal("could not open Viewer")
	}
	nodeID := surface.Groups[0].Nodes[4].ID
	nodeRow := agentViewerNodeRow(state.viewerRows, nodeID)
	if nodeRow < 0 {
		t.Fatalf("missing semantic row for %q", nodeID)
	}
	state.Viewer.SetYOffset(nodeRow)
	assertAgentViewerTopNode(t, state, nodeID)

	state.SetSize(72, 12)
	assertAgentViewerTopNode(t, state, nodeID)
	state.SetSize(40, 12)
	assertAgentViewerTopNode(t, state, nodeID)

	tool.ExpertProgress = scrollingAgentProgress(16, false)
	tool.ExpertProgress.Sequence++
	entry.Revision++
	refreshed, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
	if err != nil {
		t.Fatal(err)
	}
	_ = state.SetProjection(refreshed, false)
	assertAgentViewerTopNode(t, state, nodeID)
}

func TestAgentHubResizeThemeAndActiveGeometryPreserveState(t *testing.T) {
	m := newTestModel(t)
	installAgentHubFixture(t, m)
	for index := 0; index < 100; index++ {
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "history row"})
	}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.viewport.SetYOffset(4)
	m.pauseFollow()
	m.state = StateStreaming
	beforeOffset := m.viewport.YOffset()

	m.openAgentHub()
	if got, want := m.viewport.Height(), m.projectFrame().Transcript.Rect.Height(); got != want {
		t.Fatalf("open geometry height = %d, want %d", got, want)
	}
	if m.viewport.YOffset() != beforeOffset || !m.followPaused() {
		t.Fatalf("open moved transcript anchor: offset=%d/%d paused=%v", m.viewport.YOffset(), beforeOffset, m.followPaused())
	}
	m.agentHubState.openSelectedViewer()
	groupID := m.agentHubState.ViewerGroupID

	for _, size := range []struct {
		width  int
		height int
	}{{30, 12}, {40, 16}, {72, 24}, {112, 40}} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m = updated.(*Model)
		if m.agentHubState.Mode != agentHubViewerMode || m.agentHubState.ViewerGroupID != groupID {
			t.Fatalf("%dx%d resize lost Viewer state", size.width, size.height)
		}
		view := m.renderAgentHub()
		assertRenderedLinesFit(t, view, size.width)
		assertRenderedHeightFits(t, view, size.height)
	}

	offset := m.agentHubState.Viewer.YOffset()
	m.agentHubState.SetTheme(false, true)
	if m.agentHubState.Mode != agentHubViewerMode ||
		m.agentHubState.ViewerGroupID != groupID ||
		m.agentHubState.Viewer.YOffset() != offset {
		t.Fatal("theme update changed Viewer identity or scroll")
	}

	m.closeAgentHub()
	if got, want := m.viewport.Height(), m.projectFrame().Transcript.Rect.Height(); got != want {
		t.Fatalf("close geometry height = %d, want %d", got, want)
	}
	if !m.followPaused() {
		t.Fatal("closing active Hub stole manual transcript ownership")
	}
}

func TestAgentHubMouseOwnsOnlyExactRowsAndNeverMovesTranscript(t *testing.T) {
	m := newTestModel(t)
	liveID, settledID := installAgentHubFixture(t, m)
	m.viewport.SetContent(strings.Repeat("transcript\n", 80))
	m.viewport.SetYOffset(5)
	m.pauseFollow()
	m.openAgentHub()

	projection, ok := m.projectAgentHubPointer()
	if !ok {
		t.Fatal("missing pointer projection")
	}
	localY := 1 + agentHubTitleRows(m.agentHubState) +
		m.agentHubState.ItemHeight + max(0, m.agentHubState.ItemSpacing)
	row := projection.rowRect(localY)
	x := row.MinX + pickerFrameCursorX
	y := projection.startY + localY
	if !row.Contains(x, y) {
		t.Fatalf("test coordinate (%d,%d) outside row %#v", x, y, row)
	}

	transcriptOffset := m.viewport.YOffset()
	updated, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
	m = updated.(*Model)
	if m.agentHubState.selectedGroupID() != liveID {
		t.Fatal("right click changed Hub selection")
	}
	updated, _ = m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = updated.(*Model)
	if m.agentHubState.selectedGroupID() != settledID {
		t.Fatalf("left click selected %q, want %q", m.agentHubState.selectedGroupID(), settledID)
	}
	updated, _ = m.Update(tea.MouseClickMsg{X: row.MinX, Y: y, Button: tea.MouseLeft})
	m = updated.(*Model)
	if m.agentHubState.selectedGroupID() != settledID {
		t.Fatal("border click changed Hub selection")
	}
	updated, _ = m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
	m = updated.(*Model)
	if m.viewport.YOffset() != transcriptOffset {
		t.Fatalf("Hub wheel moved hidden transcript from %d to %d", transcriptOffset, m.viewport.YOffset())
	}
}

func TestAgentHubHintsDescribeCurrentOwnershipAtMinimumWidth(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m = updated.(*Model)
	m.openAgentHub()
	empty := ansi.Strip(m.renderAgentHub())
	if !strings.Contains(empty, "esc close") || strings.Contains(empty, "enter view") {
		t.Fatalf("empty hints claim unavailable actions:\n%s", empty)
	}

	m.closeAgentHub()
	installAgentHubFixture(t, m)
	m.openAgentHub()
	hub := ansi.Strip(m.renderAgentHub())
	for _, want := range []string{"esc close", "enter view"} {
		if !strings.Contains(hub, want) {
			t.Fatalf("Hub hints missing %q:\n%s", want, hub)
		}
	}

	updated, _ = m.Update(charKey('/'))
	m = updated.(*Model)
	filtering := ansi.Strip(m.renderAgentHub())
	if !strings.Contains(filtering, "esc cancel") || strings.Contains(filtering, "enter apply") {
		t.Fatalf("empty filter hints claim Enter can apply:\n%s", filtering)
	}
	updated, _ = m.Update(tea.PasteMsg{Content: "generalist"})
	m = updated.(*Model)
	filtering = ansi.Strip(m.renderAgentHub())
	for _, want := range []string{"esc cancel", "enter apply"} {
		if !strings.Contains(filtering, want) {
			t.Fatalf("filter hints missing %q:\n%s", want, filtering)
		}
	}
	updated, _ = m.Update(enterKey())
	m = updated.(*Model)
	applied := ansi.Strip(m.renderAgentHub())
	for _, want := range []string{"esc clear", "enter view"} {
		if !strings.Contains(applied, want) {
			t.Fatalf("applied-filter hints missing %q:\n%s", want, applied)
		}
	}

	updated, _ = m.Update(enterKey())
	m = updated.(*Model)
	viewer := ansi.Strip(m.renderAgentHub())
	for _, want := range []string{"esc back", "enter jump"} {
		if !strings.Contains(viewer, want) {
			t.Fatalf("Viewer hints missing %q:\n%s", want, viewer)
		}
	}
}

func TestAgentHubIsPreemptedAndClearedByApproval(t *testing.T) {
	m := newTestModel(t)
	installAgentHubFixture(t, m)
	m.openAgentHub()
	responses := make(chan permission.ApprovalResponse, 1)

	updated, _ := m.Update(ToolApprovalMsg{ToolName: "bash", Response: responses})
	m = updated.(*Model)
	if m.pendingApproval == nil || m.approvalState == nil ||
		m.overlay != OverlayNone || m.agentHubState != nil {
		t.Fatalf("approval did not preempt and clear Hub: pending=%v approval=%v overlay=%v hub=%v",
			m.pendingApproval != nil, m.approvalState != nil, m.overlay, m.agentHubState != nil)
	}
}

func TestAgentHubIsPreemptedAndClearedByCortexDecision(t *testing.T) {
	m := newTestModel(t)
	installAgentHubFixture(t, m)
	m.openAgentHub()
	presentation, err := newCortexDecisionPresentation(
		"task_decision",
		*cortexDecisionFixture("hub-preemption"),
		m.width,
		m.height,
		m.isDark,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	m.cortexDecision = presentation
	m.activateCortexDecision()
	if m.overlay != OverlayCortexDecision || m.agentHubState != nil {
		t.Fatalf("Cortex did not preempt and clear Hub: overlay=%v hub=%v",
			m.overlay, m.agentHubState != nil)
	}
}

func TestAgentViewerBodyCellWidthIsBounded(t *testing.T) {
	entry, tool := agentGroupFixture(0, BlockSettled, ToolStatusDone, settledAgentProgress("模型🙂critic"))
	surface, err := projectAgentSurface([]ChatEntry{entry}, []ToolEntry{tool})
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{8, 20, 52, 80} {
		body := renderAgentViewerBody(surface.Groups[0], width, true)
		for _, line := range strings.Split(body, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d rendered %d cells: %q", width, got, line)
			}
		}
	}
}

func scrollingAgentProgress(total int, firstQueued bool) *ExpertProgressState {
	experts := make([]ExpertProgressItem, total)
	running := total
	queued := 0
	for index := range experts {
		if firstQueued && index == 0 {
			experts[index] = ExpertProgressItem{
				Index:    index,
				Location: llm.OllamaModelLocationUnknown,
			}
			running--
			queued++
			continue
		}
		experts[index] = ExpertProgressItem{
			Index:    index,
			Expert:   fmt.Sprintf("expert-%d", index),
			Model:    "safe-model",
			Location: llm.OllamaModelLocationLocal,
			Phase:    expertteam.ProgressStarted,
		}
	}
	return &ExpertProgressState{
		Sequence:    1,
		Strategy:    expertselector.StrategySwarm,
		Total:       total,
		Parallelism: total,
		Running:     running,
		Queued:      queued,
		Experts:     experts,
	}
}

func agentViewerNodeRow(rows []agentViewerRowAnchor, nodeID string) int {
	for index, row := range rows {
		if row.nodeID == nodeID {
			return index
		}
	}
	return -1
}

func assertAgentViewerTopNode(t *testing.T, state *AgentHubState, want string) {
	t.Helper()
	offset := state.Viewer.YOffset()
	if offset < 0 || offset >= len(state.viewerRows) {
		t.Fatalf("Viewer offset %d outside %d semantic rows", offset, len(state.viewerRows))
	}
	if got := state.viewerRows[offset].nodeID; got != want {
		t.Fatalf("Viewer top node = %q at row %d, want %q", got, offset, want)
	}
}

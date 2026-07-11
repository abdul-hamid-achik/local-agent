package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRuntimeStatusBoundsFailuresAndScrollsToFinalEntry(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m = updated.(*Model)
	for i := 1; i <= 8; i++ {
		m.failedServers = append(m.failedServers, FailedServer{
			Name:   fmt.Sprintf("server-%02d", i),
			Reason: "connection refused after a deliberately long local transport diagnostic",
		})
	}
	m.openSettingsPicker()
	m.openSettingsChild(m.openRuntimeStatus)

	top := m.renderRuntimeStatus()
	assertRenderedLinesFit(t, top, 40)
	assertRenderedHeightFits(t, top, 20)
	if !strings.Contains(top, "Esc/q back") || !strings.Contains(top, "↓ more") {
		t.Fatalf("runtime footer is not persistently actionable:\n%s", top)
	}

	updated, _ = m.Update(charKey('G'))
	m = updated.(*Model)
	bottom := m.renderRuntimeStatus()
	if !strings.Contains(bottom, "server-08") {
		t.Fatalf("scrolling did not reach final failure:\n%s", bottom)
	}
	assertRenderedLinesFit(t, bottom, 40)
	assertRenderedHeightFits(t, bottom, 20)
}

func TestRuntimeStatusPreservesScrollAcrossResize(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 10; i++ {
		m.failedServers = append(m.failedServers, FailedServer{Name: fmt.Sprintf("failed-%d", i), Reason: strings.Repeat("detail ", 8)})
	}
	m.openRuntimeStatus()
	m.runtimeStatusState.Viewport.GotoBottom()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m = updated.(*Model)
	if m.runtimeStatusState == nil || m.runtimeStatusState.Viewport.YOffset() == 0 {
		t.Fatal("runtime resize discarded the scroll position")
	}
	assertRenderedLinesFit(t, m.renderRuntimeStatus(), 60)
	assertRenderedHeightFits(t, m.renderRuntimeStatus(), 20)
}

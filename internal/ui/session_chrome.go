package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
)

// sessionHeaderProjection reserves workspace and model identity above the
// chronological transcript.
type sessionHeaderProjection struct {
	content        string
	reservedHeight int
}

// sessionHeaderActive is true when the Grok-style top chrome claims a row.
// Welcome and idle footer use this to avoid re-printing model/context.
func (m *Model) sessionHeaderActive() bool {
	if m == nil || !m.ready || m.height < 14 || m.width < 36 {
		return false
	}
	return m.chatPaneWidth() >= 30
}

// projectSessionHeader returns top chrome when the frame is tall enough. On
// minimum terminals (30x12) it stays empty so welcome+composer still fit.
func (m *Model) projectSessionHeader() sessionHeaderProjection {
	if !m.sessionHeaderActive() {
		return sessionHeaderProjection{}
	}
	paneW := m.chatPaneWidth()

	var lines []string
	if bar := m.renderSessionTopBar(paneW); bar != "" {
		lines = append(lines, bar)
	}
	if len(lines) == 0 {
		return sessionHeaderProjection{}
	}
	content := strings.Join(lines, "\n")
	return sessionHeaderProjection{
		content:        content,
		reservedHeight: lipgloss.Height(content),
	}
}

// renderSessionTopBar paints: branch · path ........ context meter
// Mode and model live on the bottom shortcuts row — not here.
// Content starts at OriginX so it lines up with welcome, transcript, and status.
func (m *Model) renderSessionTopBar(paneW int) string {
	grid := m.contentGrid()
	lead := grid.Prefix(" ")
	// Pack into LineWidth, not the pane: the meter must share the composer
	// box's right edge instead of sitting in the unused right chrome.
	innerW := min(grid.ContentWidth(), max(1, paneW-lipgloss.Width(lead)))
	left := m.sessionIdentityLeft(innerW)
	right := m.sessionIdentityRight(innerW)
	// Pack left and right with flexible spaces between.
	gap := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Prefer right-side instrumentation on narrow widths.
		if right != "" {
			return lead + truncateDisplayWithGlyphProfile(right, innerW, m.glyphProfile)
		}
		return lead + truncateDisplayWithGlyphProfile(left, innerW, m.glyphProfile)
	}
	return lead + left + strings.Repeat(" ", gap) + right
}

func (m *Model) sessionIdentityLeft(paneW int) string {
	branch := sessionGitBranchCached(m.workspaceDir())
	path := m.sessionWorkspaceLabel(paneW)
	parts := make([]string, 0, 2)
	if branch != "" && paneW >= 48 {
		parts = append(parts, m.styles.StatusText.Render(branch))
	}
	if path != "" {
		parts = append(parts, m.styles.Dimmed.Render(path))
	}
	if len(parts) == 0 {
		return m.styles.StatusText.Render("local-agent")
	}
	return strings.Join(parts, m.styles.Dimmed.Render(glyphSeparator(m.glyphProfile)))
}

// sessionIdentityRight carries the ambient identity this frame assigned to the
// top bar: model (or its remote boundary) and the context meter. Mode stays on
// the bottom row next to the key that changes it.
func (m *Model) sessionIdentityRight(paneW int) string {
	plan := m.planStatus()
	parts := make([]string, 0, 2)

	if m.currentModelIsNonLocal() {
		if plan.owns(factRemoteBoundary, surfaceTopBar) {
			// Compact keeps the boundary token and drops the model name; the
			// full label would crowd out the meter on ordinary widths.
			parts = append(parts, m.styles.StatusWarning.Render(
				m.currentModelReachabilityLabel(paneW < 72),
			))
		}
	} else if plan.owns(factModel, surfaceTopBar) {
		if model := m.currentModelReachabilityLabel(paneW < 58); model != "" {
			parts = append(parts, m.styles.Dimmed.Render(
				truncateDisplayWithGlyphProfile(model, min(24, max(8, paneW/3)), m.glyphProfile),
			))
		}
	}
	if plan.owns(factContext, surfaceTopBar) {
		if ctx := m.renderContextStatus(); ctx != "" {
			parts = append(parts, ctx)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, m.styles.Dimmed.Render(glyphSeparator(m.glyphProfile)))
}

func (m *Model) workspaceDir() string {
	if m != nil && m.agent != nil {
		if dir := strings.TrimSpace(m.agent.WorkDir()); dir != "" {
			return filepath.Clean(dir)
		}
	}
	if dir, err := os.Getwd(); err == nil {
		return filepath.Clean(dir)
	}
	return ""
}

func (m *Model) sessionWorkspaceLabel(paneW int) string {
	dir := m.workspaceDir()
	if dir == "" {
		return ""
	}
	// Top bar stays scannable and host-portable: basename only. Full paths vary
	// by machine (macOS home vs GitHub runner) and post-render normalizers
	// cannot fix padding computed from the longer original path.
	display := filepath.Base(dir)
	// Glyphrun / temp workdirs encode scenario names that still truncate
	// differently across hosts — keep a stable short label for those.
	if strings.Contains(display, "glyphrun") {
		display = "workspace"
	}
	limit := 18
	switch {
	case paneW >= 96:
		limit = 28
	case paneW >= 72:
		limit = 24
	case paneW >= 56:
		limit = 20
	}
	return truncateDisplayWithGlyphProfile(display, limit, m.glyphProfile)
}

// renderShortcutsBar is the fixed bottom product chrome (Grok-style):
//
//	enter send · shift+tab mode · …          ornith:latest · PLAN
//
// Left: key hints. Right: model · mode. One row, no second meta under the
// composer. While a turn is live the activity rail already owns esc stop /
// enter queue, so left keeps only mode.
// shortcutsBarActive reports whether the fixed bottom row is painted this
// frame. planStatus needs this before any surface renders, so the visibility
// rules live here rather than inline in renderShortcutsBar.
func (m *Model) shortcutsBarActive() bool {
	if m == nil || m.chatPaneWidth() < 24 || m.height < 12 {
		return false
	}
	// While a decision surface owns the footer, its own key hints are enough.
	if m.pendingApproval != nil || m.readScopePrompt != nil || m.pendingPaste != nil {
		return false
	}
	if m.overlay == OverlayCompletion || m.overlay == OverlayTranscriptSearch ||
		m.overlay == OverlayPlanForm || m.overlay == OverlayGoalForm ||
		m.cortexDecisionActive() {
		return false
	}
	return true
}

// shortcutsIdentityRightBudget is the width the shortcuts row reserves for its
// right-hand identity, or zero when the row is too narrow to carry any. Keeping
// it beside shortcutsBarActive lets planStatus ask the same question the
// renderer will answer.
func (m *Model) shortcutsIdentityRightBudget() int {
	paneW := m.chatPaneWidth()
	if !m.shortcutsBarActive() || paneW < 48 {
		return 0
	}
	// Enough for "model · PLAN" on 60-column terminals: paneW/4 alone can drop
	// authority once the budget falls under 16.
	return min(40, max(18, paneW/3))
}

// shortcutsIdentityActive reports whether the shortcuts row will paint ambient
// identity this frame.
func (m *Model) shortcutsIdentityActive() bool {
	return m.shortcutsIdentityRightBudget() > 0
}

func (m *Model) renderShortcutsBar(paneW int) string {
	if !m.shortcutsBarActive() {
		return ""
	}

	grid := m.contentGrid()
	lead := grid.Prefix(" ")
	inner := min(grid.ContentWidth(), max(1, paneW-lipgloss.Width(lead)))

	// Reserve room for right identity; pack hints into the remainder.
	rightBudget := m.shortcutsIdentityRightBudget()
	leftBudget := max(8, inner-rightBudget-1)

	var hints []keyHint
	if m.state == StateIdle && !m.composerIsBusy() {
		// Idle has nothing to cancel unless a queued follow-up already owns a
		// gesture. Advertising "esc cancel" here taught a dead verb.
		hints = []keyHint{
			{Key: "enter", Action: "send"},
			{Key: "shift+tab", Action: "mode"},
			{Key: m.keys.Help.Help().Key, Action: "help"},
		}
		if m.idleCancelAffordanceActive() {
			hints = []keyHint{
				{Key: "enter", Action: "send"},
				{Key: "shift+tab", Action: "mode"},
				{Key: "esc", Action: "cancel"},
				{Key: m.keys.Help.Help().Key, Action: "help"},
			}
		}
		if m.mouseCaptureOff {
			hints = append([]keyHint{{Key: "alt+m", Action: "exit select"}}, hints...)
		} else if strings.TrimSpace(m.input.Value()) == "" && m.lastAssistantContent() != "" {
			withCopy := append(append([]keyHint{}, hints...),
				keyHint{Key: "ctrl+y", Action: "copy"})
			if lipgloss.Width(m.renderKeyHintSet(mergeKeyHintAliases(withCopy), len(withCopy))) <= leftBudget {
				hints = withCopy
			}
		}
	} else {
		// Live activity rail already surfaces esc stop · enter queue.
		hints = []keyHint{
			{Key: "shift+tab", Action: "mode"},
		}
	}
	left := m.renderKeyHints(leftBudget, hints...)
	right := ""
	if rightBudget > 0 {
		right = m.renderFooterIdentityRight(rightBudget)
	}
	if left == "" && right == "" {
		return ""
	}
	if right == "" {
		return lead + left
	}
	if left == "" {
		gap := max(0, inner-lipgloss.Width(right))
		return lead + strings.Repeat(" ", gap) + right
	}
	gap := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Prefer keys when tight.
		return lead + left
	}
	return lead + left + strings.Repeat(" ", gap) + right
}

// idleCancelAffordanceActive reports whether Escape currently cancels something
// while the session is idle. Without that, the shortcuts row must not advertise
// "esc cancel" — a verb with no effect. local-agent has no voice listening
// stage; a queued follow-up is the cancel-worthy idle state.
func (m *Model) idleCancelAffordanceActive() bool {
	if m == nil {
		return false
	}
	return m.queuedFollowUp != nil
}

// git branch cache — avoid spawning git every frame.
type gitBranchCache struct {
	mu     sync.Mutex
	dir    string
	branch string
	at     time.Time
}

var sessionGitBranch gitBranchCache

func sessionGitBranchCached(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	sessionGitBranch.mu.Lock()
	defer sessionGitBranch.mu.Unlock()
	if sessionGitBranch.dir == dir && time.Since(sessionGitBranch.at) < 5*time.Second {
		return sessionGitBranch.branch
	}
	branch := ""
	cmd := exec.Command("git", "-C", dir, "branch", "--show-current")
	if out, err := cmd.Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	if branch == "" {
		// Detached HEAD fallback.
		cmd = exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
		if out, err := cmd.Output(); err == nil {
			branch = strings.TrimSpace(string(out))
			if branch == "HEAD" {
				branch = "detached"
			}
		}
	}
	sessionGitBranch.dir = dir
	sessionGitBranch.branch = branch
	sessionGitBranch.at = time.Now()
	return branch
}

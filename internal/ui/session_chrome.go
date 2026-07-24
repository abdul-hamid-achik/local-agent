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

// sessionHeaderProjection is the Grok-style top chrome: identity bar and an
// optional sticky last-user strip. projectFrame TakeTops this from SafeScreen
// so the transcript viewport shrinks automatically.
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
	// Sticky user: real conversation + room for vertical padding (Grok band).
	if m.stickyUserActive() {
		if sticky := m.renderStickyUserStrip(paneW); sticky != "" {
			// One blank row between identity bar and sticky when the frame is
			// tall enough — horizontal chrome was fine; vertical felt crushed.
			if m.height >= 18 && len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, sticky)
			// Breath before transcript content begins.
			if m.height >= 18 {
				lines = append(lines, "")
			}
		}
	}
	if len(lines) == 0 {
		return sessionHeaderProjection{}
	}
	content := strings.Join(lines, "\n")
	return sessionHeaderProjection{
		content:        content,
		// Sticky may be multi-line (padded band); measure real paint height.
		reservedHeight: lipgloss.Height(content),
	}
}

// renderSessionTopBar paints: branch · path ........ used/limit · mode
// Content starts at OriginX so it lines up with welcome, transcript, and status.
func (m *Model) renderSessionTopBar(paneW int) string {
	lead := m.contentGrid().Prefix(" ")
	innerW := max(1, paneW-lipgloss.Width(lead))
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

func (m *Model) sessionIdentityRight(paneW int) string {
	// Model and mode already live on the bottom shortcuts row (and AUTO in the
	// activity rail). Top-right only keeps the ambient context meter so chrome
	// does not triple-print identity.
	_ = paneW
	if ctx := m.renderContextStatus(); ctx != "" {
		return ctx
	}
	return ""
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
	home, _ := os.UserHomeDir()
	display := dir
	if home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			display = "~/" + filepath.ToSlash(rel)
			if rel == "." {
				display = "~"
			}
		}
	}
	// Prefer basename / short path so the top bar stays scannable.
	limit := 24
	switch {
	case paneW >= 96:
		limit = 40
	case paneW >= 72:
		limit = 32
	case paneW >= 56:
		// Mid width: keep ~/… but tighter.
		limit = 26
	default:
		display = filepath.Base(dir)
		limit = 18
	}
	return truncateDisplayWithGlyphProfile(display, limit, m.glyphProfile)
}

// stickyUserActive reports when the sticky last-user strip is painted. The
// transcript omits that same user entry so the prompt is not printed twice.
func (m *Model) stickyUserActive() bool {
	if !m.sessionHeaderActive() || m.height < 16 {
		return false
	}
	return m.latestUserPromptText() != ""
}

// renderStickyUserStrip keeps the latest user prompt visible while the body
// scrolls — Grok Build's "last message stays" band with vertical padding.
//
// Paint as ONE full-width style. Pre-coloring the rail/body and then applying
// Background only to the composite produces a partial "chip" highlight that
// looks broken against the terminal surface.
func (m *Model) renderStickyUserStrip(paneW int) string {
	text := m.latestUserPromptText()
	if text == "" || paneW < 8 {
		return ""
	}
	text = sanitizeTerminalSingleLine(text)
	// Soft single-line summary; multi-line drafts collapse to one sticky row.
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i]) + "…"
	}

	// Plain cells only — styling is applied once to the whole bar.
	rail := glyphSet(m.glyphProfile).UserRail
	if rail == "" {
		rail = "│"
	}
	// "▌ text…" with OriginX-aligned pad: accent(1) + pad(1) before text.
	prefix := rail + " "
	budget := max(4, paneW-lipgloss.Width(prefix)-1)
	// Harmonica sticky reveal: progressive runes, full bar width kept stable.
	revealed := revealStickyText(text, m.stickyReveal())
	body := truncateDisplayWithGlyphProfile(revealed, budget, m.glyphProfile)
	plain := " " + prefix + body
	// Pad with spaces so the elevated surface truly spans the pane.
	if gap := paneW - lipgloss.Width(plain); gap > 0 {
		plain += strings.Repeat(" ", gap)
	}

	palette := newSemanticPalette(m.isDark)
	// Subtle elevated band — close to Grok's sticky strip without a harsh chip.
	elevated := lipgloss.LightDark(m.isDark)(
		lipgloss.Color("#ECEFF4"), // nord snow storm 2
		lipgloss.Color("#3B4252"), // nord polar night 1 (readable band)
	)
	barStyle := lipgloss.NewStyle().
		Width(paneW).
		MaxWidth(paneW).
		Background(elevated).
		Foreground(palette.Text)

	// Vertical padding: on roomy frames paint a 3-row elevated band
	// (empty / prompt / empty) so the sticky isn't crushed against the
	// identity bar above or the transcript below. Horizontal layout was fine.
	content := barStyle.Render(plain)
	if m != nil && m.height >= 20 && paneW >= 40 {
		blank := barStyle.Render(strings.Repeat(" ", paneW))
		content = blank + "\n" + content + "\n" + blank
	}
	return content
}

func (m *Model) latestUserPromptText() string {
	if m == nil {
		return ""
	}
	if i := m.latestUserEntryIndex(); i >= 0 {
		return strings.TrimSpace(m.entries[i].Content)
	}
	return ""
}

func (m *Model) latestUserEntryIndex() int {
	if m == nil {
		return -1
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == "user" {
			return i
		}
	}
	return -1
}

// omitUserEntryFromTranscript skips the latest user block when the sticky
// strip already owns that prompt — one surface, one truth. Multi-line bodies
// and image attachments stay in the transcript because the sticky strip is a
// single-line summary only. While the sticky spring is still revealing text,
// keep the body copy so the prompt never disappears mid-animation.
func (m *Model) omitUserEntryFromTranscript(entryIndex int) bool {
	if !m.stickyUserActive() || entryIndex < 0 || entryIndex >= len(m.entries) {
		return false
	}
	if entryIndex != m.latestUserEntryIndex() {
		return false
	}
	if m.stickyReveal() < 0.92 {
		return false
	}
	entry := m.entries[entryIndex]
	if len(entry.Attachments) > 0 {
		return false
	}
	if strings.Contains(entry.Content, "\n") {
		return false
	}
	return true
}

// renderShortcutsBar is the fixed bottom product chrome (Grok-style):
//
//	enter send · shift+tab mode · …          ornith:latest · PLAN
//
// Left: key hints. Right: model · mode. One row, no second meta under the
// composer. While a turn is live the activity rail already owns esc stop /
// enter queue, so left keeps only mode.
func (m *Model) renderShortcutsBar(paneW int) string {
	if paneW < 24 || m.height < 12 {
		return ""
	}
	// While a decision surface owns the footer, its own key hints are enough.
	if m.pendingApproval != nil || m.readScopePrompt != nil || m.pendingPaste != nil {
		return ""
	}
	if m.overlay == OverlayCompletion || m.overlay == OverlayTranscriptSearch ||
		m.overlay == OverlayPlanForm || m.overlay == OverlayGoalForm ||
		m.cortexDecisionActive() {
		return ""
	}

	var hints []keyHint
	if m.state == StateIdle && !m.composerIsBusy() {
		hints = []keyHint{
			{Key: "enter", Action: "send"},
			{Key: "shift+tab", Action: "mode"},
			{Key: "esc", Action: "cancel"},
			{Key: m.keys.Help.Help().Key, Action: "help"},
		}
	} else {
		// Live activity rail already surfaces esc stop · enter queue.
		hints = []keyHint{
			{Key: "shift+tab", Action: "mode"},
		}
	}
	lead := m.contentGrid().Prefix(" ")
	inner := max(1, paneW-lipgloss.Width(lead))

	// Reserve room for right identity; pack hints into the remainder.
	rightBudget := 0
	if paneW >= 48 {
		rightBudget = min(36, max(12, paneW/4))
	}
	leftBudget := max(8, inner-rightBudget-1)
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

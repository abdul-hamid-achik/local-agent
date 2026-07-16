package ui

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// queuedFollowUp is deliberately limited to one item. A single visible queue
// slot lets the user keep working while a turn runs without creating a hidden
// backlog or losing the ability to revise the next instruction after failure.
type queuedFollowUp struct {
	Prompt                string
	Images                []pendingImageAttachment
	RecoveryHeld          bool
	ImageAdmissionThrough uint64
}

// renderComposerOverflowCue exposes Bubbles' internal textarea viewport only
// when a capped draft has hidden rows. This keeps the ordinary composer quiet
// while making long typed and pasted prompts recoverable without guesswork.
func (m *Model) renderComposerOverflowCue() string {
	if !m.composerEditable() || m.overlay != OverlayNone {
		return ""
	}
	earlierRows, laterRows := m.composerHiddenRows()
	if earlierRows == 0 && laterRows == 0 {
		return ""
	}

	var candidates []string
	switch {
	case earlierRows == 0:
		candidates = []string{"↓ later draft · ctrl+end bottom", "↓ later · ctrl+end", "↓ more draft"}
	case laterRows == 0:
		candidates = []string{"↑ earlier draft · ctrl+home top", "↑ earlier · ctrl+home", "↑ more draft"}
	default:
		candidates = []string{
			"↑ earlier draft · ↓ later draft · ctrl+home/end",
			"↑ earlier · ↓ later · home/end",
			"↑ earlier · ↓ later",
		}
	}
	width := max(1, m.chatPaneWidth())
	chosen := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if lipgloss.Width("  "+candidate) <= width {
			chosen = candidate
			break
		}
	}
	return truncateDisplay("  "+m.styles.StatusText.Render(chosen), width)
}

// composerHiddenRows reports the exact wrapped rows outside the textarea's
// viewport. Bubbles intentionally exposes the current offset but not its total
// visual-row count, so an uncapped copy computes that count with the identical
// prompt, styles, and wrapping width. The live child and its cursor are never
// mutated.
func (m *Model) composerHiddenRows() (earlier, later int) {
	if m == nil {
		return 0, 0
	}
	value := m.input.Value()
	if value == "" {
		return 0, 0
	}
	visible := max(1, m.input.Height())
	if m.input.MaxHeight > 0 && visible < m.input.MaxHeight {
		return 0, 0
	}
	width := max(20, m.chatPaneWidth())
	digest := sha256.Sum256([]byte(value))
	total := m.composerMeasureRows
	if digest != m.composerMeasureDigest || width != m.composerMeasureW || total <= 0 {
		probe := textarea.New()
		probe.CharLimit = m.input.CharLimit
		probe.DynamicHeight = true
		probe.MinHeight = 1
		probe.MaxHeight = 0
		probe.MaxContentHeight = m.input.MaxContentHeight
		probe.ShowLineNumbers = m.input.ShowLineNumbers
		configureComposerMode(&probe, m.isDark, m.presentedMode(), m.reducedMotion)
		probe.SetWidth(width)
		probe.SetValue(value)
		total = max(1, probe.Height())
		m.composerMeasureDigest = digest
		m.composerMeasureW = width
		m.composerMeasureRows = total
	}
	start := min(max(0, m.input.ScrollYOffset()), max(0, total-visible))
	return start, max(0, total-start-visible)
}

func (m *Model) queuedFollowUpHeld() bool {
	return m != nil && m.queuedFollowUp != nil && m.queuedFollowUp.RecoveryHeld
}

// composerEditable reports whether the textarea currently owns user input.
// Ordinary turns keep it available so drafting does not stop while the model
// reasons or tools run. Owned filesystem/session/goal operations still lock it
// because their completion may replace the active conversation authority.
func (m *Model) composerEditable() bool {
	if m.initializing || m.shuttingDown || m.overlay != OverlayNone ||
		m.pendingApproval != nil || m.pendingPaste != nil || m.pendingSessionSwitch != nil || m.readScopePrompt != nil {
		return false
	}
	if m.state == StateIdle {
		return (m.queuedFollowUp == nil || m.queuedFollowUpHeld()) && !m.composerIsBusy()
	}
	if m.queuedFollowUp != nil || m.goalTurnID != "" || m.goalOperation != "" {
		return false
	}
	return m.state == StateWaiting || m.state == StateStreaming
}

func (m *Model) queueComposerFollowUp() tea.Cmd {
	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" && len(m.pendingImages) > 0 {
		prompt = "Analyze the attached image."
	}
	if prompt == "" || m.queuedFollowUp != nil {
		return nil
	}
	// Transfer attachment ownership with the text. A queued instruction is one
	// atomic future turn; leaving its images in the generic pending bucket makes
	// the visible queue receipt lie and lets session operations discard half of
	// the draft.
	images := m.pendingImages
	m.pendingImages = nil
	queued := &queuedFollowUp{Prompt: prompt, Images: images}
	if m.imageAttachRunning {
		// Every already-admitted request in the current bounded pipeline belongs
		// to this queued owner, even if the active turn is rejected first.
		queued.ImageAdmissionThrough = m.imageAttachToken + uint64(len(m.imageAttachQueue))
	}
	m.queuedFollowUp = queued
	m.input.Reset()
	m.input.SetHeight(1)
	m.inputLines = 1
	m.recalcViewportHeight()
	return nil
}

// renderQueuedFollowUp keeps the single pending instruction visible while the
// active turn settles. It is deliberately one physical row: queue state should
// never steal an unpredictable amount of transcript space.
func (m *Model) renderQueuedFollowUp() string {
	if m.queuedFollowUp == nil {
		return ""
	}
	prompt := strings.Join(strings.Fields(sanitizeTerminalMultiline(m.queuedFollowUp.Prompt)), " ")
	if prompt == "" {
		prompt = "follow-up"
	}

	prefix := "  " + m.styles.FocusIndicator.Render("queued") + m.styles.StatusText.Render(" › ")
	imageMarker := ""
	if count := len(m.queuedFollowUp.Images); count > 0 {
		imageMarker = fmt.Sprintf(" · + %d image%s", count, pluralSuffix(count))
	}
	action := "edit"
	if m.queuedFollowUpHeld() {
		action = "swap"
	}
	hints := []string{" · ↑ " + action + " · esc clear", " · ↑ " + action + " · esc", " · ↑/esc", ""}
	width := max(1, m.chatPaneWidth())
	hint := hints[len(hints)-1]
	for _, candidate := range hints {
		if width-lipgloss.Width(prefix)-lipgloss.Width(imageMarker)-lipgloss.Width(candidate) >= 8 {
			hint = candidate
			break
		}
	}
	available := max(1, width-lipgloss.Width(prefix)-lipgloss.Width(imageMarker)-lipgloss.Width(hint))
	return prefix + m.styles.StatusText.Render(truncateDisplay(prompt, available)) +
		m.styles.StatusText.Render(imageMarker+hint)
}

// editQueuedFollowUp returns the one queued instruction to the live composer.
// Up owns this action before ordinary history navigation while a turn runs.
func (m *Model) editQueuedFollowUp() bool {
	if m.queuedFollowUp == nil {
		return false
	}
	if m.queuedFollowUpHeld() {
		return m.swapHeldQueuedFollowUp()
	}
	queued := m.queuedFollowUp
	prompt := queued.Prompt
	if draft := strings.TrimSpace(m.input.Value()); draft != "" {
		prompt += "\n" + m.input.Value()
	}
	if !m.restoreQueuedImages(queued.Images) {
		// Two independently valid four-image drafts must never become one
		// eight-image prompt. Expose both owners and let the user swap them.
		m.queuedFollowUp.RecoveryHeld = true
		m.recalcViewportHeight()
		return true
	}
	m.queuedFollowUp = nil
	m.clearCompletionSuppression()
	m.input.SetValue(prompt)
	m.input.CursorEnd()
	m.input.Focus()
	_ = m.reflowInputViewport()
	m.recalcViewportHeight()
	return true
}

// swapHeldQueuedFollowUp makes each recoverable draft editable without ever
// concatenating their text or attachment sets. If the live owner is empty, Up
// simply consumes the held draft back into the composer.
func (m *Model) swapHeldQueuedFollowUp() bool {
	if !m.queuedFollowUpHeld() || m.imageAttachRunning {
		return false
	}
	queued := m.queuedFollowUp
	livePrompt := m.input.Value()
	liveImages := m.pendingImages
	if len(queued.Images) > maxPendingImages || len(liveImages) > maxPendingImages {
		return false
	}

	m.clearCompletionSuppression()
	m.input.SetValue(queued.Prompt)
	m.input.CursorEnd()
	m.pendingImages = queued.Images
	if strings.TrimSpace(livePrompt) == "" && len(liveImages) == 0 {
		m.queuedFollowUp = nil
	} else {
		m.queuedFollowUp = &queuedFollowUp{
			Prompt:       livePrompt,
			Images:       liveImages,
			RecoveryHeld: true,
		}
	}
	m.input.Focus()
	_ = m.reflowInputViewport()
	m.recalcViewportHeight()
	return true
}

// clearQueuedFollowUp releases the queue slot without cancelling the active
// run. Escape owns this action before the run-cancel fallback.
func (m *Model) clearQueuedFollowUp() bool {
	if m.queuedFollowUp == nil {
		return false
	}
	queued := m.queuedFollowUp
	m.queuedFollowUp = nil
	clearTransientImages(queued.Images)
	m.input.Focus()
	m.syncInputHeight()
	m.recalcViewportHeight()
	return true
}

// restoreQueuedFollowUp returns authority to the user after a failed or
// cancelled turn. The queue slot is never silently retried after failure.
func (m *Model) restoreQueuedFollowUp() {
	if m.queuedFollowUp == nil {
		return
	}
	if m.queuedFollowUpHeld() {
		return
	}
	queued := m.queuedFollowUp
	prompt := queued.Prompt
	if strings.TrimSpace(m.input.Value()) != "" || !m.restoreQueuedImages(queued.Images) {
		m.queuedFollowUp.RecoveryHeld = true
		return
	}
	m.queuedFollowUp = nil
	m.input.SetValue(prompt)
	m.input.CursorEnd()
	_ = m.reflowInputViewport()
}

// dispatchQueuedFollowUp starts the one queued instruction only after the
// preceding turn has completed and its state has been durably settled.
func (m *Model) dispatchQueuedFollowUp() tea.Cmd {
	if m.queuedFollowUp == nil || m.queuedFollowUpHeld() || m.state != StateIdle {
		return nil
	}
	queued := m.queuedFollowUp
	prompt := queued.Prompt
	if !m.restoreQueuedImages(queued.Images) {
		m.queuedFollowUp.RecoveryHeld = true
		m.recalcViewportHeight()
		return nil
	}
	m.queuedFollowUp = nil
	m.input.SetValue(prompt)
	m.input.CursorEnd()
	_ = m.reflowInputViewport()
	return m.submitInput()
}

// captureComposerFollowUpForRollback moves any draft prepared during the
// active turn out of the way before the rejected turn restores its own prompt
// and attachments. The caller either marks it held after a successful rollback
// or restores it normally when the rollback cannot be proven safe.
func (m *Model) captureComposerFollowUpForRollback() bool {
	if m == nil || m.queuedFollowUp != nil ||
		(strings.TrimSpace(m.input.Value()) == "" && len(m.pendingImages) == 0) {
		return false
	}
	queued := &queuedFollowUp{Prompt: m.input.Value(), Images: m.pendingImages}
	if m.imageAttachRunning {
		queued.ImageAdmissionThrough = m.imageAttachToken + uint64(len(m.imageAttachQueue))
	}
	m.queuedFollowUp = queued
	m.pendingImages = nil
	m.input.Reset()
	m.input.SetHeight(1)
	m.inputLines = 1
	return true
}

func (m *Model) holdQueuedFollowUpAfterRollback() {
	if m != nil && m.queuedFollowUp != nil {
		m.queuedFollowUp.RecoveryHeld = true
	}
}

func (m *Model) queuedFollowUpAutoDispatchable() bool {
	return m != nil && m.queuedFollowUp != nil && !m.queuedFollowUp.RecoveryHeld
}

// blockSessionReplacementForHeldFollowUp keeps a recovery-held queue bound to
// the conversation that created it. Session replacement must never silently
// discard that owner or carry it into an unrelated transcript.
func (m *Model) blockSessionReplacementForHeldFollowUp(action string) bool {
	if !m.queuedFollowUpHeld() {
		return false
	}
	action = strings.TrimSpace(sanitizeTerminalSingleLine(action))
	if action == "" {
		action = "replacing this conversation"
	}
	notice := "Resolve the held follow-up before " + action + ": ↑ swap · Esc clear."
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].Kind != "system" || m.entries[len(m.entries)-1].Content != notice {
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: notice})
	}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
	m.input.Focus()
	m.recalcViewportHeight()
	return true
}

func (m *Model) clearQueuedFollowUpForSessionReplacement() {
	if m == nil || m.queuedFollowUp == nil {
		return
	}
	clearTransientImages(m.queuedFollowUp.Images)
	m.queuedFollowUp = nil
}

func (m *Model) queuedFollowUpOwnsImageAdmission(token uint64) bool {
	if m == nil || m.queuedFollowUp == nil {
		return false
	}
	if !m.queuedFollowUp.RecoveryHeld {
		return true
	}
	return m.queuedFollowUp.ImageAdmissionThrough > 0 && token <= m.queuedFollowUp.ImageAdmissionThrough
}

func (m *Model) releaseQueuedImageAdmissionOwnership() {
	if m != nil && m.queuedFollowUp != nil {
		m.queuedFollowUp.ImageAdmissionThrough = 0
	}
}

func clearTransientImages(images []pendingImageAttachment) {
	for index := range images {
		images[index] = pendingImageAttachment{}
	}
}

// restoreQueuedImages returns a queued turn's attachments to the live draft.
// Any late admission receipt follows them, preserving order and digest-level
// deduplication without retaining a second hidden attachment owner.
func (m *Model) restoreQueuedImages(images []pendingImageAttachment) bool {
	combined := make([]pendingImageAttachment, 0, len(images)+len(m.pendingImages))
	seen := make(map[string]struct{}, cap(combined))
	appendUnique := func(attachment pendingImageAttachment) bool {
		key := attachment.Ref.Digest
		if key != "" {
			if _, duplicate := seen[key]; duplicate {
				return true
			}
			seen[key] = struct{}{}
		}
		if len(combined) >= maxPendingImages {
			return false
		}
		combined = append(combined, attachment)
		return true
	}
	for _, attachment := range images {
		if !appendUnique(attachment) {
			return false
		}
	}
	for _, attachment := range m.pendingImages {
		if !appendUnique(attachment) {
			return false
		}
	}
	m.pendingImages = combined
	return true
}

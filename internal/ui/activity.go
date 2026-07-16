package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
)

type workingActivity struct {
	label        string
	compactLabel string
	detail       string
	elapsed      time.Duration
	cancellable  bool
	waiting      bool
	static       bool
}

func reducedMotionRequested() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("LOCAL_AGENT_REDUCED_MOTION")))
	return value != "" && value != "0" && value != "false" && value != "off"
}

func (m *Model) nowTime() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Model) turnElapsed() time.Duration {
	if m.turnStartedAt.IsZero() {
		return 0
	}
	elapsed := m.nowTime().Sub(m.turnStartedAt)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func formatWorkingElapsed(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed < 10*time.Second {
		return fmt.Sprintf("%.1fs", elapsed.Seconds())
	}
	if elapsed < time.Minute {
		return fmt.Sprintf("%.0fs", elapsed.Seconds())
	}
	minutes := int(elapsed / time.Minute)
	seconds := int(elapsed/time.Second) % 60
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

func (m *Model) currentWorkingActivity() (workingActivity, bool) {
	// Interactive prompts own the footer while they are open. Reporting work
	// behind an approval would imply progress even though the operation is
	// deliberately paused on the user's decision.
	if m.pendingApproval != nil || m.pendingPaste != nil {
		return workingActivity{}, false
	}

	switch {
	case m.shuttingDown:
		return workingActivity{label: "Stopping safely", detail: "waiting for receipts"}, true
	case m.initializing:
		settled := 0
		for _, item := range m.startupItems {
			if item.Status == "connected" || item.Status == "failed" {
				settled++
			}
		}
		detail := "local runtime"
		if len(m.startupItems) > 0 {
			detail += fmt.Sprintf(" · %d/%d", settled, len(m.startupItems))
		}
		return workingActivity{label: "Starting", detail: detail}, true
	case m.sessionListing:
		return workingActivity{label: "Loading sessions", cancellable: true}, true
	case m.sessionLoading:
		return workingActivity{label: "Restoring session", cancellable: true}, true
	case m.fileLoading:
		return workingActivity{label: "Reading local file", cancellable: true}, true
	case m.imageAttachRunning:
		return workingActivity{label: "Attaching image", detail: "validating private copy", cancellable: true}, true
	case m.readScopeOpRunning:
		label := m.readScopeOpLabel
		if label == "" {
			label = "Updating read-only scope"
		}
		return workingActivity{label: label, detail: "writes remain workspace-only"}, true
	case m.commitRunning:
		return workingActivity{label: "Generating commit", cancellable: true}, true
	case m.exportRunning:
		return workingActivity{label: "Publishing export"}, true
	case m.goalOperation != "":
		return workingActivity{label: m.goalOperation, detail: "Cortex", cancellable: true}, true
	case m.compactingContext:
		return workingActivity{label: "Preparing context", detail: "summarizing earlier turns", cancellable: true}, true
	case m.toolsPending > 0:
		if activity, ok := m.runningExpertActivity(); ok {
			return activity, true
		}
		// The running ToolCard is the single animated, detailed surface for tool
		// work. The footer keeps only the global cancellation affordance.
		activity := workingActivity{label: "Tool running", cancellable: true, static: true}
		if m.toolsPending > 1 {
			activity.label = fmt.Sprintf("%d tools running", m.toolsPending)
		}
		return activity, true
	case m.autoCheckpoints.segmentsContinued > 0 && (m.state == StateWaiting || m.state == StateStreaming):
		return workingActivity{
			label:        "Continuing automatically",
			compactLabel: "AUTO continuing",
			detail:       fmt.Sprintf("checkpoint %d/%d", m.autoCheckpoints.segmentsContinued, maxAutoCheckpointSegments),
			cancellable:  true,
		}, true
	case m.capabilityRoute != nil && (m.state == StateWaiting || m.state == StateStreaming):
		route := *m.capabilityRoute
		if route.Status != agent.CapabilityRouteResolved {
			// A resolver miss or failure is advisory metadata, not the state of
			// the provider turn. Keep the active execution as the primary label
			// and expose the typed route state as progressive detail. Runtime
			// retains the complete advisory after the turn settles.
			return workingActivity{
				label: "Running", detail: capabilityRouteLabel(route),
				elapsed: m.turnElapsed(), cancellable: true,
			}, true
		}
		return workingActivity{
			label: capabilityRouteLabel(route), compactLabel: capabilityRouteCompactLabel(route),
			detail:  capabilityRouteDetail(route),
			elapsed: m.turnElapsed(), cancellable: true,
		}, true
	case m.state == StateWaiting:
		return workingActivity{
			label: "Running", elapsed: m.turnElapsed(), cancellable: true, waiting: true,
		}, true
	case m.state == StateStreaming:
		return workingActivity{
			label: "Running", elapsed: m.turnElapsed(), cancellable: true,
		}, true
	case m.ollamaInventoryCommitting:
		return workingActivity{label: "Updating Ollama inventory", detail: "verifying model authority"}, true
	case m.standaloneRecovery != nil && m.standaloneRecovery.loading:
		// Inspection is read-only and normally completes quickly. A static status
		// both locks the composer and avoids introducing another animation clock
		// for a one-shot durable receipt lookup.
		return workingActivity{label: "Inspecting recovery", detail: "read-only durable receipt", static: true}, true
	default:
		return workingActivity{}, false
	}
}

func (m *Model) composerIsBusy() bool {
	_, busy := m.currentWorkingActivity()
	return busy
}

// needsSpinner reports whether the parent model currently owns an animated
// Bubbles spinner. Waiting uses the separate scramble animation so only one
// motion clock owns each phase.
func (m *Model) needsSpinner() bool {
	if m.reducedMotion {
		return false
	}
	activity, active := m.currentWorkingActivity()
	return active && !activity.waiting
}

func (m *Model) needsScramble() bool {
	if m.reducedMotion {
		return false
	}
	activity, active := m.currentWorkingActivity()
	return active && activity.waiting
}

func (m *Model) startActivityCmd() tea.Cmd {
	if m.needsScramble() {
		return m.scramble.Tick()
	}
	return m.startSpinnerCmd()
}

func (m *Model) startSpinnerCmd() tea.Cmd {
	if !m.needsSpinner() {
		return nil
	}
	return m.spin.Tick
}

func (m *Model) renderWorkingLine() string {
	activity, ok := m.currentWorkingActivity()
	if !ok {
		return ""
	}
	activity.label = sanitizeTerminalSingleLine(activity.label)
	activity.compactLabel = sanitizeTerminalSingleLine(activity.compactLabel)
	activity.detail = sanitizeTerminalSingleLine(activity.detail)
	if activity.label == "" {
		activity.label = "Working"
	}

	// A single-cell ellipsis communicates unfinished work even when animation is
	// disabled. Unlike a filled dot it cannot be mistaken for a settled status
	// marker, and it keeps reduced-motion and static operations width-stable.
	motion := m.styles.StatusDot.Render("…")
	if !m.reducedMotion && !activity.static {
		if activity.waiting {
			cells := 1
			if m.chatPaneWidth() >= 58 {
				cells = 6
			}
			motion = m.scramble.ViewN(cells)
			if motion == "" {
				motion = m.styles.StatusDot.Render("…")
			}
		} else {
			motion = m.spin.View()
		}
	}

	longCancel := ""
	shortCancel := ""
	if activity.cancellable {
		longCancel = " · esc cancel"
		shortCancel = " · esc"
	}

	elapsed := ""
	// Sub-second timers flicker between otherwise identical frames and add no
	// useful progress signal. Start showing elapsed time after one full second;
	// longer operations still keep the compact live timer.
	if activity.elapsed >= time.Second {
		elapsed = " · " + formatWorkingElapsed(activity.elapsed)
	}
	detail := ""
	if activity.detail != "" {
		detail = " · " + activity.detail
	}
	if len(m.pendingImages) > 0 && m.queuedFollowUp == nil {
		detail = fmt.Sprintf(" · + %d image%s", len(m.pendingImages), pluralSuffix(len(m.pendingImages))) + detail
	}
	authority := ""
	switch m.presentedMode() {
	case ModeAuto:
		// The ordinary idle footer is replaced while work is active. Keep AUTO's
		// authority visible in the activity rail so a long-running turn never
		// leaves the user guessing whether it is operating autonomously.
		authority = " · AUTO"
	case ModePlan:
		// PLAN is also an authority boundary: it may inspect and reason, but must
		// not be mistaken for an ordinary implementation turn while the idle
		// status row is replaced by live activity.
		authority = " · PLAN"
	}
	queueAction := ""
	if m.queuedFollowUp == nil && m.goalTurnID == "" && m.goalOperation == "" &&
		(m.state == StateWaiting || m.state == StateStreaming) {
		queueAction = " · enter queue"
	}

	candidates := make([]string, 0, 24)
	if authority != "" {
		candidates = append(candidates,
			activity.label+authority+detail+elapsed+longCancel+queueAction,
			activity.label+authority+elapsed+longCancel+queueAction,
			activity.label+authority+longCancel+queueAction,
		)
		if queueAction != "" {
			candidates = append(candidates,
				activity.label+authority+elapsed+shortCancel+" · queue",
				activity.label+authority+shortCancel+" · queue",
			)
			if activity.compactLabel != "" {
				candidates = append(candidates,
					activity.compactLabel+authority+elapsed+shortCancel+" · queue",
					activity.compactLabel+authority+shortCancel+" · queue",
				)
			}
			candidates = append(candidates,
				"Run"+authority+shortCancel+" · queue",
				// At the 30-column tier a visible session handle and the queue
				// affordance cannot both fit with the execution authority. Preserve
				// AUTO/PLAN and cancellation first; the focused composer still
				// advertises Enter as the queue action.
				"Run"+authority+shortCancel,
				"Run"+authority,
			)
		} else {
			if activity.compactLabel != "" {
				candidates = append(candidates,
					activity.compactLabel+authority+elapsed+shortCancel,
					activity.compactLabel+authority+shortCancel,
				)
			}
			candidates = append(candidates, "Run"+authority+shortCancel)
		}
	}
	candidates = append(candidates,
		activity.label+detail+elapsed+longCancel+queueAction,
		activity.label+elapsed+longCancel+queueAction,
		activity.label+longCancel+queueAction,
	)
	if queueAction != "" {
		// Preserve the semantic activity label by shortening controls before
		// falling back to the compact identity. This keeps typed routing states
		// inspectable at ordinary widths while still fitting the 30-column tier.
		candidates = append(candidates,
			activity.label+elapsed+shortCancel+" · queue",
			activity.label+shortCancel+" · queue",
		)
		if activity.compactLabel != "" {
			candidates = append(candidates,
				activity.compactLabel+elapsed+shortCancel+" · queue",
				activity.compactLabel+shortCancel+" · queue",
			)
		}
		candidates = append(candidates, "Run"+shortCancel+" · queue")
	}
	if activity.compactLabel != "" {
		candidates = append(candidates,
			activity.compactLabel+elapsed+shortCancel,
			activity.compactLabel+shortCancel,
			activity.compactLabel,
		)
	}
	candidates = append(candidates,
		activity.label+elapsed+longCancel,
		activity.label+longCancel,
		activity.label+elapsed+shortCancel,
		activity.label+shortCancel,
		activity.label,
	)
	if m.followPaused() {
		candidates = []string{
			activity.label + authority + detail + elapsed + longCancel + " · end latest",
			activity.label + authority + elapsed + longCancel + " · end latest",
			activity.label + authority + longCancel + " · end latest",
			activity.label + authority + shortCancel + " · end",
			"Paused" + authority + shortCancel + " · end",
			"Paused" + authority + " · end",
		}
	}

	leftPad := "  "
	if m.chatPaneWidth() < 40 {
		leftPad = " "
	}
	textWidth := max(1, m.chatPaneWidth()-lipgloss.Width(leftPad)-lipgloss.Width(motion)-1)
	session := ""
	selectionWidth := textWidth
	titleLimit := 0
	if m.chatPaneWidth() >= 72 {
		titleLimit = 24
	}
	sessionID := m.sessionID
	sessionTitle := m.activeSessionTitle
	if pending := m.pendingSessionSwitch; m.sessionLoading && pending != nil &&
		pending.Choice != sessionSwitchUndecided && pending.LoadToken == m.sessionLoadToken {
		// Until the tokened receipt commits, m.sessionID still names the source
		// conversation. The activity rail must identify the target being restored,
		// not imply that the source session is being reloaded.
		sessionID = pending.TargetSessionID
		sessionTitle = pending.TargetTitle
	}
	session = sessionDisplayLabel(sessionID, sessionTitle, titleLimit)
	if session != "" {
		selectionWidth = max(1, textWidth-lipgloss.Width(" · ")-lipgloss.Width(session))
	}
	chosen := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if lipgloss.Width(candidate) <= selectionWidth {
			chosen = candidate
			break
		}
	}
	chosen = truncateDisplay(chosen, selectionWidth)
	if session != "" {
		chosen += " · " + session
	}
	return leftPad + motion + " " + m.renderWorkingCandidate(chosen)
}

// renderWorkingCandidate separates live state, authority, metadata, and keys
// without changing the carefully selected responsive text budget above. This
// keeps the footer scannable in both light and dark terminals while NO_COLOR
// still receives the exact same plain-text grammar.
func (m *Model) renderWorkingCandidate(candidate string) string {
	segments := strings.Split(sanitizeTerminalSingleLine(candidate), " · ")
	for index, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		switch {
		case index == 0:
			segments[index] = m.styles.ToolRunningText.Render(segment)
		case segment == "AUTO":
			segments[index] = m.styles.ModeBuild.Render(segment)
		case segment == "PLAN":
			segments[index] = m.styles.ModePlan.Render(segment)
		case workingControlKey(segment) != "":
			keyLabel := workingControlKey(segment)
			action := strings.TrimSpace(strings.TrimPrefix(segment, keyLabel))
			segments[index] = m.styles.FocusIndicator.Render(keyLabel)
			if action != "" {
				segments[index] += " " + m.styles.StreamHint.Render(action)
			}
		default:
			segments[index] = m.styles.StreamHint.Render(segment)
		}
	}
	return strings.Join(segments, m.styles.StreamHint.Render(" · "))
}

func workingControlKey(segment string) string {
	keyLabel, _, _ := strings.Cut(strings.TrimSpace(segment), " ")
	switch keyLabel {
	case "esc", "enter", "end":
		return keyLabel
	default:
		return ""
	}
}

func (m *Model) renderContextStatus(compact bool) string {
	if m.promptTokens <= 0 || m.numCtx <= 0 {
		return ""
	}
	percent := m.promptTokens * 100 / m.numCtx
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	style := m.styles.ContextPctLow
	if percent >= 85 {
		style = m.styles.ContextPctHigh
	} else if percent >= 65 {
		style = m.styles.ContextPctMid
	}
	if compact {
		return style.Render(fmt.Sprintf("ctx %d%%", percent))
	}

	filled := (percent + 19) / 20
	if filled > 5 {
		filled = 5
	}
	meter := strings.Repeat("▮", filled) + strings.Repeat("▯", 5-filled)
	return style.Render(fmt.Sprintf("ctx %s %d%%", meter, percent))
}

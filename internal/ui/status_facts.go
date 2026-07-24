package ui

// Ambient session state used to be decided independently by four surfaces: the
// session top bar, the idle status line, the bottom shortcuts row, and the
// empty-state welcome. Each one carried its own guards to avoid repeating what
// it guessed another surface was already showing — `if !headerActive`, "the top
// bar owns ambient identity", "mode lives on the bottom shortcuts row". Those
// guards only ever covered the pairs someone had noticed, so the model name and
// mode kept reappearing twice on the same screen from a pair nobody had checked
// yet (welcome and the shortcuts row, for one).
//
// This file makes ownership explicit instead. Each ambient fact resolves to
// exactly one surface for the current frame, every surface asks before painting,
// and a duplicate becomes impossible to express rather than something to notice
// and patch. Operational one-shots (notices, MCP failures, paste prompts) are
// not modelled here: they have a single owner already.

// statusSurface is a place that can carry ambient session state.
type statusSurface int

const (
	surfaceNone statusSurface = iota
	// surfaceTopBar is the session identity row above the transcript.
	surfaceTopBar
	// surfaceStatusLine is the operational row directly above the composer.
	surfaceStatusLine
	// surfaceShortcuts is the fixed product row at the bottom of the frame.
	surfaceShortcuts
	// surfaceWelcome is the empty-state orientation copy inside the transcript.
	surfaceWelcome
)

// statusFact is one piece of ambient state that more than one surface could
// reasonably show.
type statusFact int

const (
	// factModel is the active model name.
	factModel statusFact = iota
	// factRemoteBoundary is the CLOUD/REMOTE prompt-leaves-this-machine label.
	// It is tracked apart from factModel because it is a privacy boundary: it
	// keeps a surface even where a plain model name would be dropped for width.
	factRemoteBoundary
	// factContext is the prompt/context-window meter.
	factContext
	// factMode is the NORMAL/PLAN/AUTO authority badge.
	factMode
)

// statusPlan is the resolved owner of each ambient fact for one frame.
type statusPlan struct {
	owners [4]statusSurface
}

// owns reports whether surface should paint fact in this frame. A surface that
// does not own a fact must not paint it, even when it has room.
func (p statusPlan) owns(fact statusFact, surface statusSurface) bool {
	if fact < 0 || int(fact) >= len(p.owners) {
		return false
	}
	return p.owners[fact] == surface
}

// ownedBy returns the surface that will paint fact, or surfaceNone.
func (p statusPlan) ownedBy(fact statusFact) statusSurface {
	if fact < 0 || int(fact) >= len(p.owners) {
		return surfaceNone
	}
	return p.owners[fact]
}

// planStatus assigns every ambient fact to a single surface.
//
// Preference order per fact is fixed, and each fact falls through to the next
// surface only when the preferred one is not painted in this frame. That is why
// this is computed once per frame from surface availability rather than from
// each renderer's local view of the world.
func (m *Model) planStatus() statusPlan {
	plan := statusPlan{}
	if m == nil {
		return plan
	}

	header := m.sessionHeaderActive()
	// Not shortcutsBarActive: the bar renders its key hints well below the
	// width at which it can also carry identity, and assigning a fact to a
	// surface that will silently drop it loses the fact entirely.
	shortcuts := m.shortcutsIdentityActive()
	// The welcome surface only exists before a conversation starts, and it is
	// the orientation copy of last resort on frames too small for chrome.
	welcome := !m.conversationStarted()

	// Identity belongs at the top. It is ambient, it is read once, and keeping
	// it there frees the bottom row for keys and authority.
	switch {
	case header:
		plan.owners[factModel] = surfaceTopBar
		plan.owners[factContext] = surfaceTopBar
		plan.owners[factRemoteBoundary] = surfaceTopBar
	case shortcuts:
		plan.owners[factModel] = surfaceShortcuts
		plan.owners[factContext] = surfaceStatusLine
		plan.owners[factRemoteBoundary] = surfaceShortcuts
	case welcome:
		plan.owners[factModel] = surfaceWelcome
		plan.owners[factContext] = surfaceStatusLine
		plan.owners[factRemoteBoundary] = surfaceWelcome
	default:
		plan.owners[factModel] = surfaceStatusLine
		plan.owners[factContext] = surfaceStatusLine
		plan.owners[factRemoteBoundary] = surfaceStatusLine
	}

	// Authority belongs at the bottom, next to the keys that change it.
	switch {
	case shortcuts:
		plan.owners[factMode] = surfaceShortcuts
	case welcome:
		plan.owners[factMode] = surfaceWelcome
	default:
		plan.owners[factMode] = surfaceStatusLine
	}

	return plan
}

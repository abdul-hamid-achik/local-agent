package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PlanFormField represents a single field in the plan form.
type PlanFormField struct {
	Label       string
	Kind        string   // "text" or "select"
	Value       string   // current value (for select, set from Options[OptionIndex])
	Options     []string // for "select" kind
	OptionIndex int      // for "select" kind
	Input       textinput.Model
}

// PlanFormState holds state for the plan form overlay.
type PlanFormState struct {
	Fields      []PlanFormField
	ActiveField int
}

// NewPlanFormState creates a plan form pre-filled with the user's task description.
func NewPlanFormState(task string) *PlanFormState {
	taskInput := textinput.New()
	taskInput.Placeholder = "Describe the task..."
	taskInput.CharLimit = 512
	taskInput.SetValue(task)
	taskInput.Focus()

	focusInput := textinput.New()
	focusInput.Placeholder = "Any constraints or requirements? (optional)"
	focusInput.CharLimit = 512

	return &PlanFormState{
		Fields: []PlanFormField{
			{
				Label: "Task",
				Kind:  "text",
				Input: taskInput,
			},
			{
				Label:   "Scope",
				Kind:    "select",
				Options: []string{"single file", "module", "project-wide"},
			},
			{
				Label: "Focus (optional)",
				Kind:  "text",
				Input: focusInput,
			},
		},
		ActiveField: 0,
	}
}

// AssemblePrompt builds the structured prompt from form fields.
func (pf *PlanFormState) AssemblePrompt() string {
	task := pf.Fields[0].Input.Value()
	scope := pf.Fields[1].Options[pf.Fields[1].OptionIndex]
	focus := pf.Fields[2].Input.Value()

	var b strings.Builder
	b.WriteString("Plan the following task:\n")
	b.WriteString(fmt.Sprintf("Task: %s\n", task))
	b.WriteString(fmt.Sprintf("Scope: %s\n", scope))
	if focus != "" {
		b.WriteString(fmt.Sprintf("Focus: %s\n", focus))
	}
	b.WriteString("\nProvide a step-by-step plan.")
	return b.String()
}

// updatePlanForm handles key events within the plan form overlay.
// Returns the updated model, any command, and whether the form was submitted or cancelled.
func (m *Model) updatePlanForm(msg tea.KeyPressMsg) (bool, bool) {
	pf := m.planFormState
	if pf == nil {
		return false, false
	}

	field := &pf.Fields[pf.ActiveField]

	switch {
	case key.Matches(msg, m.keys.Cancel):
		// Cancel
		return false, true

	case msg.Code == tea.KeyEnter:
		if pf.ActiveField == len(pf.Fields)-1 {
			// Submit
			return true, false
		}
		// Advance to next field
		m.advancePlanFormField(1)
		return false, false

	case msg.Code == tea.KeyTab:
		if msg.Mod == tea.ModShift {
			m.advancePlanFormField(-1)
		} else {
			m.advancePlanFormField(1)
		}
		return false, false

	case msg.Code == tea.KeyUp:
		if field.Kind == "select" {
			if field.OptionIndex > 0 {
				field.OptionIndex--
			}
			return false, false
		}

	case msg.Code == tea.KeyDown:
		if field.Kind == "select" {
			if field.OptionIndex < len(field.Options)-1 {
				field.OptionIndex++
			}
			return false, false
		}
	}

	// Forward other keys to active text field
	if field.Kind == "text" {
		field.Input, _ = field.Input.Update(msg)
	}

	return false, false
}

// advancePlanFormField moves to the next or previous field.
func (m *Model) advancePlanFormField(dir int) {
	pf := m.planFormState
	if pf == nil {
		return
	}

	// Blur current field
	current := &pf.Fields[pf.ActiveField]
	if current.Kind == "text" {
		current.Input.Blur()
	}

	pf.ActiveField += dir
	if pf.ActiveField < 0 {
		pf.ActiveField = 0
	}
	if pf.ActiveField >= len(pf.Fields) {
		pf.ActiveField = len(pf.Fields) - 1
	}

	// Focus new field
	next := &pf.Fields[pf.ActiveField]
	if next.Kind == "text" {
		next.Input.Focus()
	}
}

// renderPlanForm renders the plan form overlay.
func (m *Model) renderPlanForm() string {
	pf := m.planFormState
	if pf == nil {
		return ""
	}

	activeStyle := m.styles.StartupCheck // reuse the success-colored style for active fields

	var b strings.Builder
	b.WriteString(m.styles.OverlayTitle.Render("Plan Task"))
	b.WriteString("\n\n")

	for i, field := range pf.Fields {
		isActive := i == pf.ActiveField

		ls := m.styles.OverlayAccent
		if isActive {
			ls = activeStyle
		}
		b.WriteString(ls.Render(field.Label))
		b.WriteString("\n")

		switch field.Kind {
		case "text":
			if isActive {
				b.WriteString("> " + field.Input.View())
			} else {
				val := field.Input.Value()
				if val == "" {
					val = m.styles.OverlayDim.Render("(empty)")
				}
				b.WriteString("  " + m.styles.OverlayDim.Render(val))
			}
		case "select":
			var opts []string
			for j, opt := range field.Options {
				if j == field.OptionIndex {
					if isActive {
						opts = append(opts, activeStyle.Render("▸ "+opt))
					} else {
						opts = append(opts, opt)
					}
				} else {
					opts = append(opts, m.styles.OverlayDim.Render(opt))
				}
			}
			b.WriteString("  " + strings.Join(opts, " / "))
		}
		b.WriteString("\n\n")
	}

	b.WriteString(m.styles.OverlayDim.Render("Tab=next  Enter=submit  Esc=cancel"))

	maxW := 50
	if m.width-8 > maxW {
		maxW = m.width - 8
	}
	if maxW > 60 {
		maxW = 60
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.OverlayBorder).
		Padding(1, 2).
		Width(maxW)

	return box.Render(b.String())
}

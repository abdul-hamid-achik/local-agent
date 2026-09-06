package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// QuestionChoice is one option in an agent-authored question.
type QuestionChoice struct {
	ID     string
	Label  string
	Detail string
}

// QuestionKind selects the interaction grammar for one prompt.
type QuestionKind uint8

const (
	QuestionKindSingle QuestionKind = iota
	QuestionKindYesNo
	QuestionKindFreeText
)

const questionChoiceWindow = 4

// QuestionPrompt is a parent-routed, persistence-free question surface.
// Cortex decisions and any future AskUser tool share this grammar so the
// agent asking something does not invent a new footer each time.
//
// Yes/No defaults to No (index of the deny-safe option). Single-choice
// starts unselected so Enter cannot confirm an implicit first option.
type QuestionPrompt struct {
	Kind     QuestionKind
	Title    string
	Prompt   string
	Choices  []QuestionChoice
	Selected int
	Notice   string

	width        int
	styles       Styles
	glyphProfile GlyphProfile
	sep          string
	input        textinput.Model
	hasInput     bool
}

func newQuestionPrompt(kind QuestionKind, title, prompt string, choices []QuestionChoice, styles Styles, profiles ...GlyphProfile) QuestionPrompt {
	selected := -1
	if kind == QuestionKindYesNo && len(choices) > 0 {
		selected = len(choices) - 1
		for index, choice := range choices {
			if strings.EqualFold(choice.ID, "no") || strings.EqualFold(choice.Label, "no") {
				selected = index
				break
			}
		}
	}
	return QuestionPrompt{
		Kind:         kind,
		Title:        title,
		Prompt:       prompt,
		Choices:      append([]QuestionChoice(nil), choices...),
		Selected:     selected,
		styles:       styles,
		glyphProfile: resolveGlyphProfile(profiles...),
	}
}

func (q *QuestionPrompt) SetWidth(width int) {
	if q == nil {
		return
	}
	q.width = width
	if q.hasInput {
		q.input.SetWidth(max(1, q.contentWidth()-2))
	}
}

func (q *QuestionPrompt) initFreeText(isDark bool, themeID string, reducedMotion bool) {
	if q == nil {
		return
	}
	q.Kind = QuestionKindFreeText
	input := textinput.New()
	input.Placeholder = "Type an answer"
	input.CharLimit = 1024
	input.Prompt = ""
	input.SetStyles(semanticTextInputStyles(isDark, themeID, reducedMotion))
	input.Focus()
	q.input = input
	q.hasInput = true
	q.SetWidth(q.width)
}

func (q *QuestionPrompt) UpdateInput(msg tea.KeyPressMsg) {
	if q == nil || !q.hasInput {
		return
	}
	q.input, _ = q.input.Update(msg)
}

func (q QuestionPrompt) Text() string {
	if !q.hasInput {
		return ""
	}
	return q.input.Value()
}

func (q QuestionPrompt) InputCursor() *tea.Cursor {
	if !q.hasInput {
		return nil
	}
	return q.input.Cursor()
}

func (q *QuestionPrompt) Move(delta int) bool {
	if q == nil || delta == 0 || len(q.Choices) == 0 {
		return false
	}
	if q.Selected < 0 {
		if delta < 0 {
			q.Selected = len(q.Choices) - 1
		} else {
			q.Selected = 0
		}
		return true
	}
	q.Selected = (q.Selected + delta + len(q.Choices)) % len(q.Choices)
	return true
}

func (q *QuestionPrompt) Choice() (QuestionChoice, bool) {
	if q == nil || q.Selected < 0 || q.Selected >= len(q.Choices) {
		return QuestionChoice{}, false
	}
	return q.Choices[q.Selected], true
}

func (q QuestionPrompt) contentWidth() int {
	if q.width > 0 {
		return max(1, pickerListWidth(q.width))
	}
	return 40
}

func (q QuestionPrompt) Render() string {
	width := q.contentWidth()
	var b strings.Builder
	title := q.Title
	if title == "" {
		title = "Question"
	}
	b.WriteString(q.styles.OverlayTitle.Render(truncateDisplayWithGlyphProfile(title, width, q.glyphProfile)))
	b.WriteString("\n")
	prompt := strings.TrimSpace(q.Prompt)
	if prompt != "" {
		b.WriteString(q.styles.StatusText.Render(limitWrappedRows(prompt, width, 3, q.glyphProfile)))
		b.WriteString("\n")
	}
	if q.Kind == QuestionKindFreeText && q.hasInput {
		b.WriteString(q.input.View())
	} else {
		b.WriteString(q.RenderChoices(width))
	}
	if q.Selected < 0 && len(q.Choices) > 0 {
		b.WriteString("\n")
		b.WriteString(q.styles.OverlayDim.Render(
			truncateDisplayWithGlyphProfile("No option selected", width, q.glyphProfile),
		))
	}
	if detail := q.RenderDetail(width); detail != "" {
		b.WriteString("\n")
		b.WriteString(q.styles.OverlayAccent.Render("Detail"))
		b.WriteString("\n")
		b.WriteString(detail)
	}
	if strings.TrimSpace(q.Notice) != "" {
		b.WriteString("\n")
		b.WriteString(q.styles.OverlayDim.Render(truncateDisplayWithGlyphProfile(q.Notice, width, q.glyphProfile)))
	}
	return b.String()
}

func (q QuestionPrompt) RenderChoices(width int) string {
	if len(q.Choices) == 0 {
		return q.styles.OverlayDim.Render("No options")
	}
	start := 0
	if q.Selected >= questionChoiceWindow {
		start = q.Selected - questionChoiceWindow + 1
	}
	end := min(len(q.Choices), start+questionChoiceWindow)
	rows := make([]string, 0, end-start)
	glyphs := glyphSet(q.glyphProfile)
	for index := start; index < end; index++ {
		marker := glyphs.Unselected
		style := q.styles.StatusText
		if index == q.Selected {
			marker = glyphs.Selected
			style = q.styles.FocusIndicator
		}
		label := q.Choices[index].Label
		if q.Choices[index].ID != "" && q.Choices[index].ID != label {
			label = q.Choices[index].ID + q.separator() + label
		}
		rows = append(rows, style.Render(truncateDisplayWithGlyphProfile(marker+" "+label, width, q.glyphProfile)))
	}
	return strings.Join(rows, "\n")
}

func (q QuestionPrompt) RenderDetail(width int) string {
	choice, ok := q.Choice()
	if !ok || strings.TrimSpace(choice.Detail) == "" {
		return ""
	}
	return q.styles.StatusText.Render(limitWrappedRows(choice.Detail, width, 3, q.glyphProfile))
}

func (q QuestionPrompt) separator() string {
	if q.sep != "" {
		return q.sep
	}
	return glyphSeparator(q.glyphProfile)
}

func (q QuestionPrompt) Footer() string {
	width := q.contentWidth()
	sep := strings.TrimSpace(q.separator())
	move := "↑/↓"
	if q.glyphProfile == GlyphASCII {
		move = "j/k"
	}
	return truncateDisplayWithGlyphProfile(
		move+" choose "+sep+" enter confirm "+sep+" esc dismiss",
		width,
		q.glyphProfile,
	)
}

// dialogFrame paints a centered-overlay box sized to the shared picker tiers.
func dialogFrame(styles Styles, content, footer string, terminalWidth int, profiles ...GlyphProfile) string {
	return dialogFrameAt(styles, content, footer, pickerContentWidth(terminalWidth)+2, profiles...)
}

// dialogFrameAt paints a box whose Lip Gloss Width is the total outer width
// (border and padding live inside it).
func dialogFrameAt(styles Styles, content, footer string, outerWidth int, profiles ...GlyphProfile) string {
	profile := resolveGlyphProfile(profiles...)
	content = strings.TrimRight(content, "\n")
	if footer != "" {
		content += "\n" + styles.OverlayDim.Render(footer)
	}
	return lipgloss.NewStyle().
		Border(borderForGlyphProfile(profile)).
		BorderForeground(styles.OverlayBorder).
		Padding(0, 1).
		Width(max(4, outerWidth)).
		Render(content)
}

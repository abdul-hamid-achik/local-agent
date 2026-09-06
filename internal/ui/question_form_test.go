package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestQuestionPromptYesNoDefaultsToNo(t *testing.T) {
	styles := NewStyles(true, defaultThemeID)
	prompt := newQuestionPrompt(QuestionKindYesNo, "Continue?", "Overwrite the file?", []QuestionChoice{
		{ID: "yes", Label: "Yes"},
		{ID: "no", Label: "No"},
	}, styles)
	choice, ok := prompt.Choice()
	if !ok || choice.ID != "no" {
		t.Fatalf("yes/no default = %#v ok=%v, want no", choice, ok)
	}
}

func TestQuestionPromptSingleStartsUnselected(t *testing.T) {
	styles := NewStyles(true, defaultThemeID)
	prompt := newQuestionPrompt(QuestionKindSingle, "Rollout", "Which path?", []QuestionChoice{
		{ID: "canary", Label: "Canary", Detail: "Ship 5% first"},
		{ID: "all", Label: "All at once", Detail: "Everyone now"},
	}, styles)
	if _, ok := prompt.Choice(); ok {
		t.Fatal("single-choice confirmed an implicit first option")
	}
	if !prompt.Move(1) {
		t.Fatal("move did not select the first option")
	}
	choice, ok := prompt.Choice()
	if !ok || choice.ID != "canary" {
		t.Fatalf("after move = %#v", choice)
	}
	plain := ansi.Strip(prompt.Render())
	for _, want := range []string{"Rollout", "Which path?", "Canary", "Ship 5% first"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("question omitted %q:\n%s", want, plain)
		}
	}
}

func TestDialogFrameFitsPickerWidth(t *testing.T) {
	styles := NewStyles(true, defaultThemeID)
	frame := dialogFrame(styles, "body", "esc dismiss", 80)
	for i, line := range strings.Split(frame, "\n") {
		if got := lipgloss.Width(line); got > 80 {
			t.Fatalf("line %d width = %d:\n%s", i, got, line)
		}
	}
}

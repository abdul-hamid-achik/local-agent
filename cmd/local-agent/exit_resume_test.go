package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/ui"
)

type exitResumeTestModel struct {
	info ui.SessionResumeInfo
	ok   bool
}

func (m *exitResumeTestModel) Init() tea.Cmd { return nil }

func (m *exitResumeTestModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

func (m *exitResumeTestModel) View() tea.View { return tea.NewView("") }

func (m *exitResumeTestModel) SessionResumeInfo() (ui.SessionResumeInfo, bool) {
	return m.info, m.ok
}

func TestWriteSessionResumeMessageUsesCanonicalShortHandle(t *testing.T) {
	var output bytes.Buffer
	writeSessionResumeMessage(&output, &exitResumeTestModel{
		info: ui.SessionResumeInfo{Handle: "s42", Title: "Polish transcript UX"},
		ok:   true,
	}, nil)

	if got, want := output.String(), "\nSession S42 · Polish transcript UX\nResume this session with:\n  local-agent --resume S42\n"; got != want {
		t.Fatalf("resume message = %q, want %q", got, want)
	}
}

func TestWriteSessionResumeMessageSanitizesTitleOutsideCommand(t *testing.T) {
	var output bytes.Buffer
	writeSessionResumeMessage(&output, &exitResumeTestModel{
		info: ui.SessionResumeInfo{
			Handle: "S7",
			Title:  "Review\x1b]0;owned\x07\nthen deploy\u202e",
		},
		ok: true,
	}, nil)

	got := output.String()
	if !strings.Contains(got, "Session S7 · Review then deploy") {
		t.Fatalf("sanitized session label = %q", got)
	}
	if strings.Contains(got, "owned") || strings.Contains(got, "\x1b]") || strings.Contains(got, "\u202e") {
		t.Fatalf("unsafe title content survived: %q", got)
	}
	if strings.Count(got, "local-agent --resume S7") != 1 {
		t.Fatalf("canonical command changed: %q", got)
	}
}

func TestWriteSessionResumeMessageSuppressesUnavailableOrFailedExit(t *testing.T) {
	tests := []struct {
		name  string
		model tea.Model
		err   error
	}{
		{name: "no final model"},
		{name: "no durable session", model: &exitResumeTestModel{}},
		{name: "invalid handle", model: &exitResumeTestModel{info: ui.SessionResumeInfo{Handle: "S42\nrm -rf /"}, ok: true}},
		{name: "tui error", model: &exitResumeTestModel{info: ui.SessionResumeInfo{Handle: "S42"}, ok: true}, err: errors.New("terminal restore failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			writeSessionResumeMessage(&output, test.model, test.err)
			if output.Len() != 0 {
				t.Fatalf("unexpected resume message %q", output.String())
			}
		})
	}
}

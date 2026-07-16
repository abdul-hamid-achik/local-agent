package main

import (
	"bytes"
	"errors"
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
		info: ui.SessionResumeInfo{Handle: "s42"},
		ok:   true,
	}, nil)

	if got, want := output.String(), "\nResume this session with:\n  local-agent --resume S42\n"; got != want {
		t.Fatalf("resume message = %q, want %q", got, want)
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

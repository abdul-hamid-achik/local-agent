package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/local-agent/internal/sessionref"
	"github.com/abdul-hamid-achik/local-agent/internal/ui"
)

type sessionResumeInfoSource interface {
	SessionResumeInfo() (ui.SessionResumeInfo, bool)
}

// writeSessionResumeMessage runs only after Bubble Tea has returned and
// restored the terminal. Validate and reformat the handle at this final output
// boundary so user-derived text can never become part of the command.
func writeSessionResumeMessage(writer io.Writer, finalModel tea.Model, runErr error) {
	if writer == nil || runErr != nil || finalModel == nil {
		return
	}
	source, ok := finalModel.(sessionResumeInfoSource)
	if !ok {
		return
	}
	info, ok := source.SessionResumeInfo()
	if !ok {
		return
	}
	id, err := sessionref.Parse(strings.TrimSpace(info.Handle))
	if err != nil {
		return
	}
	handle := sessionref.Format(id)
	if handle == "" {
		return
	}
	label := "Session " + handle
	if title := sanitizeExitSessionTitle(info.Title); title != "" {
		label += " · " + title
	}
	_, _ = fmt.Fprintf(writer, "\n%s\nResume this session with:\n  local-agent --resume %s\n", label, handle)
}

func sanitizeExitSessionTitle(title string) string {
	title = ansi.Strip(strings.ToValidUTF8(title, "�"))
	title = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) || isExitBidiControl(value) {
			return ' '
		}
		return value
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	if runes := []rune(title); len(runes) > 72 {
		title = string(runes[:69]) + "..."
	}
	return title
}

func isExitBidiControl(value rune) bool {
	switch value {
	case '\u061c', '\u200e', '\u200f',
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	default:
		return false
	}
}

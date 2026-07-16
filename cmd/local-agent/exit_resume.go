package main

import (
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

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
	_, _ = fmt.Fprintf(writer, "\nResume this session with:\n  local-agent --resume %s\n", handle)
}

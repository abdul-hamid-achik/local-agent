package ui

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type blockingExecCommand struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (c blockingExecCommand) Run() error {
	close(c.started)
	<-c.release
	return nil
}

func (blockingExecCommand) SetStdin(io.Reader)  {}
func (blockingExecCommand) SetStdout(io.Writer) {}
func (blockingExecCommand) SetStderr(io.Writer) {}

type execOwnershipModel struct {
	command tea.ExecCommand
}

func (m execOwnershipModel) Init() tea.Cmd {
	return tea.Exec(m.command, nil)
}

func (m execOwnershipModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (m execOwnershipModel) View() tea.View { return tea.NewView("") }

// openExternalEditor uses tea.ExecProcess, which goes through the same
// synchronous tea.Exec path exercised here. Bubble Tea releases the terminal,
// blocks its event loop in ExecCommand.Run, then restores the terminal before
// it can process a normal quit. Therefore a graceful local-agent exit cannot
// leave an editor child running; the second OS signal remains the emergency
// escape for a wedged interactive program.
func TestBubbleTeaOwnsInteractiveEditorUntilItReturns(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	p := tea.NewProgram(
		execOwnershipModel{command: blockingExecCommand{started: started, release: release}},
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	)
	runDone := make(chan error, 1)
	go func() {
		_, err := p.Run()
		runDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("interactive command did not start")
	}

	quitReturned := make(chan struct{})
	go func() {
		p.Quit()
		close(quitReturned)
	}()
	select {
	case err := <-runDone:
		t.Fatalf("program exited while interactive child was running: %v", err)
	case <-quitReturned:
		t.Fatal("graceful quit bypassed the synchronously owned interactive child")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("program exit: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("program did not exit after interactive child returned")
	}
}

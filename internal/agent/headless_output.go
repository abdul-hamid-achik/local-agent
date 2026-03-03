package agent

import (
	"fmt"
	"io"
	"os"
	"time"
)

// HeadlessOutput implements the Output interface for non-interactive / pipe mode.
// Text is written to stdout; tool calls, system messages, and errors go to stderr.
type HeadlessOutput struct {
	stdout io.Writer
	stderr io.Writer
}

// NewHeadlessOutput creates a HeadlessOutput that writes text to os.Stdout
// and diagnostics to os.Stderr.
func NewHeadlessOutput() *HeadlessOutput {
	return &HeadlessOutput{
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
}

// newHeadlessOutput creates a HeadlessOutput with custom writers (for testing).
func newHeadlessOutput(stdout, stderr io.Writer) *HeadlessOutput {
	return &HeadlessOutput{
		stdout: stdout,
		stderr: stderr,
	}
}

// StreamText writes incremental text content to stdout.
func (h *HeadlessOutput) StreamText(text string) {
	fmt.Fprint(h.stdout, text)
}

// StreamDone writes a trailing newline to ensure output is terminated.
func (h *HeadlessOutput) StreamDone(evalCount, promptTokens int) {
	fmt.Fprintln(h.stdout)
}

// ToolCallStart writes a brief tool invocation notice to stderr.
func (h *HeadlessOutput) ToolCallStart(name string, args map[string]any) {
	fmt.Fprintf(h.stderr, "→ %s %s\n", name, FormatToolArgs(args))
}

// ToolCallResult writes the tool result summary to stderr.
func (h *HeadlessOutput) ToolCallResult(name string, result string, isError bool, duration time.Duration) {
	status := "ok"
	if isError {
		status = "ERROR"
	}
	// Truncate long results for stderr display.
	display := result
	if len(display) > 200 {
		display = display[:197] + "..."
	}
	fmt.Fprintf(h.stderr, "← %s [%s %s] %s\n", name, status, duration.Round(time.Millisecond), display)
}

// SystemMessage writes a system message to stderr.
func (h *HeadlessOutput) SystemMessage(msg string) {
	fmt.Fprintf(h.stderr, "[system] %s\n", msg)
}

// Error writes an error message to stderr.
func (h *HeadlessOutput) Error(msg string) {
	fmt.Fprintf(h.stderr, "[error] %s\n", msg)
}

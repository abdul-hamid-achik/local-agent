package agent

import "time"

// Output is the interface the agent uses to stream results to the UI.
type Output interface {
	// StreamText sends incremental text content.
	StreamText(text string)

	// StreamDone signals that the current response is complete.
	StreamDone(evalCount, promptTokens int)

	// ToolCallStart signals the beginning of a tool invocation.
	ToolCallStart(name string, args map[string]any)

	// ToolCallResult delivers the result of a tool invocation.
	ToolCallResult(name string, result string, isError bool, duration time.Duration)

	// SystemMessage displays a system-level message to the user.
	SystemMessage(msg string)

	// Error reports a non-fatal error to the user.
	Error(msg string)
}

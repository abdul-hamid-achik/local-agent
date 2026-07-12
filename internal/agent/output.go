package agent

import "time"

// Output is the interface the agent uses to stream results to the UI.
type Output interface {
	// StreamText sends incremental text content.
	StreamText(text string)

	// StreamReasoning sends provider-native thinking separately from answer text.
	StreamReasoning(text string)

	// StreamDone reports evaluation usage for the current provider request. For
	// a hard-capped request whose terminal provider receipt is missing or
	// untrustworthy, evalCount may be the conservative unaccounted reservation;
	// callers must include every report in durable turn usage even when the turn
	// later returns an error.
	StreamDone(evalCount, promptTokens int)

	// ToolCallStart signals the beginning of a tool invocation.
	ToolCallStart(callID, name string, args map[string]any)

	// ToolCallResult delivers the result of a tool invocation.
	ToolCallResult(callID, name string, result string, isError bool, duration time.Duration)

	// SystemMessage displays a system-level message to the user.
	SystemMessage(msg string)

	// Error reports a non-fatal error to the user.
	Error(msg string)
}

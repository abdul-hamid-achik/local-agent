package agent

import (
	"encoding/json"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
)

// SemanticToolOutput is an optional richer output contract. Existing headless
// and test outputs keep the small Output interface; interactive projections can
// receive a host-derived semantic receipt without ever receiving raw structured
// MCP content that may contain secrets.
type SemanticToolOutput interface {
	ToolCallSemanticResult(callID, name, result string, isError bool, duration time.Duration, projection ecosystem.ToolProjection)
}

func emitSemanticToolResult(
	out Output,
	callID, name string,
	result string,
	structured json.RawMessage,
	toolError, transportError bool,
	duration time.Duration,
	projection ecosystem.ToolProjection,
) {
	if semantic, ok := out.(SemanticToolOutput); ok {
		displayResult := result
		if len(structured) > 0 {
			// Structured calls render the same bounded semantic digest that the
			// model and ledger receive. Raw structured fields never cross into the
			// UI, even when a server duplicated them in TextContent.
			displayResult = ecosystem.SafeReceiptText(projection)
		}
		semantic.ToolCallSemanticResult(callID, name, displayResult, toolError || transportError, duration, projection)
		return
	}
	out.ToolCallResult(callID, name, result, toolError || transportError, duration)
}

func projectSemanticToolReceipt(
	name string,
	args map[string]any,
	text string,
	structured, errorMeta json.RawMessage,
	transportError, toolError, trustedLocal bool,
) ecosystem.ToolProjection {
	return ecosystem.ProjectReceipt(ecosystem.ProjectToolCall(name, args), ecosystem.RawReceipt{
		Text:           text,
		Structured:     structured,
		ErrorMeta:      errorMeta,
		TransportError: transportError,
		ToolError:      toolError,
		TrustedLocal:   trustedLocal,
	})
}

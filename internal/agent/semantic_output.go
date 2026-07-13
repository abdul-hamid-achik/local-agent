package agent

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

const (
	maxTransientCortexResultBytes = 32 * 1024
	maxTransientSchemaBytes       = 16 * 1024
	maxTransientSchemaDepth       = 8
	maxTransientSchemaProperties  = 64
	maxTransientSchemaEnumValues  = 32
	maxTransientSchemaNameBytes   = 96
	maxTransientSchemaScalarBytes = 128
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

// semanticToolContents separates active provider context from durable/display
// context. Only a validated result from an exact host-trusted local contract
// may differ; every other typed MCP receipt stays as a bounded semantic
// projection on both paths.
func (a *Agent) semanticToolContents(call llm.ToolCall, projection ecosystem.ToolProjection, rawResult string, structured json.RawMessage, toolError bool) (modelResult, durableResult string) {
	if len(structured) == 0 {
		return rawResult, rawResult
	}
	durableResult = ecosystem.SafeReceiptText(projection)
	modelResult = durableResult
	if _, trusted := a.trustedMCPContract(call); trusted && projection.Operation == "mcphub_get_result" && !toolError {
		if transient, ok := ecosystem.TransientModelContent(projection, ecosystem.RawReceipt{Structured: structured}); ok {
			return transient, durableResult
		}
	}
	if transient, ok := a.trustedMCPHubDescribeContent(call, projection, structured, toolError); ok {
		return transient, durableResult
	}
	if transient, ok := a.trustedCortexTransientContent(call, projection, rawResult, toolError); ok {
		modelResult = transient
	}
	return modelResult, durableResult
}

func (a *Agent) trustedMCPHubDescribeContent(call llm.ToolCall, projection ecosystem.ToolProjection, structured json.RawMessage, toolError bool) (string, bool) {
	if toolError || projection.Operation != "mcphub_describe_tool" || projection.Digest == nil ||
		projection.Digest.Kind != ecosystem.DigestMCPHubDescribe || projection.Domain != ecosystem.DomainSucceeded {
		return "", false
	}
	parts := strings.Split(call.Name, "__")
	if len(parts) != 2 || parts[1] != "mcphub_describe_tool" || a.trustedMCPImplementation(parts[0]) != trustedMCPHub {
		return "", false
	}
	var envelope struct {
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if json.Unmarshal(structured, &envelope) != nil || len(envelope.InputSchema) == 0 {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.InputSchema))
	decoder.UseNumber()
	var rawSchema any
	if decoder.Decode(&rawSchema) != nil {
		return "", false
	}
	schema, ok := sanitizeTransientJSONSchema(rawSchema, 0)
	if !ok {
		return "", false
	}
	payload, err := json.Marshal(map[string]any{
		"tool":         projection.Digest.Target,
		"input_schema": schema,
	})
	if err != nil || len(payload) > maxTransientSchemaBytes {
		return "", false
	}
	return "MCPHub tool schema (transient; prose removed; not saved)\n" + string(payload), true
}

func sanitizeTransientJSONSchema(value any, depth int) (any, bool) {
	if depth > maxTransientSchemaDepth {
		return nil, false
	}
	if boolean, ok := value.(bool); ok {
		return boolean, true
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	result := make(map[string]any, 6)
	if value, ok := sanitizeSchemaType(object["type"]); ok {
		result["type"] = value
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		keys := make([]string, 0, len(properties))
		for key := range properties {
			if validSchemaName(key) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if len(keys) > maxTransientSchemaProperties {
			keys = keys[:maxTransientSchemaProperties]
		}
		safeProperties := make(map[string]any, len(keys))
		for _, key := range keys {
			if child, childOK := sanitizeTransientJSONSchema(properties[key], depth+1); childOK {
				safeProperties[key] = child
			}
		}
		if len(safeProperties) > 0 {
			result["properties"] = safeProperties
		}
	}
	if required, ok := sanitizeSchemaNames(object["required"]); ok {
		result["required"] = required
	}
	if items, exists := object["items"]; exists {
		if safeItems, itemsOK := sanitizeTransientJSONSchema(items, depth+1); itemsOK {
			result["items"] = safeItems
		}
	}
	if values, ok := sanitizeSchemaEnum(object["enum"]); ok {
		result["enum"] = values
	}
	if additional, exists := object["additionalProperties"]; exists {
		if safeAdditional, additionalOK := sanitizeTransientJSONSchema(additional, depth+1); additionalOK {
			result["additionalProperties"] = safeAdditional
		}
	}
	return result, true
}

func sanitizeSchemaType(value any) (any, bool) {
	valid := func(candidate string) bool {
		switch candidate {
		case "null", "boolean", "object", "array", "number", "string", "integer":
			return true
		default:
			return false
		}
	}
	switch typed := value.(type) {
	case string:
		if valid(typed) {
			return typed, true
		}
	case []any:
		result := make([]string, 0, min(len(typed), 7))
		seen := make(map[string]struct{}, 7)
		for _, item := range typed {
			candidate, ok := item.(string)
			if !ok || !valid(candidate) {
				continue
			}
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			result = append(result, candidate)
		}
		if len(result) > 0 {
			return result, true
		}
	}
	return nil, false
}

func sanitizeSchemaNames(value any) ([]string, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, min(len(values), maxTransientSchemaProperties))
	seen := make(map[string]struct{}, cap(result))
	for _, value := range values {
		name, ok := value.(string)
		if !ok || !validSchemaName(name) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
		if len(result) == maxTransientSchemaProperties {
			break
		}
	}
	return result, true
}

func sanitizeSchemaEnum(value any) ([]any, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]any, 0, min(len(values), maxTransientSchemaEnumValues))
	for _, value := range values {
		switch typed := value.(type) {
		case nil, bool, json.Number:
			result = append(result, typed)
		case string:
			if len(typed) <= maxTransientSchemaScalarBytes && validSchemaScalarString(typed) {
				result = append(result, typed)
			}
		}
		if len(result) == maxTransientSchemaEnumValues {
			break
		}
	}
	return result, true
}

func validSchemaName(value string) bool {
	return value != "" && len(value) <= maxTransientSchemaNameBytes && strings.TrimSpace(value) == value &&
		utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validSchemaScalarString(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n") && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (a *Agent) trustedCortexTransientContent(call llm.ToolCall, projection ecosystem.ToolProjection, redactedResult string, toolError bool) (string, bool) {
	if toolError || projection.Specialist != "cortex" {
		return "", false
	}
	if _, trusted := a.trustedMCPContract(call); !trusted {
		return "", false
	}
	// Use the post-hook text copy, not StructuredContent, so host result
	// redaction remains authoritative before anything reaches the provider.
	document := bytes.TrimSpace([]byte(redactedResult))
	if len(document) == 0 || len(document) > maxTransientCortexResultBytes || !utf8.Valid(document) ||
		(document[0] != '{' && document[0] != '[') || !json.Valid(document) {
		return "", false
	}
	if document[0] == '{' {
		var state struct {
			OK    *bool           `json:"ok"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(document, &state) != nil || (state.OK != nil && !*state.OK) || rawJSONValuePresent(state.Error) {
			return "", false
		}
	}
	return "Cortex result (transient; not saved)\n" + string(document), true
}

func rawJSONValuePresent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte(`""`))
}

func (a *Agent) settleTransientMessages() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	settled := 0
	for index := range a.messages {
		if a.messages[index].DurableContent == "" {
			continue
		}
		a.messages[index].Content = a.messages[index].DurableContent
		a.messages[index].DurableContent = ""
		settled++
	}
	return settled
}

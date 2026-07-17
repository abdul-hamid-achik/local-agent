package agent

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func boundedEvalLimit(remaining int64) int {
	if remaining <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if remaining > maxInt {
		return int(maxInt)
	}
	return int(remaining)
}

func contextReservedEvalLimit(remaining int64, promptTokens, numCtx int) int {
	if remaining <= 0 || numCtx <= 0 || promptTokens < 0 {
		return 0
	}
	reserve := numCtx / 4
	if available := numCtx - promptTokens; available < reserve {
		reserve = available
	}
	if reserve <= 0 {
		return 0
	}
	return min(boundedEvalLimit(remaining), reserve)
}

// chargeUnknownEvalReservation propagates a fail-closed usage receipt when a
// capped provider request does not return trustworthy terminal accounting. The
// exact number of generated tokens is unknowable in that failure mode, so the
// host must consume the unaccounted portion of the per-request reservation
// before it may admit another goal turn.
func chargeUnknownEvalReservation(out Output, requestLimit int, reported int64) int64 {
	if requestLimit <= 0 {
		return 0
	}
	reserved := int64(requestLimit)
	if reported >= reserved {
		return 0
	}
	missing := reserved - max(int64(0), reported)
	out.StreamDone(int(missing), 0)
	return missing
}

func capToolResultForContext(result string, numCtx int) string {
	if numCtx <= 0 {
		return result
	}
	limit := numCtx
	if limit < 2*1024 {
		limit = 2 * 1024
	}
	if limit > 96*1024 {
		limit = 96 * 1024
	}
	const marker = "\n... [tool result truncated to protect model context]"
	if len(result) <= limit {
		return result
	}
	cut := limit - len(marker)
	for cut > 0 && !utf8.ValidString(result[:cut]) {
		cut--
	}
	return result[:cut] + marker
}

func (a *Agent) estimatePromptTokens(system string, tools []llm.ToolDef) int {
	a.mu.RLock()
	messageTokens := estimateMessagesPromptTokens(a.messages)
	a.mu.RUnlock()
	return estimateHostPromptTokens(system, tools) + messageTokens
}

func estimateHostPromptTokens(system string, tools []llm.ToolDef) int {
	tokens := estimateTextPromptTokens(system)
	if encoded, err := json.Marshal(tools); err == nil {
		tokens += estimateTextPromptTokens(string(encoded))
	}
	return tokens + 1
}

func estimateMessagesPromptTokens(messages []llm.Message) int {
	tokens := 0
	for _, message := range messages {
		// Role/framing metadata consumes a few provider tokens even when the
		// visible fields are empty.
		tokens += 4
		tokens += estimateTextPromptTokens(message.Content)
		tokens += estimateTextPromptTokens(message.ToolName)
		tokens += estimateTextPromptTokens(message.ToolCallID)
		if encoded, err := json.Marshal(message.ToolCalls); err == nil {
			tokens += estimateTextPromptTokens(string(encoded))
		}
		for _, image := range message.Images {
			tokens += estimateImagePromptTokens(image)
		}
	}
	return tokens
}

// estimateTextPromptTokens uses the model-agnostic chars/4 heuristic for ASCII
// while charging non-ASCII input at the tokenizer byte-fallback upper bound.
// It is an admission estimate rather than an exact tokenizer; the separate
// generation reserve and image-patch budget provide the remaining safety margin.
func estimateTextPromptTokens(text string) int {
	if text == "" {
		return 0
	}
	asciiBytes := 0
	nonASCIIBytes := 0
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r < utf8.RuneSelf {
			asciiBytes++
		} else {
			nonASCIIBytes += size
		}
		text = text[size:]
	}
	return (asciiBytes+3)/4 + nonASCIIBytes
}

// estimateImagePromptTokens reserves vision-patch context before raw image
// bytes cross the provider boundary. Referenced images have verified dimensions;
// legacy transient images without them receive a fixed conservative reserve.
func estimateImagePromptTokens(image llm.ImageData) int {
	const (
		visionPatchSize       = 28
		minimumVisionTokens   = 256
		unknownImageTokenCost = 1_024
	)
	if image.Width <= 0 || image.Height <= 0 {
		return unknownImageTokenCost
	}
	patchesWide := (image.Width + visionPatchSize - 1) / visionPatchSize
	patchesHigh := (image.Height + visionPatchSize - 1) / visionPatchSize
	return max(minimumVisionTokens, patchesWide*patchesHigh)
}

func filterToolDefsByName(defs []llm.ToolDef, allowed map[string]struct{}) []llm.ToolDef {
	if len(allowed) == 0 {
		return nil
	}

	filtered := make([]llm.ToolDef, 0, len(defs))
	for _, def := range defs {
		if _, ok := allowed[def.Name]; ok {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

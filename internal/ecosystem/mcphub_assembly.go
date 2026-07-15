package ecosystem

import (
	"bytes"
	"encoding/json"
	"sync"
)

const (
	maxMCPHubAssembledResultBytes = 4 * 1024 * 1024
	maxMCPHubActiveAssemblies     = 4
)

type mcphubResultAssembly struct {
	server   string
	tool     string
	total    int64
	next     int64
	sequence uint64
	data     []byte
}

// MCPHubResultAssembler keeps a small, turn-scoped set of exact stored-result
// pages until the complete serialized CallToolResult can be passed through the
// same downstream parser as a direct result. Raw bytes never leave this type
// and Reset zeroes any partial data before releasing it.
type MCPHubResultAssembler struct {
	mu       sync.Mutex
	entries  map[string]*mcphubResultAssembly
	sequence uint64
}

// NewMCPHubResultAssembler creates an empty bounded result assembler.
func NewMCPHubResultAssembler() *MCPHubResultAssembler {
	return &MCPHubResultAssembler{entries: make(map[string]*mcphubResultAssembly)}
}

// Observe records a stored-result receipt or one validated result page. Page
// order, total size, call identity, and the original downstream route must all
// remain exact before a completed result can replace retrieval semantics with
// downstream domain semantics.
func (a *MCPHubResultAssembler) Observe(projection ToolProjection, receipt RawReceipt) ToolProjection {
	if a == nil || projection.Digest == nil {
		return projection
	}
	switch projection.Digest.Kind {
	case DigestMCPHubStored:
		a.remember(projection)
		return projection
	case DigestMCPHubPage:
		return a.appendPage(projection, receipt)
	case DigestMCPHubUnavailable, DigestMCPHubCursorOutOfRange, DigestMCPHubError:
		a.discard(projection.Route.CallID)
		return projection
	default:
		return projection
	}
}

func (a *MCPHubResultAssembler) discard(callID string) {
	if callID == "" {
		return
	}
	a.mu.Lock()
	a.dropLocked(callID)
	a.mu.Unlock()
}

// Reset discards every partial result and zeroes its in-memory payload.
func (a *MCPHubResultAssembler) Reset() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for callID := range a.entries {
		a.dropLocked(callID)
	}
}

func (a *MCPHubResultAssembler) remember(projection ToolProjection) {
	digest := projection.Digest
	callID := projection.Route.CallID
	if digest == nil || callID == "" || projection.Route.Server == "" || projection.Route.Tool == "" ||
		digest.OriginalBytes <= 0 || digest.OriginalBytes > maxMCPHubAssembledResultBytes {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.dropLocked(callID)
	if len(a.entries) >= maxMCPHubActiveAssemblies {
		a.dropOldestLocked()
	}
	a.sequence++
	a.entries[callID] = &mcphubResultAssembly{
		server: projection.Route.Server, tool: projection.Route.Tool,
		total: digest.OriginalBytes, sequence: a.sequence,
		data: make([]byte, 0, min(int(digest.OriginalBytes), maxMCPHubResultPageBytes)),
	}
}

func (a *MCPHubResultAssembler) appendPage(projection ToolProjection, receipt RawReceipt) ToolProjection {
	document, ok := receiptDocument(receipt)
	if !ok {
		return a.fail(projection)
	}
	var envelope mcphubResultPageEnvelope
	if json.Unmarshal(document, &envelope) != nil {
		return a.fail(projection)
	}
	page, ok := decodeMCPHubResultPage(envelope)
	if !ok || projection.Route.CallID == "" || envelope.CallID != projection.Route.CallID ||
		envelope.Cursor == nil || envelope.NextCursor == nil || envelope.Done == nil || envelope.TotalBytes == nil ||
		projection.Digest.Cursor != *envelope.Cursor || projection.Digest.NextCursor != *envelope.NextCursor ||
		projection.Digest.TotalBytes != *envelope.TotalBytes || projection.Digest.Done != *envelope.Done {
		return a.fail(projection)
	}

	a.mu.Lock()
	entry := a.entries[projection.Route.CallID]
	if entry == nil {
		a.mu.Unlock()
		return assemblyAttention(projection)
	}
	if entry.total != *envelope.TotalBytes || entry.next != *envelope.Cursor ||
		*envelope.NextCursor > maxMCPHubAssembledResultBytes {
		a.dropLocked(projection.Route.CallID)
		a.mu.Unlock()
		return assemblyFailure(projection)
	}
	entry.data = append(entry.data, page...)
	entry.next = *envelope.NextCursor
	if !*envelope.Done {
		a.mu.Unlock()
		return assemblyAttention(projection)
	}
	if entry.next != entry.total || int64(len(entry.data)) != entry.total {
		a.dropLocked(projection.Route.CallID)
		a.mu.Unlock()
		return assemblyFailure(projection)
	}
	payload := entry.data
	server, tool := entry.server, entry.tool
	delete(a.entries, projection.Route.CallID)
	a.mu.Unlock()
	defer clear(payload)

	domain, evidence, typed := reparseStoredMCPHubResult(server, tool, payload)
	if !typed {
		projection.Domain = DomainUnknown
		projection.DomainTyped = false
		projection.Evidence = EvidenceNone
		return projection.Normalize()
	}
	projection.Domain = domain
	projection.DomainTyped = true
	projection.Evidence = evidence
	return projection.Normalize()
}

func (a *MCPHubResultAssembler) fail(projection ToolProjection) ToolProjection {
	a.discard(projection.Route.CallID)
	return assemblyFailure(projection)
}

func (a *MCPHubResultAssembler) dropOldestLocked() {
	oldestID := ""
	oldestSequence := ^uint64(0)
	for callID, entry := range a.entries {
		if entry.sequence < oldestSequence {
			oldestID, oldestSequence = callID, entry.sequence
		}
	}
	if oldestID != "" {
		a.dropLocked(oldestID)
	}
}

func (a *MCPHubResultAssembler) dropLocked(callID string) {
	if entry := a.entries[callID]; entry != nil {
		clear(entry.data)
		delete(a.entries, callID)
	}
}

func assemblyAttention(projection ToolProjection) ToolProjection {
	projection.Domain = DomainAttention
	projection.DomainTyped = true
	projection.Evidence = EvidenceNone
	return projection.Normalize()
}

func assemblyFailure(projection ToolProjection) ToolProjection {
	projection.Domain = DomainFailed
	projection.DomainTyped = true
	projection.Evidence = EvidenceNone
	return projection.Normalize()
}

func reparseStoredMCPHubResult(server, tool string, payload []byte) (DomainState, EvidenceState, bool) {
	receipt, ok := storedCallToolReceipt(payload)
	if !ok || server == "" || tool == "" {
		return DomainUnknown, EvidenceNone, false
	}
	parsed := ProjectReceipt(ProjectToolCall(server+"__"+tool, nil), receipt)
	if !parsed.DomainTyped || parsed.Domain == DomainUnknown {
		return DomainUnknown, EvidenceNone, false
	}
	return parsed.Domain, parsed.Evidence, true
}

func storedCallToolReceipt(payload []byte) (RawReceipt, bool) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || payload[0] != '{' || !json.Valid(payload) {
		return RawReceipt{}, false
	}
	var result struct {
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if json.Unmarshal(payload, &result) != nil {
		return RawReceipt{}, false
	}
	structured, ok := exactJSONDocument(result.StructuredContent)
	if !ok {
		return RawReceipt{}, false
	}
	return RawReceipt{Structured: structured, ToolError: result.IsError}, true
}

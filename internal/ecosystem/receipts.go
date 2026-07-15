package ecosystem

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MCPHub caps result pages at 8 KiB. Enforce the same parser bound so a
// malicious or incompatible gateway cannot smuggle an unbounded transient
// payload into the next provider request.
const maxMCPHubResultPageBytes = 8 * 1024

const (
	mcphubResolverContractVersion           = 1
	maxMCPHubCatalogRevisionBytes           = 128
	maxMCPHubResolverDocumentBytes          = 64 * 1024
	maxMCPHubResolverIdentifierBytes        = 256
	maxMCPHubResolverRequiredFields         = 48
	maxMCPHubResolverRequiredFieldBytes     = 128
	maxMCPHubResolverRequiredFieldNameBytes = 2048
	maxMCPHubResolverAlternatives           = 5
)

type mcphubResultPageEnvelope struct {
	Status     string `json:"status"`
	CallID     string `json:"callId"`
	MediaType  string `json:"mediaType"`
	Data       string `json:"data"`
	Cursor     *int64 `json:"cursor"`
	NextCursor *int64 `json:"nextCursor"`
	Done       *bool  `json:"done"`
	TotalBytes *int64 `json:"totalBytes"`
}

func receiptDocument(receipt RawReceipt) (json.RawMessage, bool) {
	// Any non-empty Structured value, including the host's null rejection
	// marker, proves that typed content was present. Never fall back to a
	// duplicated text block when that typed value is unsupported or oversized;
	// doing so would let arbitrary typed fields escape the parser boundary.
	if hasStructuredReceipt(receipt) {
		return exactJSONDocument(receipt.Structured)
	}
	return exactJSONDocument(json.RawMessage(strings.TrimSpace(receipt.Text)))
}

func hasStructuredReceipt(receipt RawReceipt) bool {
	return len(bytes.TrimSpace(receipt.Structured)) > 0
}

func exactJSONDocument(raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (raw[0] != '{' && raw[0] != '[') || !json.Valid(raw) {
		return nil, false
	}
	return append(json.RawMessage(nil), raw...), true
}

func projectMCPHubReceipt(projection ToolProjection, receipt RawReceipt) (ToolProjection, bool) {
	document, ok := receiptDocument(receipt)
	if !ok {
		return projection, false
	}

	if projection.Operation == "mcphub_get_result" {
		return projectMCPHubResultPage(projection, document), true
	}
	if projected, stored := projectMCPHubStoredReceipt(projection, document); stored {
		return projected, true
	}
	if !isMCPHubManagementOperation(projection.Operation) {
		return projection, false
	}
	if projection.Operation == "mcphub_resolve_tool" {
		return projectMCPHubResolve(projection, document), true
	}
	if jsonObjectHasValue(document, "error") {
		projection.Domain = DomainFailed
		projection.Evidence = EvidenceNone
		projection.Digest = &ReceiptDigest{Kind: DigestMCPHubError}
		return projection, true
	}

	switch projection.Operation {
	case "mcphub_list_servers":
		return projectMCPHubServers(projection, document)
	case "mcphub_search_tools":
		return projectMCPHubSearch(projection, document)
	case "mcphub_describe_tool":
		return projectMCPHubDescribe(projection, document)
	case "mcphub_stats":
		return projectMCPHubStats(projection, document)
	default:
		return projection, false
	}
}

func isMCPHubManagementOperation(operation string) bool {
	switch operation {
	case "mcphub_list_servers", "mcphub_search_tools", "mcphub_describe_tool",
		"mcphub_resolve_tool", "mcphub_stats":
		return true
	default:
		return false
	}
}

func projectMCPHubStoredReceipt(projection ToolProjection, document json.RawMessage) (ToolProjection, bool) {
	var envelope struct {
		Status        string `json:"status"`
		CallID        string `json:"callId"`
		Server        string `json:"server"`
		Tool          string `json:"tool"`
		OriginalBytes int64  `json:"originalBytes"`
		BudgetBytes   int64  `json:"budgetBytes"`
	}
	if json.Unmarshal(document, &envelope) != nil || envelope.Status != "stored" {
		return projection, false
	}
	callID := canonicalIdentifier(envelope.CallID)
	if callID == "" {
		projection.Domain = DomainUnknown
		projection.Evidence = EvidenceNone
		return projection, true
	}
	projection.Route.CallID = callID
	if projection.Route.Server == "" {
		projection.Route.Server = canonicalIdentifier(envelope.Server)
	}
	if projection.Route.Tool == "" {
		projection.Route.Tool = canonicalIdentifier(envelope.Tool)
	}
	projection.Domain = DomainAttention
	projection.Evidence = EvidenceNone
	projection.Digest = &ReceiptDigest{
		Kind: DigestMCPHubStored, OriginalBytes: envelope.OriginalBytes, BudgetBytes: envelope.BudgetBytes,
	}
	return projection.Normalize(), true
}

func projectMCPHubResultPage(projection ToolProjection, document json.RawMessage) ToolProjection {
	var envelope mcphubResultPageEnvelope
	if json.Unmarshal(document, &envelope) != nil {
		projection.Domain = DomainUnknown
		projection.Evidence = EvidenceNone
		return projection
	}

	// Retrieval is bound to the exact opaque identifier the model supplied. A
	// mismatched or absent response ID never replaces it and can never become a
	// follow-up fetch target.
	requestedID := canonicalIdentifier(projection.Route.CallID)
	receivedID := canonicalIdentifier(envelope.CallID)
	if requestedID == "" || receivedID == "" || requestedID != receivedID {
		projection.Domain = DomainFailed
		projection.Evidence = EvidenceNone
		projection.Digest = &ReceiptDigest{Kind: DigestMCPHubError}
		return projection.Normalize()
	}
	projection.Route.CallID = requestedID

	switch envelope.Status {
	case "ok":
		page, ok := decodeMCPHubResultPage(envelope)
		if !ok {
			projection.Domain = DomainUnknown
			projection.Evidence = EvidenceNone
			return projection.Normalize()
		}
		projection.Digest = &ReceiptDigest{
			Kind: DigestMCPHubPage, Cursor: *envelope.Cursor, NextCursor: *envelope.NextCursor,
			TotalBytes: *envelope.TotalBytes, PageBytes: int64(len(page)), Done: *envelope.Done,
		}
		if *envelope.Done {
			projection.Domain = DomainSucceeded
			// A completed page may contain the downstream specialist's full
			// envelope. Re-run that bounded document through the exact specialist
			// parsers so pagination does not erase Bob/Cortex domain semantics.
			if domain, evidence, ok := reparseCompletedMCPHubPage(page); ok {
				projection.Domain = domain
				projection.Evidence = evidence
			}
		} else {
			// The page call completed, but the stored result still has a precise
			// continuation cursor. Attention is terminal and avoids a fake spinner.
			projection.Domain = DomainAttention
		}
	case "unavailable":
		projection.Domain = DomainBlocked
		projection.Digest = &ReceiptDigest{Kind: DigestMCPHubUnavailable}
	case "cursor_out_of_range":
		projection.Domain = DomainBlocked
		cursor := int64(0)
		if envelope.Cursor != nil {
			cursor = *envelope.Cursor
		}
		projection.Digest = &ReceiptDigest{Kind: DigestMCPHubCursorOutOfRange, Cursor: cursor}
	default:
		projection.Domain = DomainUnknown
	}
	projection.Evidence = EvidenceNone
	return projection.Normalize()
}

func reparseCompletedMCPHubPage(page []byte) (DomainState, EvidenceState, bool) {
	receipt, stored := storedCallToolReceipt(page)
	if !stored {
		receipt = RawReceipt{Structured: json.RawMessage(page)}
	}
	for operation := range cortexEnvelopeOperations {
		if domain, _, ok := projectCortexReceipt(operation, receipt); ok {
			if receipt.ToolError && domain == DomainSucceeded {
				domain = DomainFailed
			}
			return domain, EvidenceNone, true
		}
	}
	for _, operation := range []string{"bob_check", "bob_context", "bob_inspect", "bob_path", "bob_plan", "bob_playbook", "bob_recipe_describe", "bob_stats", "bob_validate_manifest"} {
		if domain, ok := projectBobReceipt(operation, receipt); ok {
			if receipt.ToolError && domain == DomainSucceeded {
				domain = DomainFailed
			}
			return domain, EvidenceNone, true
		}
	}
	return DomainUnknown, EvidenceNone, false
}

func decodeMCPHubResultPage(envelope mcphubResultPageEnvelope) ([]byte, bool) {
	page, err := base64.StdEncoding.DecodeString(envelope.Data)
	if err != nil || envelope.MediaType != "application/json" || envelope.Cursor == nil ||
		envelope.NextCursor == nil || envelope.Done == nil || envelope.TotalBytes == nil ||
		*envelope.Cursor < 0 || *envelope.NextCursor < *envelope.Cursor ||
		*envelope.TotalBytes < *envelope.NextCursor ||
		int64(len(page)) != *envelope.NextCursor-*envelope.Cursor ||
		len(page) > maxMCPHubResultPageBytes ||
		(!*envelope.Done && *envelope.NextCursor == *envelope.Cursor) {
		return nil, false
	}
	return page, true
}

// TransientModelContent returns useful content from an exact, validated
// transient contract. The content is intentionally not part of ToolProjection:
// callers may feed it to the active provider turn but must pair it with
// SafeReceiptText as the durable replacement.
func TransientModelContent(projection ToolProjection, receipt RawReceipt) (string, bool) {
	projection = projection.Normalize()
	if projection.Digest == nil {
		return "", false
	}
	switch projection.Digest.Kind {
	case DigestMCPHubPage:
		return transientMCPHubResultPage(projection, receipt)
	case DigestHitspecSearch:
		return transientHitspecSearch(projection, receipt)
	default:
		return "", false
	}
}

func transientMCPHubResultPage(projection ToolProjection, receipt RawReceipt) (string, bool) {
	if projection.Operation != "mcphub_get_result" {
		return "", false
	}
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", false
	}
	var envelope mcphubResultPageEnvelope
	if json.Unmarshal(document, &envelope) != nil || envelope.Status != "ok" {
		return "", false
	}
	requestedID := canonicalIdentifier(projection.Route.CallID)
	if requestedID == "" || canonicalIdentifier(envelope.CallID) != requestedID {
		return "", false
	}
	page, ok := decodeMCPHubResultPage(envelope)
	if !ok || envelope.Cursor == nil || envelope.NextCursor == nil || envelope.Done == nil || envelope.TotalBytes == nil {
		return "", false
	}
	digest := projection.Digest
	if digest.Cursor != *envelope.Cursor || digest.NextCursor != *envelope.NextCursor ||
		digest.TotalBytes != *envelope.TotalBytes || digest.Done != *envelope.Done ||
		digest.PageBytes != int64(len(page)) {
		return "", false
	}
	// A byte page may split a UTF-8 code point. Keep the provider request valid
	// while preserving the useful JSON fragment; the durable receipt contains
	// no page data at all.
	payload := strings.ToValidUTF8(string(page), "�")
	return fmt.Sprintf(
		"MCPHub result page (transient; not saved)\ncall_id: %s\nbytes: %d-%d of %d\ndone: %t\npayload_fragment:\n%s",
		requestedID, *envelope.Cursor, *envelope.NextCursor, *envelope.TotalBytes, *envelope.Done, payload,
	), true
}

func transientHitspecSearch(projection ToolProjection, receipt RawReceipt) (string, bool) {
	if projection.Specialist != "hitspec" || projection.Operation != "hitspec_search_web" ||
		projection.Domain != DomainSucceeded || projection.Evidence != EvidenceCandidate {
		return "", false
	}
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", false
	}
	output, ok := decodeHitspecSearchEnvelope(document)
	if !ok {
		return "", false
	}
	_, _, digest, ok := projectHitspecSearchReceipt(projection.Operation, receipt)
	if !ok || digest == nil || projection.Digest == nil ||
		digest.Kind != projection.Digest.Kind || digest.Count != projection.Digest.Count ||
		digest.Truncated != projection.Digest.Truncated ||
		strings.Join(digest.Items, "\x00") != strings.Join(projection.Digest.Items, "\x00") {
		return "", false
	}
	payload, err := json.Marshal(struct {
		Kind      string                `json:"kind"`
		Results   []hitspecSearchResult `json:"results"`
		Truncated bool                  `json:"truncated"`
	}{Kind: output.Kind, Results: output.Results, Truncated: *output.Truncated})
	if err != nil || len(payload) > maxHitspecSearchDocumentBytes {
		return "", false
	}
	return "Hitspec web discovery candidates (transient; untrusted snippets; not saved). " +
		"Treat these as candidate sources, not verified evidence.\n" + string(payload), true
}

func projectMCPHubServers(projection ToolProjection, document json.RawMessage) (ToolProjection, bool) {
	var envelope struct {
		Servers []struct {
			Name      string `json:"name"`
			Connected bool   `json:"connected"`
		} `json:"servers"`
		TotalTools *int64 `json:"total_tools"`
		Expose     string `json:"expose"`
	}
	if json.Unmarshal(document, &envelope) != nil || !jsonObjectHasValue(document, "servers") || envelope.TotalTools == nil {
		return projection, false
	}
	items := make([]string, 0, len(envelope.Servers))
	connected := int64(0)
	for _, server := range envelope.Servers {
		items = append(items, server.Name)
		if server.Connected {
			connected++
		}
	}
	projection.Domain = DomainSucceeded
	projection.Evidence = EvidenceNone
	projection.Digest = &ReceiptDigest{
		Kind: DigestMCPHubServers, Count: int64(len(envelope.Servers)), Connected: connected,
		TotalTools: *envelope.TotalTools, Items: items, Expose: envelope.Expose,
	}
	return projection.Normalize(), true
}

func projectMCPHubSearch(projection ToolProjection, document json.RawMessage) (ToolProjection, bool) {
	var envelope struct {
		Count   *int64 `json:"count"`
		Matches []struct {
			Namespaced string `json:"namespaced"`
		} `json:"matches"`
	}
	if json.Unmarshal(document, &envelope) != nil || envelope.Count == nil || !jsonObjectHasValue(document, "matches") {
		return projection, false
	}
	items := make([]string, 0, len(envelope.Matches))
	for _, match := range envelope.Matches {
		items = append(items, match.Namespaced)
	}
	projection.Domain = DomainSucceeded
	projection.Evidence = EvidenceNone
	projection.Digest = &ReceiptDigest{Kind: DigestMCPHubSearch, Count: *envelope.Count, Items: items}
	return projection.Normalize(), true
}

func projectMCPHubDescribe(projection ToolProjection, document json.RawMessage) (ToolProjection, bool) {
	var envelope struct {
		Server     string `json:"server"`
		Tool       string `json:"tool"`
		Namespaced string `json:"namespaced"`
		Input      struct {
			Required []string `json:"required"`
		} `json:"input_schema"`
	}
	if json.Unmarshal(document, &envelope) != nil || !jsonObjectHasValue(document, "input_schema") {
		return projection, false
	}
	target := envelope.Namespaced
	if target == "" && envelope.Server != "" && envelope.Tool != "" {
		target = envelope.Server + "__" + envelope.Tool
	}
	if canonicalIdentifier(target) == "" {
		return projection, false
	}
	projection.Domain = DomainSucceeded
	projection.Evidence = EvidenceNone
	projection.Digest = &ReceiptDigest{Kind: DigestMCPHubDescribe, Target: target, Required: envelope.Input.Required}
	return projection.Normalize(), true
}

func projectMCPHubResolve(projection ToolProjection, document json.RawMessage) ToolProjection {
	// Resolver receipts are advisory. Start from the fail-closed projection so
	// any contract mismatch remains transport success without domain success.
	projection.Domain = DomainUnknown
	projection.Evidence = EvidenceNone
	projection.Digest = nil
	if !validMCPHubResolverDocument(document) {
		return projection.Normalize()
	}
	if jsonObjectHasValue(document, "error") {
		projection.Domain = DomainFailed
		projection.Digest = &ReceiptDigest{Kind: DigestMCPHubError}
		return projection.Normalize()
	}

	var envelope struct {
		ContractVersion *int            `json:"contract_version"`
		CatalogRevision *string         `json:"catalog_revision"`
		Status          *string         `json:"status"`
		Recommendation  json.RawMessage `json:"recommendation"`
		Ambiguous       *bool           `json:"ambiguous"`
		Alternatives    json.RawMessage `json:"alternatives"`
	}
	if json.Unmarshal(document, &envelope) != nil || envelope.ContractVersion == nil ||
		*envelope.ContractVersion != mcphubResolverContractVersion || envelope.CatalogRevision == nil ||
		!validMCPHubCatalogRevision(*envelope.CatalogRevision) || envelope.Status == nil || envelope.Ambiguous == nil {
		return projection.Normalize()
	}
	alternatives, ok := parseMCPHubResolverAlternatives(envelope.Alternatives)
	if !ok {
		return projection.Normalize()
	}
	recommendation := bytes.TrimSpace(envelope.Recommendation)
	hasRecommendation := jsonKind(recommendation, '{')
	noRecommendation := bytes.Equal(recommendation, []byte("null"))
	if !hasRecommendation && !noRecommendation {
		return projection.Normalize()
	}

	digest := ReceiptDigest{Kind: DigestMCPHubResolve, Ambiguous: *envelope.Ambiguous}
	var domain DomainState
	switch *envelope.Status {
	case "no_match":
		if hasRecommendation || *envelope.Ambiguous || len(alternatives) != 0 {
			return projection.Normalize()
		}
		domain = DomainAttention
	case "confident", "ambiguous":
		wantAmbiguous := *envelope.Status == "ambiguous"
		if !hasRecommendation || *envelope.Ambiguous != wantAmbiguous {
			return projection.Normalize()
		}
		target, required, ok := parseMCPHubResolverRecommendation(recommendation)
		if !ok {
			return projection.Normalize()
		}
		digest.Target = target
		digest.Required = required
		if wantAmbiguous {
			domain = DomainAttention
		} else {
			domain = DomainSucceeded
		}
	default:
		return projection.Normalize()
	}
	seen := map[string]struct{}{digest.Target: {}}
	for _, alternative := range alternatives {
		if _, duplicate := seen[alternative]; duplicate {
			return projection.Normalize()
		}
		seen[alternative] = struct{}{}
		digest.Items = append(digest.Items, alternative)
	}
	projection.Domain = domain
	projection.Evidence = EvidenceNone
	projection.Digest = &digest
	return projection.Normalize()
}

func validMCPHubResolverDocument(document json.RawMessage) bool {
	document = bytes.TrimSpace(document)
	return len(document) > 0 && len(document) <= maxMCPHubResolverDocumentBytes &&
		document[0] == '{' && json.Valid(document)
}

func validMCPHubCatalogRevision(value string) bool {
	if value == "" || len(value) > maxMCPHubCatalogRevisionBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("_-.:/", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func parseMCPHubResolverRecommendation(raw json.RawMessage) (string, []string, bool) {
	var recommendation struct {
		Server     string          `json:"server"`
		Tool       string          `json:"tool"`
		Namespaced string          `json:"namespaced"`
		Required   json.RawMessage `json:"required_fields"`
	}
	if json.Unmarshal(raw, &recommendation) != nil ||
		!validMCPHubResolverIdentifier(recommendation.Server, false) ||
		!validMCPHubResolverIdentifier(recommendation.Tool, true) ||
		!validMCPHubResolverNamespacedIdentifier(recommendation.Namespaced) ||
		recommendation.Namespaced != recommendation.Server+"__"+recommendation.Tool {
		return "", nil, false
	}
	required, ok := parseMCPHubResolverRequiredFields(recommendation.Required)
	if !ok {
		return "", nil, false
	}
	return recommendation.Namespaced, required, true
}

func parseMCPHubResolverAlternatives(raw json.RawMessage) ([]string, bool) {
	var alternatives []struct {
		Namespaced string `json:"namespaced"`
	}
	if !jsonKind(raw, '[') || json.Unmarshal(raw, &alternatives) != nil || alternatives == nil ||
		len(alternatives) > maxMCPHubResolverAlternatives {
		return nil, false
	}
	seen := make(map[string]struct{}, len(alternatives))
	result := make([]string, 0, len(alternatives))
	for _, alternative := range alternatives {
		if !validMCPHubResolverNamespacedIdentifier(alternative.Namespaced) {
			return nil, false
		}
		if _, duplicate := seen[alternative.Namespaced]; duplicate {
			return nil, false
		}
		seen[alternative.Namespaced] = struct{}{}
		result = append(result, alternative.Namespaced)
	}
	return result, true
}

func parseMCPHubResolverRequiredFields(raw json.RawMessage) ([]string, bool) {
	var required []string
	if !jsonKind(raw, '[') || json.Unmarshal(raw, &required) != nil || required == nil ||
		len(required) > maxMCPHubResolverRequiredFields {
		return nil, false
	}
	seen := make(map[string]struct{}, len(required))
	total := 0
	for _, field := range required {
		if field == "" || len(field) > maxMCPHubResolverRequiredFieldBytes || !utf8.ValidString(field) || strings.TrimSpace(field) != field {
			return nil, false
		}
		for _, character := range field {
			if unicode.IsLetter(character) || unicode.IsNumber(character) || strings.ContainsRune("_-.:/$[]", character) {
				continue
			}
			return nil, false
		}
		if _, duplicate := seen[field]; duplicate {
			return nil, false
		}
		seen[field] = struct{}{}
		total += len(field)
		if total > maxMCPHubResolverRequiredFieldNameBytes {
			return nil, false
		}
	}
	return append([]string(nil), required...), true
}

func validMCPHubResolverNamespacedIdentifier(value string) bool {
	server, tool, found := strings.Cut(value, "__")
	return found && validMCPHubResolverIdentifier(server, false) && validMCPHubResolverIdentifier(tool, true)
}

func validMCPHubResolverIdentifier(value string, allowDoubleUnderscore bool) bool {
	if value == "" || len(value) > maxMCPHubResolverIdentifierBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	if !allowDoubleUnderscore && strings.Contains(value, "__") {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || strings.ContainsRune("_-.:/", character) {
			continue
		}
		return false
	}
	return true
}

func projectMCPHubStats(projection ToolProjection, document json.RawMessage) (ToolProjection, bool) {
	var envelope struct {
		Totals *struct {
			Calls     int64 `json:"calls"`
			Errors    int64 `json:"errors"`
			EstTokens int64 `json:"est_tokens"`
		} `json:"totals"`
		Servers []struct {
			Server string `json:"server"`
		} `json:"servers"`
	}
	if json.Unmarshal(document, &envelope) != nil || envelope.Totals == nil || !jsonObjectHasValue(document, "servers") {
		return projection, false
	}
	items := make([]string, 0, len(envelope.Servers))
	for _, server := range envelope.Servers {
		items = append(items, server.Server)
	}
	projection.Domain = DomainSucceeded
	projection.Evidence = EvidenceNone
	projection.Digest = &ReceiptDigest{
		Kind: DigestMCPHubStats, Count: int64(len(envelope.Servers)), Calls: envelope.Totals.Calls,
		Errors: envelope.Totals.Errors, Estimated: envelope.Totals.EstTokens, Items: items,
	}
	return projection.Normalize(), true
}

func jsonObjectHasValue(document json.RawMessage, key string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(document, &object) != nil {
		return false
	}
	value, exists := object[key]
	if !exists {
		return false
	}
	value = bytes.TrimSpace(value)
	return len(value) > 0 && !bytes.Equal(value, []byte("null"))
}

func jsonObjectHasKey(document json.RawMessage, key string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(document, &object) != nil {
		return false
	}
	_, exists := object[key]
	return exists
}

// cortexEnvelopeOperations lists the cortex operations whose results carry the
// shared lifecycle envelope (cortex_status embeds it in its StatusReport).
// List/read projections with their own shapes stay outside this catalog and
// therefore remain DomainUnknown until they gain exact parsers.
var cortexEnvelopeOperations = map[string]bool{
	"cortex_open_task":        true,
	"cortex_start_task":       true,
	"cortex_investigate":      true,
	"cortex_plan":             true,
	"cortex_begin_change":     true,
	"cortex_verify":           true,
	"cortex_remember":         true,
	"cortex_resolve":          true,
	"cortex_note":             true,
	"cortex_request_decision": true,
	"cortex_answer_decision":  true,
	"cortex_abort_task":       true,
	"cortex_status":           true,
}

// projectCortexReceipt recognizes the shared cortex lifecycle envelope for the
// catalogued operations. ok=true with a task identity is coordination success
// — deliberately never verification evidence, so the evidence state stays
// untouched. Structurally unrecognized documents report false and stay on the
// fail-closed unknown path.
func projectCortexReceipt(operation string, receipt RawReceipt) (DomainState, *ReceiptDigest, bool) {
	if !cortexEnvelopeOperations[operation] {
		return "", nil, false
	}
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", nil, false
	}
	var envelope struct {
		OK     *bool   `json:"ok"`
		TaskID *string `json:"taskId"`
		Phase  *string `json:"phase"`
	}
	if json.Unmarshal(document, &envelope) != nil || envelope.OK == nil || envelope.TaskID == nil {
		return "", nil, false
	}
	taskID := canonicalIdentifier(*envelope.TaskID)
	if !*envelope.OK {
		return DomainFailed, &ReceiptDigest{Kind: DigestCortexFailure, Target: taskID}, true
	}
	if taskID == "" {
		return "", nil, false
	}
	digest := &ReceiptDigest{Kind: DigestCortexReceipt, Target: taskID}
	if envelope.Phase != nil {
		if phase := canonicalIdentifier(*envelope.Phase); phase != "" {
			digest.Items = []string{phase}
		}
	}
	return DomainSucceeded, digest, true
}

// projectCortexFailureReceipt recognizes only the shared, explicit rejection
// envelope. It intentionally does not promote ok=true to success: coordination
// success and verified evidence are different claims and need operation-specific
// parsers. Error prose and summaries remain outside the persisted projection.
func projectCortexFailureReceipt(receipt RawReceipt) (string, bool) {
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", false
	}
	var envelope struct {
		OK     *bool  `json:"ok"`
		TaskID string `json:"taskId"`
	}
	if json.Unmarshal(document, &envelope) != nil || envelope.OK == nil || *envelope.OK {
		return "", false
	}
	return canonicalIdentifier(envelope.TaskID), true
}

type bobMCPReceipt struct {
	SchemaVersion         int             `json:"schema_version"`
	OK                    bool            `json:"ok"`
	Workspace             string          `json:"workspace"`
	Clean                 *bool           `json:"clean"`
	LockChanged           *bool           `json:"lock_changed"`
	ConflictCount         *int            `json:"conflict_count"`
	PlanDigest            string          `json:"plan_digest"`
	Authority             json.RawMessage `json:"authority"`
	Report                json.RawMessage `json:"report"`
	Counts                json.RawMessage `json:"counts"`
	Truncation            json.RawMessage `json:"truncation"`
	Recipe                json.RawMessage `json:"recipe"`
	Manifest              json.RawMessage `json:"manifest"`
	Stats                 json.RawMessage `json:"stats"`
	Source                string          `json:"source"`
	ManifestSchemaVersion *int            `json:"manifest_schema_version"`
	Enabled               *bool           `json:"enabled"`
	LocalOnly             *bool           `json:"local_only"`
	Warnings              json.RawMessage `json:"warnings"`
	NextActions           json.RawMessage `json:"next_actions"`
	Actions               []struct {
		Path string `json:"path"`
		Code string `json:"code"`
		Kind string `json:"kind"`
	} `json:"actions"`
	Context   json.RawMessage `json:"context"`
	Path      json.RawMessage `json:"path"`
	Operation string          `json:"operation"`
	List      json.RawMessage `json:"list"`
	Show      json.RawMessage `json:"show"`
	Plan      json.RawMessage `json:"plan"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func projectBobReceipt(operation string, receipt RawReceipt) (DomainState, bool) {
	switch operation {
	case "bob_check", "bob_context", "bob_inspect", "bob_path", "bob_plan", "bob_playbook", "bob_recipe_describe", "bob_stats", "bob_validate_manifest":
	default:
		return "", false
	}
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", false
	}
	var output bobMCPReceipt
	if json.Unmarshal(document, &output) != nil || output.SchemaVersion != 1 ||
		jsonObjectHasKey(document, "command") || jsonObjectHasKey(document, "data") {
		return "", false
	}
	if isBobGuidanceOperation(operation) {
		return projectBobGuidanceReceipt(operation, document, output)
	}
	if output.Error != nil {
		if output.OK || strings.TrimSpace(output.Error.Code) == "" || strings.TrimSpace(output.Error.Code) != output.Error.Code ||
			strings.TrimSpace(output.Error.Message) == "" {
			return "", false
		}
		return classifyBobErrorCode(output.Error.Code), true
	}
	if !output.OK || !validBobMCPSuccess(operation, document, output) {
		return "", false
	}
	if operation == "bob_inspect" {
		inspection, ok := validBobInspectReport(output.Report, output.Workspace)
		if !ok {
			return "", false
		}
		return classifyBobInspection(inspection), true
	}
	if operation != "bob_plan" && operation != "bob_check" {
		return DomainSucceeded, true
	}
	if output.ConflictCount != nil && *output.ConflictCount > 0 {
		return DomainConflict, true
	}
	for _, action := range output.Actions {
		if IsBobConflictCode(action.Code) || action.Kind == "conflict" {
			return DomainConflict, true
		}
	}
	if (output.LockChanged != nil && *output.LockChanged) || (output.Clean != nil && !*output.Clean) {
		return DomainDrift, true
	}
	return DomainSucceeded, true
}

func validBobMCPSuccess(operation string, document json.RawMessage, output bobMCPReceipt) bool {
	switch operation {
	case "bob_inspect":
		_, reportOK := validBobInspectReport(output.Report, output.Workspace)
		return validBobWorkspace(output.Workspace) && validBobAuthority(output.Authority, output.Workspace, true) && reportOK
	case "bob_plan":
		return validBobWorkspace(output.Workspace) && validBobAuthority(output.Authority, output.Workspace, true) &&
			validBobPlanFields(document, output, true)
	case "bob_check":
		return validBobWorkspace(output.Workspace) && validBobAuthority(output.Authority, output.Workspace, true) &&
			validBobPlanFields(document, output, false)
	case "bob_validate_manifest":
		return validBobManifestSuccess(output)
	case "bob_recipe_describe":
		return validBobRecipeDescription(output.Recipe)
	case "bob_stats":
		return validBobAuthority(output.Authority, "", false) && output.Enabled != nil && output.LocalOnly != nil &&
			*output.LocalOnly && validBobStats(output.Stats, *output.Enabled)
	default:
		return false
	}
}

func validBobPlanFields(document json.RawMessage, output bobMCPReceipt, requireActions bool) bool {
	if output.Clean == nil || output.LockChanged == nil || output.ConflictCount == nil || *output.ConflictCount < 0 ||
		!validLowerHexDigest(output.PlanDigest) || !jsonKind(output.Warnings, '[') || !jsonKind(output.NextActions, '[') {
		return false
	}
	counts, ok := validBobActionCounts(output.Counts)
	total := counts.create + counts.update + counts.adopt + counts.unchanged + counts.conflict
	if !ok || total == 0 || counts.conflict != *output.ConflictCount {
		return false
	}
	clean := !*output.LockChanged && counts.create == 0 && counts.update == 0 && counts.adopt == 0 && counts.conflict == 0
	if *output.Clean != clean {
		return false
	}
	if !requireActions {
		return !jsonObjectHasKey(document, "actions") && !jsonObjectHasKey(document, "truncation")
	}
	truncation, ok := validBobTruncation(output.Truncation)
	if !jsonObjectHasKey(document, "actions") || !ok || truncation.returned != len(output.Actions) ||
		truncation.total != total ||
		truncation.eligible+truncation.filtered != truncation.total ||
		(truncation.includeUnchanged && truncation.filtered != 0) ||
		(!truncation.includeUnchanged && truncation.filtered != counts.unchanged) {
		return false
	}
	projected := bobActionCountValues{}
	previousPath := ""
	for index, action := range output.Actions {
		if !validBobActionPath(action.Path) || !validBobActionKindCode(action.Kind, action.Code) {
			return false
		}
		if index > 0 && action.Path <= previousPath {
			return false
		}
		previousPath = action.Path
		projected.increment(action.Kind)
	}
	if !truncation.includeUnchanged && projected.unchanged != 0 {
		return false
	}
	return projected.lessThanOrEqual(counts)
}

type bobInspectionValues struct {
	repositoryState string
	degraded        bool
}

func validBobInspectReport(raw json.RawMessage, workspace string) (bobInspectionValues, bool) {
	if !jsonKind(raw, '{') {
		return bobInspectionValues{}, false
	}
	var report struct {
		SchemaVersion int             `json:"schema_version"`
		Workspace     string          `json:"workspace"`
		Repository    json.RawMessage `json:"repository"`
		Integrations  json.RawMessage `json:"integrations"`
		Degraded      *bool           `json:"degraded"`
		Warnings      json.RawMessage `json:"warnings"`
		NextActions   json.RawMessage `json:"next_actions"`
	}
	if json.Unmarshal(raw, &report) != nil || report.SchemaVersion != 1 || report.Workspace != workspace ||
		report.Degraded == nil || !jsonKind(report.Integrations, '[') || !jsonKind(report.Warnings, '[') ||
		!jsonKind(report.NextActions, '[') {
		return bobInspectionValues{}, false
	}
	state, ok := validBobRepositoryReport(report.Repository, workspace)
	if !ok {
		return bobInspectionValues{}, false
	}
	degraded, ok := validBobInspectIntegrations(report.Integrations, workspace)
	if !ok || degraded != *report.Degraded {
		return bobInspectionValues{}, false
	}
	return bobInspectionValues{repositoryState: state, degraded: *report.Degraded}, true
}

func validBobCLIInspectReport(raw json.RawMessage) (bobInspectionValues, bool) {
	var report struct {
		Workspace string `json:"workspace"`
	}
	if json.Unmarshal(raw, &report) != nil || !validBobWorkspace(report.Workspace) {
		return bobInspectionValues{}, false
	}
	return validBobInspectReport(raw, report.Workspace)
}

func validBobRecipeRef(raw json.RawMessage) (string, bool) {
	if !jsonKind(raw, '{') {
		return "", false
	}
	var recipe struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if json.Unmarshal(raw, &recipe) != nil {
		return "", false
	}
	switch recipe.ID {
	case "files":
		return recipe.ID, recipe.Version == 1
	case "go-agent-tool":
		return recipe.ID, recipe.Version == 3 || recipe.Version == 4
	default:
		return recipe.ID, false
	}
}

type bobActionCountValues struct {
	create    int
	update    int
	adopt     int
	unchanged int
	conflict  int
}

func (c *bobActionCountValues) increment(kind string) {
	switch kind {
	case "create":
		c.create++
	case "update":
		c.update++
	case "adopt":
		c.adopt++
	case "unchanged":
		c.unchanged++
	case "conflict":
		c.conflict++
	}
}

func (c bobActionCountValues) lessThanOrEqual(other bobActionCountValues) bool {
	return c.create <= other.create && c.update <= other.update && c.adopt <= other.adopt &&
		c.unchanged <= other.unchanged && c.conflict <= other.conflict
}

func validBobWorkspace(value string) bool {
	return value != "" && len(value) <= 4096 && utf8.ValidString(value) &&
		!strings.ContainsRune(value, '\x00') && filepath.IsAbs(value)
}

func validBobActionPath(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') || filepath.IsAbs(value) {
		return false
	}
	clean := filepath.Clean(value)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func validBobAuthority(raw json.RawMessage, selected string, requireSelected bool) bool {
	if !jsonKind(raw, '{') {
		return false
	}
	var authority struct {
		Mode                  string `json:"mode"`
		DefaultWorkspace      string `json:"default_workspace"`
		SelectedWorkspace     string `json:"selected_workspace"`
		AllowedWorkspaceCount *int   `json:"allowed_workspace_count"`
	}
	if json.Unmarshal(raw, &authority) != nil ||
		(authority.Mode != "exact_allowlist" && authority.Mode != "any_workspace") ||
		!validBobWorkspace(authority.DefaultWorkspace) || authority.AllowedWorkspaceCount == nil ||
		*authority.AllowedWorkspaceCount < 1 {
		return false
	}
	if requireSelected {
		return validBobWorkspace(selected) && authority.SelectedWorkspace == selected
	}
	return authority.SelectedWorkspace == "" || validBobWorkspace(authority.SelectedWorkspace)
}

func validBobActionCounts(raw json.RawMessage) (bobActionCountValues, bool) {
	if !jsonKind(raw, '{') {
		return bobActionCountValues{}, false
	}
	var counts struct {
		Create    *int `json:"create"`
		Update    *int `json:"update"`
		Adopt     *int `json:"adopt"`
		Unchanged *int `json:"unchanged"`
		Conflict  *int `json:"conflict"`
	}
	if json.Unmarshal(raw, &counts) != nil || counts.Create == nil || counts.Update == nil ||
		counts.Adopt == nil || counts.Unchanged == nil || counts.Conflict == nil ||
		*counts.Create < 0 || *counts.Update < 0 || *counts.Adopt < 0 ||
		*counts.Unchanged < 0 || *counts.Conflict < 0 {
		return bobActionCountValues{}, false
	}
	return bobActionCountValues{
		create: *counts.Create, update: *counts.Update, adopt: *counts.Adopt,
		unchanged: *counts.Unchanged, conflict: *counts.Conflict,
	}, true
}

type bobTruncationValues struct {
	includeUnchanged bool
	max              int
	total            int
	eligible         int
	filtered         int
	returned         int
	byteLimitApplied bool
}

func validBobTruncation(raw json.RawMessage) (bobTruncationValues, bool) {
	if !jsonKind(raw, '{') {
		return bobTruncationValues{}, false
	}
	var value struct {
		IncludeUnchanged  *bool `json:"include_unchanged"`
		MaxActions        *int  `json:"max_actions"`
		TotalActions      *int  `json:"total_actions"`
		EligibleActions   *int  `json:"eligible_actions"`
		FilteredUnchanged *int  `json:"filtered_unchanged"`
		ReturnedActions   *int  `json:"returned_actions"`
		OmittedActions    *int  `json:"omitted_actions"`
		Truncated         *bool `json:"truncated"`
		OutputByteLimit   *int  `json:"output_byte_limit"`
		ByteLimitApplied  *bool `json:"byte_limit_applied"`
	}
	if json.Unmarshal(raw, &value) != nil || value.IncludeUnchanged == nil || value.MaxActions == nil ||
		value.TotalActions == nil || value.EligibleActions == nil || value.FilteredUnchanged == nil ||
		value.ReturnedActions == nil || value.OmittedActions == nil || value.Truncated == nil ||
		value.OutputByteLimit == nil || value.ByteLimitApplied == nil {
		return bobTruncationValues{}, false
	}
	initialReturned := *value.EligibleActions
	if initialReturned > *value.MaxActions {
		initialReturned = *value.MaxActions
	}
	valid := *value.MaxActions >= 1 && *value.MaxActions <= 500 && *value.TotalActions >= 0 && *value.EligibleActions >= 0 &&
		*value.FilteredUnchanged >= 0 && *value.ReturnedActions >= 0 && *value.OmittedActions >= 0 &&
		*value.OutputByteLimit == 30<<10 && *value.ReturnedActions <= initialReturned &&
		*value.OmittedActions == *value.EligibleActions-*value.ReturnedActions &&
		*value.Truncated == (*value.OmittedActions > 0) &&
		*value.ByteLimitApplied == (*value.ReturnedActions < initialReturned)
	if !valid {
		return bobTruncationValues{}, false
	}
	return bobTruncationValues{
		includeUnchanged: *value.IncludeUnchanged,
		max:              *value.MaxActions,
		total:            *value.TotalActions, eligible: *value.EligibleActions,
		filtered: *value.FilteredUnchanged, returned: *value.ReturnedActions,
		byteLimitApplied: *value.ByteLimitApplied,
	}, true
}

func validBobActionKindCode(kind, code string) bool {
	valid := map[string]map[string]bool{
		"create":    {"missing": true},
		"update":    {"mode_drift": true, "content_update": true},
		"adopt":     {"identical_content": true},
		"unchanged": {"in_sync": true},
		"conflict": {
			"unmanaged_differs": true, "managed_hash_mismatch": true, "managed_missing": true,
			"unmanaged_mode_differs": true, "retired_owned": true, "symlink": true, "special_file": true,
		},
	}
	return valid[kind] != nil && valid[kind][code]
}

func validLowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validBobInspectIntegrations(raw json.RawMessage, workspace string) (bool, bool) {
	if !jsonKind(raw, '[') {
		return false, false
	}
	var integrations []json.RawMessage
	if json.Unmarshal(raw, &integrations) != nil || len(integrations) != 2 {
		return false, false
	}
	seen := make(map[string]bool, len(integrations))
	degraded := false
	for _, rawIntegration := range integrations {
		var integration struct {
			Name      string          `json:"name"`
			Selected  *bool           `json:"selected"`
			Available *bool           `json:"available"`
			Probe     json.RawMessage `json:"probe"`
			Index     json.RawMessage `json:"index"`
		}
		if json.Unmarshal(rawIntegration, &integration) != nil || !oneOf(integration.Name, "codemap", "vecgrep") ||
			seen[integration.Name] || integration.Selected == nil || integration.Available == nil ||
			!jsonKind(integration.Probe, '{') || !jsonKind(integration.Index, '{') {
			return false, false
		}
		seen[integration.Name] = true
		var probe struct {
			State string          `json:"state"`
			CWD   string          `json:"cwd"`
			Argv  json.RawMessage `json:"argv"`
		}
		var index struct {
			State string `json:"state"`
		}
		if json.Unmarshal(integration.Probe, &probe) != nil || probe.CWD != workspace || !jsonKind(probe.Argv, '[') ||
			!oneOf(probe.State, "not_selected", "not_requested", "unavailable", "complete", "timed_out", "failed", "invalid_output", "wrong_project") ||
			json.Unmarshal(integration.Index, &index) != nil ||
			!oneOf(index.State, "not_indexed", "fresh", "stale", "incompatible", "unknown") {
			return false, false
		}
		if *integration.Selected {
			if probe.State == "not_selected" || (!*integration.Available && probe.State != "unavailable") ||
				(*integration.Available && probe.State == "unavailable") {
				return false, false
			}
			if !*integration.Available || probe.State != "complete" || index.State != "fresh" {
				degraded = true
			}
		} else if probe.State != "not_selected" || index.State != "unknown" {
			return false, false
		}
	}
	return degraded, seen["codemap"] && seen["vecgrep"]
}

func validBobRepositoryReport(raw json.RawMessage, workspace string) (string, bool) {
	if !jsonKind(raw, '{') {
		return "", false
	}
	var repository struct {
		State         string          `json:"state"`
		ManifestPath  string          `json:"manifest_path"`
		Recipe        string          `json:"recipe"`
		Ready         *bool           `json:"ready"`
		Converged     *bool           `json:"converged"`
		LockChanged   *bool           `json:"lock_changed"`
		ManagedFiles  *int            `json:"managed_files"`
		ConflictCount *int            `json:"conflict_count"`
		Actions       json.RawMessage `json:"actions"`
		Error         string          `json:"error"`
	}
	if json.Unmarshal(raw, &repository) != nil || repository.ManifestPath != filepath.Join(workspace, "bob.yaml") ||
		repository.Ready == nil || repository.Converged == nil || repository.LockChanged == nil ||
		repository.ManagedFiles == nil || repository.ConflictCount == nil ||
		*repository.ManagedFiles < 0 || *repository.ConflictCount < 0 {
		return "", false
	}
	counts, ok := validBobActionCounts(repository.Actions)
	if !ok || counts.conflict != *repository.ConflictCount {
		return "", false
	}
	total := counts.create + counts.update + counts.adopt + counts.unchanged + counts.conflict
	planned := repository.State == "conflicted" || repository.State == "clean" || repository.State == "drifted"
	if planned && (!oneOf(repository.Recipe, "files", "go-agent-tool") || total == 0) {
		return "", false
	}
	if planned && (*repository.ManagedFiles != total || repository.Error != "") {
		return "", false
	}
	switch repository.State {
	case "missing_manifest", "invalid_manifest":
		if repository.Recipe != "" || *repository.Ready || *repository.Converged || *repository.LockChanged ||
			*repository.ManagedFiles != 0 || total != 0 || strings.TrimSpace(repository.Error) == "" {
			return "", false
		}
	case "plan_error":
		if !oneOf(repository.Recipe, "files", "go-agent-tool") || *repository.Ready || *repository.Converged ||
			*repository.LockChanged || *repository.ManagedFiles != 0 || total != 0 || strings.TrimSpace(repository.Error) == "" {
			return "", false
		}
	case "conflicted":
		if *repository.Ready || *repository.Converged || *repository.ConflictCount == 0 {
			return "", false
		}
	case "clean":
		if !*repository.Ready || !*repository.Converged || *repository.LockChanged || *repository.ConflictCount != 0 ||
			counts.create != 0 || counts.update != 0 || counts.adopt != 0 {
			return "", false
		}
	case "drifted":
		if !*repository.Ready || *repository.Converged || *repository.ConflictCount != 0 ||
			(!*repository.LockChanged && counts.create == 0 && counts.update == 0 && counts.adopt == 0) {
			return "", false
		}
	default:
		return "", false
	}
	return repository.State, true
}

func classifyBobInspection(inspection bobInspectionValues) DomainState {
	switch inspection.repositoryState {
	case "missing_manifest", "invalid_manifest":
		return DomainBlocked
	case "plan_error":
		return DomainFailed
	case "conflicted":
		return DomainConflict
	case "drifted":
		return DomainDrift
	case "clean":
		if inspection.degraded {
			return DomainAttention
		}
		return DomainSucceeded
	default:
		return DomainUnknown
	}
}

func validBobManifestSuccess(output bobMCPReceipt) bool {
	if (output.Source != "inline" && output.Source != "workspace") || output.ManifestSchemaVersion == nil ||
		*output.ManifestSchemaVersion != 1 || !jsonKind(output.Warnings, '[') {
		return false
	}
	recipeID, ok := validBobRecipeRef(output.Recipe)
	if !ok || !validBobManifest(output.Manifest, recipeID) {
		return false
	}
	if output.Source == "workspace" {
		return validBobWorkspace(output.Workspace) && validBobAuthority(output.Authority, output.Workspace, true)
	}
	return output.Workspace == "" && validBobAuthority(output.Authority, "", false)
}

func validBobManifest(raw json.RawMessage, expectedRecipe string) bool {
	if !jsonKind(raw, '{') {
		return false
	}
	var manifest struct {
		SchemaVersion int             `json:"schema_version"`
		Recipe        string          `json:"recipe"`
		Product       json.RawMessage `json:"product"`
		Runtime       json.RawMessage `json:"runtime"`
		Surfaces      json.RawMessage `json:"surfaces"`
		Integrations  json.RawMessage `json:"integrations"`
		Distribution  json.RawMessage `json:"distribution"`
		Vars          json.RawMessage `json:"vars"`
		Files         json.RawMessage `json:"files"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.SchemaVersion != 1 || manifest.Recipe != expectedRecipe ||
		(expectedRecipe != "files" && expectedRecipe != "go-agent-tool") ||
		!validBobProduct(manifest.Product, expectedRecipe) || !jsonKind(manifest.Runtime, '{') ||
		!jsonKind(manifest.Surfaces, '{') || !jsonKind(manifest.Integrations, '{') ||
		!jsonKind(manifest.Distribution, '{') {
		return false
	}
	if expectedRecipe == "go-agent-tool" {
		return validBobGoRuntime(manifest.Runtime) && validBobGoSurfaces(manifest.Surfaces) &&
			validBobGoIntegrations(manifest.Integrations) && validBobGoDistribution(manifest.Distribution, manifest.Product) &&
			!jsonObjectHasKey(raw, "vars") && !jsonObjectHasKey(raw, "files")
	}
	return validBobZeroRuntime(manifest.Runtime) && validBobZeroSurfaces(manifest.Surfaces) &&
		validBobZeroIntegrations(manifest.Integrations) && validBobZeroDistribution(manifest.Distribution) &&
		validBobManifestVars(manifest.Vars) && validBobManifestFiles(manifest.Files)
}

func validBobProduct(raw json.RawMessage, recipe string) bool {
	if !jsonKind(raw, '{') {
		return false
	}
	for _, key := range []string{"name", "module", "description", "visibility", "license"} {
		if !jsonObjectHasKey(raw, key) {
			return false
		}
	}
	var product struct {
		Name        string `json:"name"`
		Module      string `json:"module"`
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
		License     string `json:"license"`
	}
	if json.Unmarshal(raw, &product) != nil || !validBobProductName(product.Name) ||
		strings.TrimSpace(product.Description) == "" || !validBobModule(product.Module, recipe == "files") {
		return false
	}
	if recipe == "go-agent-tool" {
		return (product.Visibility == "public" || product.Visibility == "private") && product.License == "MIT"
	}
	return (product.Visibility == "" || product.Visibility == "public" || product.Visibility == "private") &&
		(product.License == "" || (strings.TrimSpace(product.License) != "" && len(product.License) <= 64))
}

func validBobProductName(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func validBobModule(value string, optional bool) bool {
	if value == "" || (optional && strings.TrimSpace(value) == "") {
		return optional
	}
	if strings.ContainsAny(value, " \t\r\n") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, "//") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') ||
			strings.ContainsRune("-._~/", char) {
			continue
		}
		return false
	}
	return true
}

func validBobGoRuntime(raw json.RawMessage) bool {
	var value struct{ Language, Kind string }
	return jsonObjectHasKey(raw, "language") && jsonObjectHasKey(raw, "kind") &&
		json.Unmarshal(raw, &value) == nil && value.Language == "go" && value.Kind == "cli"
}

func validBobZeroRuntime(raw json.RawMessage) bool {
	var value struct{ Language, Kind string }
	return jsonObjectHasKey(raw, "language") && jsonObjectHasKey(raw, "kind") &&
		json.Unmarshal(raw, &value) == nil && value.Language == "" && value.Kind == ""
}

func validBobSurfaces(raw json.RawMessage, wantCLI, wantJSON bool) bool {
	var value struct {
		CLI    *bool `json:"cli"`
		JSON   *bool `json:"json"`
		MCP    *bool `json:"mcp"`
		Studio *bool `json:"studio"`
	}
	return json.Unmarshal(raw, &value) == nil && value.CLI != nil && value.JSON != nil && value.MCP != nil &&
		value.Studio != nil && *value.CLI == wantCLI && *value.JSON == wantJSON && !*value.MCP && !*value.Studio
}

func validBobGoSurfaces(raw json.RawMessage) bool   { return validBobSurfaces(raw, true, true) }
func validBobZeroSurfaces(raw json.RawMessage) bool { return validBobSurfaces(raw, false, false) }

type bobManifestIntegrations struct {
	CodeStructure        string `json:"code_structure"`
	SemanticSearch       string `json:"semantic_search"`
	TerminalVerification string `json:"terminal_verification"`
	BrowserVerification  string `json:"browser_verification"`
	Secrets              string `json:"secrets"`
	Artifacts            string `json:"artifacts"`
}

func decodeBobManifestIntegrations(raw json.RawMessage) (bobManifestIntegrations, bool) {
	for _, key := range []string{"code_structure", "semantic_search", "terminal_verification", "browser_verification", "secrets", "artifacts"} {
		if !jsonObjectHasKey(raw, key) {
			return bobManifestIntegrations{}, false
		}
	}
	var value bobManifestIntegrations
	if json.Unmarshal(raw, &value) != nil {
		return bobManifestIntegrations{}, false
	}
	return value, true
}

func validBobGoIntegrations(raw json.RawMessage) bool {
	value, ok := decodeBobManifestIntegrations(raw)
	return ok && oneOf(value.CodeStructure, "none", "codemap") && oneOf(value.SemanticSearch, "none", "vecgrep") &&
		oneOf(value.TerminalVerification, "none", "glyphrun") && oneOf(value.BrowserVerification, "none", "cairntrace") &&
		oneOf(value.Secrets, "none", "tinyvault") && oneOf(value.Artifacts, "none", "fcheap")
}

func validBobZeroIntegrations(raw json.RawMessage) bool {
	value, ok := decodeBobManifestIntegrations(raw)
	return ok && value == (bobManifestIntegrations{})
}

type bobManifestDistribution struct {
	GitHubActions *bool  `json:"github_actions"`
	GoReleaser    *bool  `json:"goreleaser"`
	Homebrew      *bool  `json:"homebrew"`
	Docs          string `json:"docs"`
}

func decodeBobManifestDistribution(raw json.RawMessage) (bobManifestDistribution, bool) {
	var value bobManifestDistribution
	if json.Unmarshal(raw, &value) != nil || value.GitHubActions == nil || value.GoReleaser == nil ||
		value.Homebrew == nil || !jsonObjectHasKey(raw, "docs") {
		return bobManifestDistribution{}, false
	}
	return value, true
}

func validBobGoDistribution(raw, productRaw json.RawMessage) bool {
	value, ok := decodeBobManifestDistribution(raw)
	var product struct {
		Visibility string `json:"visibility"`
	}
	return ok && json.Unmarshal(productRaw, &product) == nil && oneOf(value.Docs, "none", "markdown") &&
		(!*value.Homebrew || (*value.GoReleaser && product.Visibility == "public"))
}

func validBobZeroDistribution(raw json.RawMessage) bool {
	value, ok := decodeBobManifestDistribution(raw)
	return ok && !*value.GitHubActions && !*value.GoReleaser && !*value.Homebrew && value.Docs == ""
}

func validBobManifestVars(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	if !jsonKind(raw, '{') {
		return false
	}
	var values map[string]string
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 {
		return false
	}
	for key := range values {
		if !validBobVarKey(key) {
			return false
		}
	}
	return true
}

func validBobVarKey(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func validBobManifestFiles(raw json.RawMessage) bool {
	if !jsonKind(raw, '[') {
		return false
	}
	var files []json.RawMessage
	if json.Unmarshal(raw, &files) != nil || len(files) == 0 {
		return false
	}
	seen := make(map[string]bool, len(files))
	for _, rawFile := range files {
		var file struct {
			Path    string  `json:"path"`
			Mode    string  `json:"mode"`
			Content *string `json:"content"`
		}
		if json.Unmarshal(rawFile, &file) != nil || strings.TrimSpace(file.Path) == "" ||
			!utf8.ValidString(file.Path) || file.Content == nil || !validBobFileMode(file.Mode) {
			return false
		}
		canonical := filepath.ToSlash(filepath.Clean(file.Path))
		if seen[canonical] {
			return false
		}
		seen[canonical] = true
	}
	return true
}

func validBobFileMode(value string) bool {
	if value == "" {
		return true
	}
	if len(value) < 3 || len(value) > 4 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '7' {
			return false
		}
	}
	return len(value) == 3 || value[0] == '0'
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validBobRecipeDescription(raw json.RawMessage) bool {
	recipeID, ok := validBobRecipeRef(raw)
	if !ok {
		return false
	}
	var recipe struct {
		ManifestSchemaVersion *int            `json:"manifest_schema_version"`
		Description           string          `json:"description"`
		Surfaces              []string        `json:"surfaces"`
		SupportedChoices      json.RawMessage `json:"supported_choices"`
	}
	if json.Unmarshal(raw, &recipe) != nil || recipe.ManifestSchemaVersion == nil ||
		*recipe.ManifestSchemaVersion != 1 || strings.TrimSpace(recipe.Description) == "" ||
		strings.TrimSpace(recipe.Description) != recipe.Description || len(recipe.Surfaces) == 0 ||
		!jsonObjectHasKey(raw, "supported_choices") {
		return false
	}
	if len(recipe.Surfaces) != 2 || recipe.Surfaces[0] != "cli" || recipe.Surfaces[1] != "json" {
		return false
	}
	choices := bytes.TrimSpace(recipe.SupportedChoices)
	if recipeID == "files" {
		return bytes.Equal(choices, []byte("null"))
	}
	if !jsonKind(choices, '[') {
		return false
	}
	var entries []struct {
		Field  string   `json:"field"`
		Values []string `json:"values"`
	}
	if json.Unmarshal(choices, &entries) != nil || len(entries) == 0 {
		return false
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Field == "" || canonicalIdentifier(entry.Field) != entry.Field || seen[entry.Field] || len(entry.Values) == 0 {
			return false
		}
		seen[entry.Field] = true
		for _, value := range entry.Values {
			if value == "" || canonicalIdentifier(value) != value {
				return false
			}
		}
	}
	return true
}

func validBobCLIRecipeDescription(raw json.RawMessage) bool {
	if _, ok := validBobRecipeRef(raw); !ok {
		return false
	}
	var recipe struct {
		Description string   `json:"description"`
		Surfaces    []string `json:"surfaces"`
	}
	if json.Unmarshal(raw, &recipe) != nil || strings.TrimSpace(recipe.Description) == "" ||
		strings.TrimSpace(recipe.Description) != recipe.Description || len(recipe.Surfaces) != 2 ||
		recipe.Surfaces[0] != "cli" || recipe.Surfaces[1] != "json" {
		return false
	}
	return true
}

type bobTelemetryActionCounts struct {
	create    int
	update    int
	adopt     int
	unchanged int
	conflict  int
}

func decodeBobTelemetryActionCounts(raw json.RawMessage) (bobTelemetryActionCounts, bool) {
	if !jsonKind(raw, '{') {
		return bobTelemetryActionCounts{}, false
	}
	var value struct {
		Create    *int `json:"create"`
		Update    *int `json:"update"`
		Adopt     *int `json:"adopt"`
		Unchanged *int `json:"unchanged"`
		Conflict  *int `json:"conflict"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return bobTelemetryActionCounts{}, false
	}
	read := func(number *int) (int, bool) {
		if number == nil {
			return 0, true
		}
		return *number, *number >= 0
	}
	create, createOK := read(value.Create)
	update, updateOK := read(value.Update)
	adopt, adoptOK := read(value.Adopt)
	unchanged, unchangedOK := read(value.Unchanged)
	conflict, conflictOK := read(value.Conflict)
	return bobTelemetryActionCounts{create: create, update: update, adopt: adopt, unchanged: unchanged, conflict: conflict},
		createOK && updateOK && adoptOK && unchangedOK && conflictOK
}

func (c *bobTelemetryActionCounts) add(other bobTelemetryActionCounts) {
	c.create += other.create
	c.update += other.update
	c.adopt += other.adopt
	c.unchanged += other.unchanged
	c.conflict += other.conflict
}

func validBobStats(raw json.RawMessage, enabled bool) bool {
	if !jsonKind(raw, '{') {
		return false
	}
	var stats struct {
		SchemaVersion  int             `json:"schema_version"`
		Since          string          `json:"since"`
		Until          string          `json:"until"`
		Events         *int            `json:"events"`
		Successes      *int            `json:"successes"`
		Failures       *int            `json:"failures"`
		ConflictEvents *int            `json:"conflict_events"`
		DriftEvents    *int            `json:"drift_events"`
		DurationMS     *int64          `json:"duration_ms"`
		Actions        json.RawMessage `json:"actions"`
		Skipped        *int            `json:"skipped"`
		ByOperation    json.RawMessage `json:"by_operation"`
	}
	if json.Unmarshal(raw, &stats) != nil || stats.SchemaVersion != 1 ||
		stats.Events == nil || stats.Successes == nil || stats.Failures == nil || stats.ConflictEvents == nil ||
		stats.DriftEvents == nil || stats.DurationMS == nil || stats.Skipped == nil {
		return false
	}
	until, untilErr := time.Parse(time.RFC3339Nano, stats.Until)
	actions, actionsOK := decodeBobTelemetryActionCounts(stats.Actions)
	if untilErr != nil || !actionsOK ||
		*stats.Events < 0 || *stats.Successes < 0 || *stats.Failures < 0 ||
		*stats.ConflictEvents < 0 || *stats.DriftEvents < 0 || *stats.DurationMS < 0 || *stats.Skipped < 0 ||
		*stats.Events != *stats.Successes+*stats.Failures || *stats.ConflictEvents+*stats.DriftEvents > *stats.Failures {
		return false
	}
	if stats.Since != "" {
		since, err := time.Parse(time.RFC3339Nano, stats.Since)
		if err != nil || until.Before(since) {
			return false
		}
	}
	byOperation := bytes.TrimSpace(stats.ByOperation)
	if !enabled {
		emptyGroups := bytes.Equal(byOperation, []byte("null"))
		if jsonKind(byOperation, '[') {
			var groups []json.RawMessage
			emptyGroups = json.Unmarshal(byOperation, &groups) == nil && len(groups) == 0
		}
		return emptyGroups && *stats.Events == 0 && *stats.Successes == 0 &&
			*stats.Failures == 0 && *stats.ConflictEvents == 0 && *stats.DriftEvents == 0 &&
			*stats.DurationMS == 0 && *stats.Skipped == 0 && actions == (bobTelemetryActionCounts{})
	}
	if !jsonKind(byOperation, '[') {
		return false
	}
	var operations []json.RawMessage
	if json.Unmarshal(byOperation, &operations) != nil {
		return false
	}
	seen := make(map[string]bool, len(operations))
	totalEvents, totalSuccesses, totalFailures := 0, 0, 0
	totalConflict, totalDrift := 0, 0
	var totalDuration int64
	var totalActions bobTelemetryActionCounts
	for _, rawOperation := range operations {
		var operation struct {
			Operation      string          `json:"operation"`
			Events         *int            `json:"events"`
			Successes      *int            `json:"successes"`
			Failures       *int            `json:"failures"`
			ConflictEvents *int            `json:"conflict_events"`
			DriftEvents    *int            `json:"drift_events"`
			DurationMS     *int64          `json:"duration_ms"`
			Actions        json.RawMessage `json:"actions"`
		}
		if json.Unmarshal(rawOperation, &operation) != nil || seen[operation.Operation] ||
			!oneOf(operation.Operation, "new", "init", "plan", "apply", "check", "doctor", "inspect", "validate_manifest", "recipe_describe") ||
			operation.Events == nil || operation.Successes == nil || operation.Failures == nil ||
			operation.ConflictEvents == nil || operation.DriftEvents == nil || operation.DurationMS == nil {
			return false
		}
		operationActions, ok := decodeBobTelemetryActionCounts(operation.Actions)
		if !ok || *operation.Events < 0 || *operation.Successes < 0 || *operation.Failures < 0 ||
			*operation.ConflictEvents < 0 || *operation.DriftEvents < 0 || *operation.DurationMS < 0 ||
			*operation.Events != *operation.Successes+*operation.Failures ||
			*operation.ConflictEvents+*operation.DriftEvents > *operation.Failures {
			return false
		}
		seen[operation.Operation] = true
		totalEvents += *operation.Events
		totalSuccesses += *operation.Successes
		totalFailures += *operation.Failures
		totalConflict += *operation.ConflictEvents
		totalDrift += *operation.DriftEvents
		totalDuration += *operation.DurationMS
		totalActions.add(operationActions)
	}
	return totalEvents == *stats.Events && totalSuccesses == *stats.Successes && totalFailures == *stats.Failures &&
		totalConflict == *stats.ConflictEvents && totalDrift == *stats.DriftEvents && totalDuration == *stats.DurationMS &&
		totalActions == actions
}

func projectVerifierReceipt(specialist, operation string, receipt RawReceipt) (DomainState, EvidenceState, bool) {
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", EvidenceNone, false
	}
	if specialist == "glyphrun" || specialist == "glyph" {
		return projectGlyphReceipt(operation, document)
	}
	return projectCairnReceipt(operation, document)
}

func projectGlyphReceipt(operation string, document json.RawMessage) (DomainState, EvidenceState, bool) {
	if operation != "glyph_run" && operation != "glyphrun_run" && !strings.HasSuffix(operation, "_glyph_run") {
		return "", EvidenceNone, false
	}
	var envelope struct {
		SchemaVersion int             `json:"schemaVersion"`
		RunID         string          `json:"runId"`
		SpecName      string          `json:"specName"`
		Status        string          `json:"status"`
		StartedAt     string          `json:"startedAt"`
		EndedAt       string          `json:"endedAt"`
		DurationMS    *int64          `json:"durationMs"`
		Target        json.RawMessage `json:"target"`
		Terminal      json.RawMessage `json:"terminal"`
		Outcomes      json.RawMessage `json:"outcomes"`
		Artifacts     json.RawMessage `json:"artifacts"`
		RunDir        string          `json:"runDir"`
		ExitCode      *int            `json:"exitCode"`
	}
	if json.Unmarshal(document, &envelope) != nil || envelope.SchemaVersion != 1 ||
		envelope.RunID == "" || envelope.SpecName == "" || envelope.StartedAt == "" || envelope.EndedAt == "" ||
		envelope.DurationMS == nil || *envelope.DurationMS < 0 || envelope.RunDir == "" || envelope.ExitCode == nil ||
		!jsonKind(envelope.Target, '{') || !jsonKind(envelope.Terminal, '{') ||
		!jsonKind(envelope.Outcomes, '[') || !jsonKind(envelope.Artifacts, '{') {
		return "", EvidenceNone, false
	}
	if envelope.Status == "passed" && (*envelope.ExitCode != 0 || !allOutcomeStatuses(envelope.Outcomes, "passed")) {
		return DomainUnknown, EvidenceNone, true
	}
	return projectRunStatuses([]string{envelope.Status})
}

func projectCairnReceipt(operation string, document json.RawMessage) (DomainState, EvidenceState, bool) {
	if operation != "cairn_run" && operation != "cairntrace_run" && !strings.HasSuffix(operation, "_cairn_run") {
		return "", EvidenceNone, false
	}
	var envelope struct {
		Schema          string            `json:"$schema"`
		Version         string            `json:"version"`
		Status          string            `json:"status"`
		Reason          string            `json:"reason"`
		RunID           string            `json:"runId"`
		RunDir          string            `json:"runDir"`
		Spec            json.RawMessage   `json:"spec"`
		Environment     string            `json:"environment"`
		Backend         string            `json:"backend"`
		ColdStart       *bool             `json:"coldStart"`
		StartedAt       string            `json:"startedAt"`
		EndedAt         string            `json:"endedAt"`
		DurationMS      *int64            `json:"durationMs"`
		Outcomes        json.RawMessage   `json:"outcomes"`
		Steps           json.RawMessage   `json:"steps"`
		Artifacts       json.RawMessage   `json:"artifacts"`
		ExitCode        *int              `json:"exitCode"`
		Parallel        *int              `json:"parallel"`
		TotalDurationMS *int64            `json:"totalDurationMs"`
		Summary         json.RawMessage   `json:"summary"`
		Results         []json.RawMessage `json:"results"`
	}
	if json.Unmarshal(document, &envelope) != nil {
		return "", EvidenceNone, false
	}
	if envelope.Schema == "urn:cairntrace.dev:run:v1" && envelope.Version == "1" {
		if !validCairnRun(envelope.RunID, envelope.RunDir, envelope.Spec, envelope.Environment, envelope.Backend,
			envelope.ColdStart, envelope.StartedAt, envelope.EndedAt, envelope.DurationMS,
			envelope.Outcomes, envelope.Steps, envelope.Artifacts, envelope.ExitCode) {
			return "", EvidenceNone, false
		}
		if envelope.Status == "passed" && (*envelope.ExitCode != 0 || !allOutcomeStatuses(envelope.Outcomes, "passed", "skipped")) {
			return DomainUnknown, EvidenceNone, true
		}
		return projectRunStatuses([]string{envelope.Status})
	}
	if envelope.Schema == "urn:cairntrace.dev:run-batch:v1" && envelope.Version == "1" && len(envelope.Results) > 0 &&
		envelope.Parallel != nil && *envelope.Parallel > 0 && envelope.TotalDurationMS != nil && *envelope.TotalDurationMS >= 0 &&
		jsonKind(envelope.Summary, '{') && envelope.ExitCode != nil {
		statuses := make([]string, 0, len(envelope.Results))
		for _, raw := range envelope.Results {
			domain, _, recognized := projectCairnReceipt(operation, raw)
			if !recognized {
				return "", EvidenceNone, false
			}
			switch domain {
			case DomainSucceeded:
				statuses = append(statuses, "passed")
			case DomainFailed:
				statuses = append(statuses, "failed")
			default:
				return DomainAttention, EvidenceNone, true
			}
		}
		if *envelope.ExitCode == 0 {
			for _, status := range statuses {
				if status != "passed" {
					return DomainUnknown, EvidenceNone, true
				}
			}
		}
		return projectRunStatuses(statuses)
	}
	if envelope.Status == "skipped" && envelope.Reason == "not_in_blast_radius" {
		return DomainAttention, EvidenceNone, true
	}
	return "", EvidenceNone, false
}

func projectRunStatuses(statuses []string) (DomainState, EvidenceState, bool) {
	if len(statuses) == 0 {
		return "", EvidenceNone, false
	}
	for _, status := range statuses {
		switch status {
		case "failed":
			return DomainFailed, EvidenceContradicted, true
		case "errored":
			return DomainFailed, EvidenceNone, true
		case "skipped":
			return DomainAttention, EvidenceNone, true
		case "passed":
		default:
			return "", EvidenceNone, false
		}
	}
	return DomainSucceeded, EvidenceVerified, true
}

func projectCodemapReceipt(operation string, receipt RawReceipt) (DomainState, EvidenceState, bool) {
	document, ok := receiptDocument(receipt)
	if !ok || !strings.HasPrefix(operation, "codemap_") {
		return "", EvidenceNone, false
	}
	var output struct {
		SchemaVersion *int  `json:"schema_version"`
		Registered    *bool `json:"registered"`
		Indexed       *bool `json:"indexed"`
		Stale         *struct {
			Changed int `json:"changed"`
			New     int `json:"new"`
			Deleted int `json:"deleted"`
		} `json:"stale"`
		FileStale     *bool           `json:"file_stale"`
		PartialErrors json.RawMessage `json:"partial_errors"`
		Confidence    *string         `json:"confidence"`
		CallGraph     *string         `json:"call_graph"`
		Error         any             `json:"error"`
	}
	if json.Unmarshal(document, &output) != nil {
		return "", EvidenceNone, false
	}
	if output.Error != nil {
		return DomainFailed, EvidenceNone, true
	}
	recognized := false
	if operation == "codemap_status" {
		recognized = output.Registered != nil || output.Indexed != nil
	} else {
		if output.SchemaVersion != nil {
			if *output.SchemaVersion != 1 {
				return "", EvidenceNone, false
			}
			recognized = true
		}
		recognized = recognized || output.FileStale != nil || output.Confidence != nil || output.CallGraph != nil || rawJSONPresent(output.PartialErrors)
	}
	if !recognized {
		return "", EvidenceNone, false
	}
	if output.Registered != nil && !*output.Registered || output.Indexed != nil && !*output.Indexed {
		return DomainBlocked, EvidenceNone, true
	}
	if output.FileStale != nil && *output.FileStale || output.Stale != nil && output.Stale.Changed+output.Stale.New+output.Stale.Deleted > 0 {
		return DomainAttention, EvidenceStale, true
	}
	if rawJSONArrayLen(output.PartialErrors) > 0 {
		return DomainAttention, EvidenceSupported, true
	}
	evidence := EvidenceSupported
	if output.Confidence != nil {
		switch *output.Confidence {
		case "candidate", "mixed":
			evidence = EvidenceCandidate
		case "none":
			evidence = EvidenceNone
		case "confirmed", "high", "medium", "low", "resolved":
		default:
			return "", EvidenceNone, false
		}
	}
	return DomainSucceeded, evidence, true
}

func projectMonitorReceipt(operation string, receipt RawReceipt) (DomainState, EvidenceState, bool) {
	document, ok := receiptDocument(receipt)
	if !ok || !strings.HasPrefix(operation, "monitor_") {
		return "", EvidenceNone, false
	}
	var output struct {
		SchemaVersion    *int            `json:"schema_version"`
		Kind             string          `json:"kind"`
		Summary          string          `json:"summary"`
		Hostname         string          `json:"hostname"`
		CPU              json.RawMessage `json:"cpu"`
		Processes        json.RawMessage `json:"processes"`
		Total            *int            `json:"total"`
		Truncated        *bool           `json:"truncated"`
		Reason           string          `json:"reason"`
		Healthy          *bool           `json:"healthy"`
		Samples          *int            `json:"samples"`
		Diagnoses        json.RawMessage `json:"diagnoses"`
		Error            any             `json:"error"`
		Refused          bool            `json:"refused"`
		Limitation       string          `json:"limitation"`
		Outcome          string          `json:"outcome"`
		Captured         *bool           `json:"captured"`
		Recording        *bool           `json:"recording"`
		ArtifactVerified *bool           `json:"artifact_verified"`
		Artifact         struct {
			Verified *bool `json:"verified"`
		} `json:"artifact"`
		Verdict string `json:"verdict"`
	}
	if json.Unmarshal(document, &output) != nil {
		return "", EvidenceNone, false
	}
	if output.Refused {
		return DomainBlocked, EvidenceNone, true
	}
	if output.Error != nil {
		return DomainFailed, EvidenceNone, true
	}
	if output.Limitation != "" {
		return DomainAttention, EvidenceNone, true
	}
	switch operation {
	case "monitor_snapshot":
		compact := output.SchemaVersion != nil && *output.SchemaVersion == 1 && output.Kind == "monitor.compact_snapshot"
		full := output.Summary != "" && (output.Hostname != "" || jsonKind(output.CPU, '{'))
		if !compact && !full {
			return "", EvidenceNone, false
		}
		return DomainSucceeded, EvidenceSupported, true
	case "monitor_processes":
		if !jsonKind(output.Processes, '[') || output.Total == nil || output.Truncated == nil ||
			(output.Reason != "top_cpu" && output.Reason != "top_rss" && output.Reason != "filtered") {
			return "", EvidenceNone, false
		}
		return DomainSucceeded, EvidenceSupported, true
	case "monitor_doctor":
		var tools map[string]struct {
			Available *bool `json:"available"`
		}
		if json.Unmarshal(document, &tools) != nil {
			return "", EvidenceNone, false
		}
		recognized, unavailable := false, false
		for _, name := range []string{"codemap", "fcheap", "vecgrep", "tinyvault", "vidtrace", "glyphrun", "cairntrace", "veclite", "tmux"} {
			if status, ok := tools[name]; ok && status.Available != nil {
				recognized = true
				unavailable = unavailable || !*status.Available
			}
		}
		if !recognized {
			return "", EvidenceNone, false
		}
		if unavailable {
			return DomainAttention, EvidenceSupported, true
		}
		return DomainSucceeded, EvidenceSupported, true
	case "monitor_analyze":
		if output.Healthy == nil || output.Samples == nil || *output.Samples < 0 || !jsonKind(output.Diagnoses, '[') {
			return "", EvidenceNone, false
		}
		if !*output.Healthy {
			return DomainAttention, EvidenceSupported, true
		}
		return DomainSucceeded, EvidenceSupported, true
	case "monitor_kill":
		switch output.Outcome {
		case "terminated":
			return DomainSucceeded, EvidenceVerified, true
		case "still_running":
			return DomainFailed, EvidenceContradicted, true
		case "unknown":
			return DomainUnknown, EvidenceNone, true
		default:
			return "", EvidenceNone, false
		}
	case "monitor_profile_capture":
		if output.Captured == nil || output.Artifact.Verified == nil {
			return "", EvidenceNone, false
		}
		if *output.Captured && *output.Artifact.Verified {
			return DomainSucceeded, EvidenceVerified, true
		}
		return DomainAttention, EvidenceNone, true
	case "monitor_investigate":
		switch output.Verdict {
		case "complete":
			return DomainSucceeded, EvidenceSupported, true
		case "partial":
			return DomainAttention, EvidenceSupported, true
		default:
			return "", EvidenceNone, false
		}
	case "monitor_record":
		if output.Recording == nil || output.ArtifactVerified == nil {
			return "", EvidenceNone, false
		}
		if *output.Recording && *output.ArtifactVerified {
			return DomainSucceeded, EvidenceVerified, true
		}
		return DomainAttention, EvidenceNone, true
	default:
		return "", EvidenceNone, false
	}
}

func jsonKind(raw json.RawMessage, kind byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == kind && json.Valid(raw)
}

func rawJSONPresent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && json.Valid(raw)
}

func rawJSONArrayLen(raw json.RawMessage) int {
	if !jsonKind(raw, '[') {
		return 0
	}
	var values []json.RawMessage
	_ = json.Unmarshal(raw, &values)
	return len(values)
}

func allOutcomeStatuses(raw json.RawMessage, allowed ...string) bool {
	if !jsonKind(raw, '[') {
		return false
	}
	var outcomes []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &outcomes) != nil {
		return false
	}
	set := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		set[value] = struct{}{}
	}
	for _, outcome := range outcomes {
		if outcome.ID == "" {
			return false
		}
		if _, ok := set[outcome.Status]; !ok {
			return false
		}
	}
	return true
}

func validCairnRun(runID, runDir string, spec json.RawMessage, environment, backend string, coldStart *bool,
	startedAt, endedAt string, durationMS *int64, outcomes, steps, artifacts json.RawMessage, exitCode *int,
) bool {
	if runID == "" || runDir == "" || environment == "" || backend == "" || coldStart == nil ||
		startedAt == "" || endedAt == "" || durationMS == nil || *durationMS < 0 || exitCode == nil ||
		!jsonKind(spec, '{') || !jsonKind(outcomes, '[') || !jsonKind(steps, '[') || !jsonKind(artifacts, '{') {
		return false
	}
	var ref struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	return json.Unmarshal(spec, &ref) == nil && ref.Name != "" && ref.Path != ""
}

func projectVidtraceReceipt(operation string, receipt RawReceipt) (DomainState, EvidenceState, bool) {
	document, ok := receiptDocument(receipt)
	if !ok || !strings.HasPrefix(operation, "vidtrace_") {
		return "", EvidenceNone, false
	}
	var output struct {
		OK           *bool `json:"ok"`
		Error        any   `json:"error"`
		ConnectError any   `json:"connect_error"`
		CodemapError any   `json:"codemap_error"`
	}
	if json.Unmarshal(document, &output) != nil {
		return "", EvidenceNone, false
	}
	if output.Error != nil || output.OK != nil && !*output.OK {
		return DomainFailed, EvidenceNone, true
	}
	if output.ConnectError != nil || output.CodemapError != nil {
		return DomainAttention, EvidenceCandidate, true
	}
	if output.OK == nil {
		return "", EvidenceNone, false
	}
	return DomainSucceeded, EvidenceSupported, true
}

func projectFileCheapReceipt(operation string, receipt RawReceipt) (DomainState, EvidenceState, *ArtifactDigest, bool) {
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", EvidenceNone, nil, false
	}
	switch operation {
	case "fcheap_save", "filecheap_save":
		return projectFileCheapSaveReceipt(document)
	case "fcheap_restore", "filecheap_restore":
		domain, evidence, recognized := projectFileCheapRestoreReceipt(document, receipt.ToolError)
		return domain, evidence, nil, recognized
	default:
		return "", EvidenceNone, nil, false
	}
}

const (
	maxHitspecSearchDocumentBytes  = 64 << 10
	maxHitspecCaptureDocumentBytes = 64 << 10
	maxHitspecSearchResults        = 10
	maxHitspecSearchQueryRunes     = 512
	maxHitspecSearchTitleRunes     = 300
	maxHitspecSearchSnippetRunes   = 1024
	maxHitspecSearchURLBytes       = 4096
	maxHitspecSearchPublished      = 128
	maxHitspecCaptureTags          = 20
	maxHitspecCaptureFailures      = 16
	maxHitspecCaptureURLBytes      = 8192
)

type hitspecSearchEnvelope struct {
	Kind      string                `json:"kind"`
	Query     string                `json:"query"`
	Results   []hitspecSearchResult `json:"results"`
	Truncated *bool                 `json:"truncated"`
}

type hitspecSearchWireEnvelope struct {
	Kind      string                    `json:"kind"`
	Query     string                    `json:"query"`
	Results   []hitspecSearchWireResult `json:"results"`
	Truncated *bool                     `json:"truncated"`
}

type hitspecSearchWireResult struct {
	Title       json.RawMessage `json:"title"`
	URL         json.RawMessage `json:"url"`
	Domain      json.RawMessage `json:"domain"`
	Snippet     json.RawMessage `json:"snippet"`
	PublishedAt json.RawMessage `json:"published_at,omitempty"`
	CitationID  json.RawMessage `json:"citation_id"`
}

type hitspecSearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Domain      string `json:"domain"`
	Snippet     string `json:"snippet"`
	PublishedAt string `json:"published_at,omitempty"`
	CitationID  string `json:"citation_id"`
}

// projectHitspecSearchReceipt recognizes Hitspec v2.18's provider-neutral,
// bounded discovery envelope. Search completion is typed domain success, while
// its snippets remain candidate evidence rather than verified facts.
func projectHitspecSearchReceipt(operation string, receipt RawReceipt) (DomainState, EvidenceState, *ReceiptDigest, bool) {
	if operation != "hitspec_search_web" {
		return "", EvidenceNone, nil, false
	}
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", EvidenceNone, nil, false
	}
	output, ok := decodeHitspecSearchEnvelope(document)
	if !ok {
		return "", EvidenceNone, nil, false
	}
	domains := make([]string, 0, len(output.Results))
	for _, result := range output.Results {
		domains = append(domains, result.Domain)
	}
	digest := normalizeReceiptDigest(ReceiptDigest{
		Kind: DigestHitspecSearch, Count: int64(len(output.Results)), Items: domains, Truncated: *output.Truncated,
	})
	if digest.Kind == "" {
		return "", EvidenceNone, nil, false
	}
	return DomainSucceeded, EvidenceCandidate, &digest, true
}

func decodeHitspecSearchEnvelope(document json.RawMessage) (hitspecSearchEnvelope, bool) {
	if len(document) == 0 || len(document) > maxHitspecSearchDocumentBytes || !jsonKind(document, '{') {
		return hitspecSearchEnvelope{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var wire hitspecSearchWireEnvelope
	if decoder.Decode(&wire) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		wire.Kind != "discovery" || wire.Truncated == nil || wire.Results == nil ||
		len(wire.Results) > maxHitspecSearchResults ||
		!validHitspecSearchInline(wire.Query, maxHitspecSearchQueryRunes, true) {
		return hitspecSearchEnvelope{}, false
	}
	output := hitspecSearchEnvelope{
		Kind: wire.Kind, Query: wire.Query, Truncated: wire.Truncated,
		Results: make([]hitspecSearchResult, 0, len(wire.Results)),
	}
	seenURLs := make(map[string]struct{}, len(wire.Results))
	for index, encoded := range wire.Results {
		title, titleOK := decodeStrictJSONString(encoded.Title, true)
		rawURL, urlOK := decodeStrictJSONString(encoded.URL, true)
		domain, domainOK := decodeStrictJSONString(encoded.Domain, true)
		snippet, snippetOK := decodeStrictJSONString(encoded.Snippet, true)
		publishedAt, publishedOK := decodeStrictJSONString(encoded.PublishedAt, false)
		citationID, citationOK := decodeStrictJSONString(encoded.CitationID, true)
		if !titleOK || !urlOK || !domainOK || !snippetOK || !publishedOK || !citationOK {
			return hitspecSearchEnvelope{}, false
		}
		result := hitspecSearchResult{
			Title: title, URL: rawURL, Domain: domain, Snippet: snippet,
			PublishedAt: publishedAt, CitationID: citationID,
		}
		if !validHitspecSearchInline(result.Title, maxHitspecSearchTitleRunes, false) ||
			!validHitspecSearchInline(result.Snippet, maxHitspecSearchSnippetRunes, false) ||
			!validHitspecSearchInline(result.PublishedAt, maxHitspecSearchPublished, false) ||
			result.CitationID != fmt.Sprintf("source-%02d", index+1) ||
			!validHitspecSearchURL(result.URL, result.Domain) {
			return hitspecSearchEnvelope{}, false
		}
		if _, duplicate := seenURLs[result.URL]; duplicate {
			return hitspecSearchEnvelope{}, false
		}
		seenURLs[result.URL] = struct{}{}
		output.Results = append(output.Results, result)
	}
	return output, true
}

func decodeStrictJSONString(raw json.RawMessage, required bool) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", !required
	}
	if raw[0] != '"' {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func validHitspecSearchInline(value string, maximumRunes int, required bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximumRunes ||
		strings.Join(strings.Fields(value), " ") != value {
		return false
	}
	if required && value == "" {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validHitspecSearchURL(raw, domain string) bool {
	canonical, canonicalDomain, ok := canonicalHitspecSearchURL(raw)
	return ok && raw == canonical && domain == canonicalDomain
}

// canonicalHitspecSearchURL mirrors Hitspec v2.18's provider boundary. Search
// results are candidate-only: accept every canonical URL the producer can
// emit, while still rejecting credentials, localhost, and explicit non-public
// IP literals. Requiring the already-canonical spelling catches fragments,
// tracking parameters, default ports, and forged domain fields.
func canonicalHitspecSearchURL(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > maxHitspecSearchURLBytes {
		return "", "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil {
		return "", "", false
	}
	parsed.Fragment = ""
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return "", "", false
	}
	if address := net.ParseIP(hostname); address != nil &&
		(!address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast()) {
		return "", "", false
	}
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "gclid" || lower == "fbclid" || lower == "mc_cid" || lower == "mc_eid" {
			query.Del(key)
		}
	}
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	parsed.Host = hostname
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	parsed.RawQuery = query.Encode()
	canonical := parsed.String()
	if len(canonical) > maxHitspecSearchURLBytes {
		return "", "", false
	}
	return canonical, hostname, true
}

// projectHitspecReceipt recognizes the compact Hitspec v2.18 capture surface.
// The webpage body, URLs, title, tags, and downstream failure prose remain
// inside the short-lived parser boundary. Only durable file.cheap identity and
// bounded storage metrics survive as an artifact projection.
func projectHitspecReceipt(operation string, receipt RawReceipt) (DomainState, EvidenceState, *ArtifactDigest, bool) {
	if operation != "hitspec_capture_webpage" {
		return "", EvidenceNone, nil, false
	}
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", EvidenceNone, nil, false
	}
	output, ok := decodeHitspecCaptureEnvelope(document)
	if !ok || *output.HTTPStatus < 200 || *output.HTTPStatus > 299 || *output.MarkdownBytes < 0 {
		return "", EvidenceNone, nil, false
	}
	switch output.Stash.Status {
	case "failed":
		return DomainFailed, EvidenceNone, nil, true
	case "unknown":
		return DomainUnknown, EvidenceNone, nil, true
	case "saved", "saved_with_failures":
	default:
		return "", EvidenceNone, nil, false
	}
	// Capture writes exactly one response.md payload. Contradictory storage
	// metrics or an index that succeeded without being requested are not the
	// installed compact contract and must not become durable evidence.
	if *output.Stash.FileCount != 1 || *output.Stash.TotalSize != *output.MarkdownBytes ||
		*output.Stash.Indexed && !*output.Stash.IndexRequested {
		return "", EvidenceNone, nil, false
	}
	artifact := normalizeArtifactDigest(ArtifactDigest{
		Kind:           ArtifactDigestHitspecCapture,
		ID:             output.Stash.ID,
		SchemaVersion:  hitspecCaptureSchema,
		FileCount:      *output.Stash.FileCount,
		TotalSize:      *output.Stash.TotalSize,
		CreatedAt:      output.Stash.CreatedAt,
		IndexingFailed: *output.Stash.IndexRequested && !*output.Stash.Indexed,
	})
	if artifact.Kind == "" {
		return "", EvidenceNone, nil, false
	}
	domain := DomainSucceeded
	if output.Stash.Status == "saved_with_failures" || output.Stash.FailedCount > 0 || artifact.IndexingFailed {
		domain = DomainAttention
	}
	return domain, EvidenceSupported, &artifact, true
}

type hitspecCaptureWireEnvelope struct {
	URL           json.RawMessage `json:"url"`
	FinalURL      json.RawMessage `json:"final_url"`
	Title         json.RawMessage `json:"title"`
	HTTPStatus    *int64          `json:"http_status"`
	ContentType   json.RawMessage `json:"content_type"`
	MarkdownBytes *int64          `json:"markdown_bytes"`
	Stash         *struct {
		ID             json.RawMessage `json:"id"`
		Name           json.RawMessage `json:"name,omitempty"`
		Status         json.RawMessage `json:"status"`
		CreatedAt      json.RawMessage `json:"created_at,omitempty"`
		ExpiresAt      json.RawMessage `json:"expires_at,omitempty"`
		Tags           json.RawMessage `json:"tags,omitempty"`
		ContentHash    json.RawMessage `json:"content_hash,omitempty"`
		FileCount      *int64          `json:"file_count"`
		TotalSize      *int64          `json:"total_size"`
		Indexed        *bool           `json:"indexed"`
		IndexRequested *bool           `json:"index_requested"`
		Failed         json.RawMessage `json:"failed,omitempty"`
	} `json:"stash"`
}

type hitspecCaptureEnvelope struct {
	HTTPStatus    *int64
	MarkdownBytes *int64
	Stash         struct {
		ID             string
		Status         string
		CreatedAt      string
		FileCount      *int64
		TotalSize      *int64
		Indexed        *bool
		IndexRequested *bool
		FailedCount    int
	}
}

type hitspecCaptureFailureWire struct {
	ID    json.RawMessage `json:"id"`
	Stage json.RawMessage `json:"stage"`
	Error json.RawMessage `json:"error"`
}

// decodeHitspecCaptureEnvelope accepts exactly the bounded v2.18 capture
// receipt. Private page metadata is type-checked inside the parser boundary,
// then discarded before the durable artifact projection is constructed.
func decodeHitspecCaptureEnvelope(document json.RawMessage) (hitspecCaptureEnvelope, bool) {
	if len(document) == 0 || len(document) > maxHitspecCaptureDocumentBytes || !jsonKind(document, '{') {
		return hitspecCaptureEnvelope{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var wire hitspecCaptureWireEnvelope
	if decoder.Decode(&wire) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		wire.HTTPStatus == nil || wire.MarkdownBytes == nil || wire.Stash == nil ||
		wire.Stash.FileCount == nil || wire.Stash.TotalSize == nil ||
		wire.Stash.Indexed == nil || wire.Stash.IndexRequested == nil {
		return hitspecCaptureEnvelope{}, false
	}

	pageURL, pageURLOK := decodeStrictJSONString(wire.URL, true)
	finalURL, finalURLOK := decodeStrictJSONString(wire.FinalURL, true)
	title, titleOK := decodeStrictJSONString(wire.Title, true)
	contentType, contentTypeOK := decodeStrictJSONString(wire.ContentType, true)
	id, idOK := decodeStrictJSONString(wire.Stash.ID, true)
	name, nameOK := decodeStrictJSONString(wire.Stash.Name, false)
	status, statusOK := decodeStrictJSONString(wire.Stash.Status, true)
	createdAt, createdAtOK := decodeStrictJSONString(wire.Stash.CreatedAt, false)
	expiresAt, expiresAtOK := decodeStrictJSONString(wire.Stash.ExpiresAt, false)
	contentHash, contentHashOK := decodeStrictJSONString(wire.Stash.ContentHash, false)
	if !pageURLOK || !finalURLOK || !titleOK || !contentTypeOK || !idOK || !nameOK || !statusOK ||
		!createdAtOK || !expiresAtOK || !contentHashOK ||
		!validHitspecCaptureURL(pageURL) ||
		!validHitspecCaptureURL(finalURL) ||
		!validHitspecCaptureRunes(title, 300, false) ||
		!validHitspecCaptureString(contentType, 256, false) ||
		!validHitspecCaptureString(id, maxProjectionArtifactIDBytes, false) ||
		!validHitspecCaptureString(name, 80, false) ||
		!validHitspecCaptureString(status, 64, true) ||
		!validHitspecCaptureString(createdAt, 64, false) ||
		!validHitspecCaptureString(expiresAt, 64, false) ||
		!validHitspecCaptureString(contentHash, 128, false) {
		return hitspecCaptureEnvelope{}, false
	}
	if !validHitspecCaptureTags(wire.Stash.Tags) {
		return hitspecCaptureEnvelope{}, false
	}
	// created_at is optional producer metadata. Keep it only when it can satisfy
	// the durable artifact contract; a custom sink's opaque timestamp must not
	// invalidate an otherwise exact saved-capture receipt.
	if createdAt != "" {
		if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
			createdAt = ""
		}
	}
	failedCount, failuresOK := decodeHitspecCaptureFailures(wire.Stash.Failed)
	if !failuresOK {
		return hitspecCaptureEnvelope{}, false
	}

	output := hitspecCaptureEnvelope{HTTPStatus: wire.HTTPStatus, MarkdownBytes: wire.MarkdownBytes}
	output.Stash.ID = id
	output.Stash.Status = status
	output.Stash.CreatedAt = createdAt
	output.Stash.FileCount = wire.Stash.FileCount
	output.Stash.TotalSize = wire.Stash.TotalSize
	output.Stash.Indexed = wire.Stash.Indexed
	output.Stash.IndexRequested = wire.Stash.IndexRequested
	output.Stash.FailedCount = failedCount
	return output, true
}

func validHitspecCaptureTags(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return true
	}
	if !jsonKind(raw, '[') {
		return false
	}
	var encoded []json.RawMessage
	if json.Unmarshal(raw, &encoded) != nil || len(encoded) > maxHitspecCaptureTags {
		return false
	}
	for _, item := range encoded {
		tag, ok := decodeStrictJSONString(item, true)
		if !ok || !validHitspecCaptureString(tag, 64, true) {
			return false
		}
	}
	return true
}

func decodeHitspecCaptureFailures(raw json.RawMessage) (int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, true
	}
	if !jsonKind(raw, '[') {
		return 0, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var encoded []hitspecCaptureFailureWire
	if decoder.Decode(&encoded) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(encoded) > maxHitspecCaptureFailures {
		return 0, false
	}
	for _, item := range encoded {
		id, idOK := decodeStrictJSONString(item.ID, true)
		stage, stageOK := decodeStrictJSONString(item.Stage, true)
		failure, failureOK := decodeStrictJSONString(item.Error, true)
		if !idOK || !stageOK || !failureOK ||
			!validHitspecCaptureString(id, 128, false) ||
			!validHitspecCaptureString(stage, 64, false) ||
			!validHitspecCaptureString(failure, 1024, false) {
			return 0, false
		}
	}
	return len(encoded), true
}

func validHitspecCaptureString(value string, maximumBytes int, required bool) bool {
	if !utf8.ValidString(value) || len(value) > maximumBytes || required && value == "" {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validHitspecCaptureRunes(value string, maximumRunes int, required bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximumRunes || required && value == "" {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validHitspecCaptureURL(raw string) bool {
	if len(raw) == 0 || len(raw) > maxHitspecCaptureURLBytes {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "" {
		return false
	}
	if address, err := netip.ParseAddr(hostname); err == nil {
		return validHitspecCapturePublicIP(address.Unmap())
	}
	for _, suffix := range []string{"localhost", "local", "internal", "invalid", "test", "onion", "home.arpa"} {
		if hostname == suffix || strings.HasSuffix(hostname, "."+suffix) {
			return false
		}
	}
	labels := strings.Split(hostname, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validHitspecCapturePublicIP(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsUnspecified() || address.IsLinkLocalUnicast() || address.IsMulticast() {
		return false
	}
	for _, rawPrefix := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24",
		"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4",
		"240.0.0.0/4", "100::/64", "64:ff9b::/96", "64:ff9b:1::/48", "2001:db8::/32", "2002::/16",
	} {
		if netip.MustParsePrefix(rawPrefix).Contains(address) {
			return false
		}
	}
	return true
}

type fileCheapSaveEnvelope struct {
	Manifest       *fileCheapManifest `json:"manifest"`
	SecretsWarning json.RawMessage    `json:"secrets_warning"`
	Secrets        json.RawMessage    `json:"secrets"`
	Indexed        json.RawMessage    `json:"indexed"`
	IndexError     json.RawMessage    `json:"index_error"`
	Error          json.RawMessage    `json:"error"`
}

type fileCheapManifest struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	CreatedAt     string `json:"created_at"`
	FileCount     *int64 `json:"file_count"`
	TotalSize     *int64 `json:"total_size"`
	ContentHash   string `json:"content_hash"`
}

func projectFileCheapSaveReceipt(document json.RawMessage) (DomainState, EvidenceState, *ArtifactDigest, bool) {
	var output fileCheapSaveEnvelope
	if json.Unmarshal(document, &output) != nil {
		return "", EvidenceNone, nil, false
	}
	if rawJSONPresent(output.Error) {
		return DomainFailed, EvidenceNone, nil, true
	}
	if output.Manifest == nil || output.Manifest.FileCount == nil || output.Manifest.TotalSize == nil {
		return "", EvidenceNone, nil, false
	}

	secretsWarning, validSecretsShape := fileCheapSecretsWarning(output.SecretsWarning, output.Secrets)
	if !validSecretsShape {
		return "", EvidenceNone, nil, false
	}
	indexingFailed, validIndexShape := fileCheapIndexState(output.Indexed, output.IndexError)
	if !validIndexShape {
		return "", EvidenceNone, nil, false
	}

	artifact := normalizeArtifactDigest(ArtifactDigest{
		Kind:           ArtifactDigestFileCheapStash,
		ID:             output.Manifest.ID,
		SchemaVersion:  output.Manifest.SchemaVersion,
		ContentSHA256:  output.Manifest.ContentHash,
		FileCount:      *output.Manifest.FileCount,
		TotalSize:      *output.Manifest.TotalSize,
		CreatedAt:      output.Manifest.CreatedAt,
		SecretsWarning: secretsWarning,
		IndexingFailed: indexingFailed,
	})
	if artifact.Kind == "" {
		return "", EvidenceNone, nil, false
	}
	domain := DomainSucceeded
	if secretsWarning {
		domain = DomainAttention
	}
	return domain, EvidenceSupported, &artifact, true
}

// fileCheapSecretsWarning validates the exact paired save-time scan fields but
// deliberately retains neither warning prose nor individual findings.
func fileCheapSecretsWarning(warning, findings json.RawMessage) (present, valid bool) {
	warningPresent := rawJSONPresent(warning)
	findingsPresent := rawJSONPresent(findings)
	if !warningPresent && !findingsPresent {
		return false, true
	}
	if !warningPresent || !jsonKind(findings, '[') || rawJSONArrayLen(findings) == 0 {
		return false, false
	}
	var warningText string
	if json.Unmarshal(warning, &warningText) != nil || strings.TrimSpace(warningText) == "" {
		return false, false
	}
	return true, true
}

// fileCheapIndexState treats indexing as explicitly best-effort. A non-empty
// index_error is projected to a boolean while the successfully persisted stash
// remains a domain success; arbitrary error prose is discarded.
func fileCheapIndexState(indexed, indexError json.RawMessage) (failed, valid bool) {
	indexedPresent := rawJSONPresent(indexed)
	errorPresent := rawJSONPresent(indexError)
	if indexedPresent && errorPresent {
		return false, false
	}
	if indexedPresent && !jsonKind(indexed, '{') {
		return false, false
	}
	if !errorPresent {
		return false, true
	}
	var errorText string
	if json.Unmarshal(indexError, &errorText) != nil || strings.TrimSpace(errorText) == "" {
		return false, false
	}
	return true, true
}

func projectFileCheapRestoreReceipt(document json.RawMessage, toolError bool) (DomainState, EvidenceState, bool) {
	var output struct {
		StashID    string          `json:"stash_id"`
		Target     string          `json:"target"`
		FileCount  *int64          `json:"file_count"`
		Status     string          `json:"status"`
		Verified   *bool           `json:"verified"`
		Mismatches json.RawMessage `json:"mismatches"`
		Error      json.RawMessage `json:"error"`
	}
	if json.Unmarshal(document, &output) != nil {
		return "", EvidenceNone, false
	}
	if rawJSONPresent(output.Error) {
		return DomainFailed, EvidenceNone, true
	}
	if !validFileCheapStashID(output.StashID) || strings.TrimSpace(output.Target) == "" ||
		output.FileCount == nil || !validProjectionMetric(*output.FileCount) || output.Verified == nil ||
		!jsonKind(output.Mismatches, '[') {
		return "", EvidenceNone, false
	}
	mismatches := rawJSONArrayLen(output.Mismatches)
	switch output.Status {
	case "restored":
		if !*output.Verified || mismatches != 0 || toolError {
			return "", EvidenceNone, false
		}
		return DomainSucceeded, EvidenceVerified, true
	case "restored_unverified":
		if *output.Verified || mismatches != 0 {
			return "", EvidenceNone, false
		}
		return DomainAttention, EvidenceSupported, true
	case "restored_with_mismatches":
		if *output.Verified || mismatches == 0 {
			return "", EvidenceNone, false
		}
		return DomainAttention, EvidenceContradicted, true
	default:
		return "", EvidenceNone, false
	}
}

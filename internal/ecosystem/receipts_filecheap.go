package ecosystem

import (
	"encoding/json"
	"strings"
)

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

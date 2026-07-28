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
	case "fcheap_artifact_ref", "filecheap_artifact_ref":
		return projectFileCheapArtifactRefReceipt(document)
	default:
		return "", EvidenceNone, nil, false
	}
}

// artifactRefEnvelope is the exact ArtifactRefV1 wire shape
// (urn:filecheap.dev:artifact-ref:v1). Producer metadata and URIs are
// validated for shape and then discarded; only the bounded digest survives.
type artifactRefEnvelope struct {
	Schema     string          `json:"$schema"`
	Version    *int            `json:"version"`
	Provider   string          `json:"provider"`
	URI        string          `json:"uri"`
	ArtifactID string          `json:"artifact_id"`
	Kind       string          `json:"kind"`
	Producer   json.RawMessage `json:"producer"`
	WebURL     string          `json:"web_url"`
	Error      json.RawMessage `json:"error"`
}

// artifactDigestFromRef validates one ArtifactRefV1 document and projects it
// to the bounded portable digest. Unknown schemas, versions, providers, or
// malformed identities yield no digest — never a partial one.
func artifactDigestFromRef(document json.RawMessage) (*ArtifactDigest, bool) {
	var ref artifactRefEnvelope
	if json.Unmarshal(document, &ref) != nil {
		return nil, false
	}
	if ref.Schema != artifactRefSchemaURI || ref.Version == nil || *ref.Version != 1 {
		return nil, false
	}
	if strings.TrimSpace(ref.URI) == "" || len(ref.URI) > 2048 {
		return nil, false
	}
	if rawJSONPresent(ref.Producer) && !jsonKind(ref.Producer, '{') {
		return nil, false
	}
	switch ref.Provider {
	case artifactRefProviderLocal:
		// The contract binds the local URI to the artifact identity exactly.
		if ref.WebURL != "" || ref.URI != fileCheapStashURI(ref.ArtifactID) {
			return nil, false
		}
	case artifactRefProviderCloud:
		if ref.ArtifactID == "" {
			return nil, false
		}
	case artifactRefProviderLink:
		if ref.ArtifactID != "" || ref.WebURL != "" {
			return nil, false
		}
	default:
		return nil, false
	}
	digest := normalizeArtifactDigest(ArtifactDigest{
		Kind:          ArtifactDigestPortableRef,
		ID:            ref.ArtifactID,
		SchemaVersion: artifactRefSchemaURI,
		Provider:      ref.Provider,
		RefKind:       ref.Kind,
	})
	if digest.Kind == "" {
		return nil, false
	}
	return &digest, true
}

func projectFileCheapArtifactRefReceipt(document json.RawMessage) (DomainState, EvidenceState, *ArtifactDigest, bool) {
	var envelope artifactRefEnvelope
	if json.Unmarshal(document, &envelope) != nil {
		return "", EvidenceNone, nil, false
	}
	if rawJSONPresent(envelope.Error) {
		return DomainFailed, EvidenceNone, nil, true
	}
	digest, ok := artifactDigestFromRef(document)
	if !ok {
		return "", EvidenceNone, nil, false
	}
	// A local reference is backed by a stash the emitting tool just resolved;
	// cloud and link references cannot be corroborated on this host.
	if digest.Provider == artifactRefProviderLocal {
		return DomainSucceeded, EvidenceSupported, digest, true
	}
	return DomainSucceeded, EvidenceNone, digest, true
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

package ecosystem

import (
	"fmt"
	"strings"
	"time"
)

const (
	maxProjectionArtifactIDBytes = 128
	fileCheapManifestSchema      = "1.0"
	hitspecCaptureSchema         = "hitspec.capture.v1"
)

// ArtifactDigestKind identifies one exact, bounded artifact contract. Artifact
// digests contain durable identity and integrity metadata only; paths, file
// trees, tags, custom fields, scan findings, and provider prose stay inside the
// short-lived parser boundary.
type ArtifactDigestKind string

const (
	ArtifactDigestFileCheapStash ArtifactDigestKind = "filecheap_stash"
	ArtifactDigestHitspecCapture ArtifactDigestKind = "hitspec_capture"
)

// ArtifactDigest is the persistable projection of one durable artifact. URI is
// host-derived during Normalize and is never accepted from an MCP response or a
// restored session. SchemaVersion identifies the exact parser contract: it is
// file.cheap's manifest version for direct saves and a host-owned projection
// version for Hitspec capture receipts. ContentSHA256 is retained only for the
// direct file.cheap manifest contract, which guarantees its algorithm.
type ArtifactDigest struct {
	Kind           ArtifactDigestKind `json:"kind"`
	ID             string             `json:"id"`
	URI            string             `json:"uri"`
	SchemaVersion  string             `json:"schema_version"`
	ContentSHA256  string             `json:"content_sha256"`
	FileCount      int64              `json:"file_count"`
	TotalSize      int64              `json:"total_size"`
	CreatedAt      string             `json:"created_at"`
	SecretsWarning bool               `json:"secrets_warning,omitempty"`
	IndexingFailed bool               `json:"indexing_failed,omitempty"`
}

func normalizeArtifactDigest(digest ArtifactDigest) ArtifactDigest {
	if !validFileCheapStashID(digest.ID) || !validProjectionMetric(digest.FileCount) || !validProjectionMetric(digest.TotalSize) {
		return ArtifactDigest{}
	}
	switch digest.Kind {
	case ArtifactDigestFileCheapStash:
		if digest.SchemaVersion != fileCheapManifestSchema || !validLowerSHA256(digest.ContentSHA256) || digest.CreatedAt == "" {
			return ArtifactDigest{}
		}
	case ArtifactDigestHitspecCapture:
		if digest.SchemaVersion != hitspecCaptureSchema || digest.ContentSHA256 != "" || digest.FileCount < 1 {
			return ArtifactDigest{}
		}
	default:
		return ArtifactDigest{}
	}
	if digest.CreatedAt != "" {
		createdAt, err := time.Parse(time.RFC3339, digest.CreatedAt)
		if err != nil {
			return ArtifactDigest{}
		}
		digest.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	}
	digest.URI = fileCheapStashURI(digest.ID)
	return digest
}

func (d ArtifactDigest) summaryText() string {
	d = normalizeArtifactDigest(d)
	if d.Kind == "" {
		return ""
	}
	parts := []string{
		d.URI,
		metricLabel(d.FileCount, "file", "files"),
		fmt.Sprintf("%d bytes", d.TotalSize),
	}
	if d.SecretsWarning {
		parts = append(parts, "potential secrets need review")
	}
	if d.IndexingFailed {
		parts = append(parts, "saved; indexing incomplete")
	}
	return strings.Join(parts, " · ")
}

func fileCheapStashURI(id string) string {
	return "fcheap://stash/" + id
}

// validFileCheapStashID accepts a bounded portable URI path element. The
// current file.cheap generator emits lowercase ASCII IDs, while preserving
// case here keeps the value opaque and avoids inventing a different identity.
func validFileCheapStashID(id string) bool {
	if id == "" || len(id) > maxProjectionArtifactIDBytes || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	reserved := strings.ToLower(id)
	if reserved == "fcheap.db" || strings.HasPrefix(reserved, "fcheap.db-") ||
		reserved == "fcheap.veclite" || strings.HasPrefix(reserved, "fcheap.veclite.") {
		return false
	}
	return true
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validProjectionMetric(value int64) bool {
	return value >= 0 && value <= maxProjectionMetric
}

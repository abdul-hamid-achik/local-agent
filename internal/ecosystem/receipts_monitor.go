package ecosystem

import (
	"encoding/json"
	"strings"
)

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

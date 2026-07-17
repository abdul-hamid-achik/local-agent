package ecosystem

import (
	"encoding/json"
	"strings"
)

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

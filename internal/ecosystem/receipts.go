package ecosystem

import (
	"bytes"
	"encoding/json"
	"strings"
)

func receiptDocument(receipt RawReceipt) (json.RawMessage, bool) {
	if document, ok := exactJSONDocument(receipt.Structured); ok {
		return document, true
	}
	return exactJSONDocument(json.RawMessage(strings.TrimSpace(receipt.Text)))
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
	var envelope struct {
		Status string `json:"status"`
		CallID string `json:"callId"`
		Done   *bool  `json:"done"`
		Error  any    `json:"error"`
	}
	if json.Unmarshal(document, &envelope) != nil {
		return projection, false
	}
	if envelope.CallID != "" {
		projection.Route.CallID = canonicalIdentifier(envelope.CallID)
	}

	if projection.Operation == "mcphub_get_result" {
		switch envelope.Status {
		case "ok":
			switch {
			case envelope.Done == nil:
				projection.Domain = DomainUnknown
			case !*envelope.Done:
				projection.Domain = DomainAttention
			default:
				projection.Domain = DomainSucceeded
			}
		case "unavailable", "cursor_out_of_range":
			projection.Domain = DomainBlocked
		default:
			projection.Domain = DomainUnknown
		}
		projection.Evidence = EvidenceNone
		return projection, true
	}
	if envelope.Status == "stored" && envelope.CallID != "" {
		projection.Domain = DomainAttention
		projection.Evidence = EvidenceNone
		return projection, true
	}
	return projection, false
}

func projectBobReceipt(operation string, receipt RawReceipt) (DomainState, bool) {
	switch operation {
	case "bob_check", "bob_inspect", "bob_plan", "bob_recipe_describe", "bob_stats", "bob_validate_manifest":
	default:
		return "", false
	}
	document, ok := receiptDocument(receipt)
	if !ok {
		return "", false
	}
	var output struct {
		SchemaVersion int             `json:"schema_version"`
		OK            bool            `json:"ok"`
		Command       string          `json:"command"`
		Data          json.RawMessage `json:"data"`
		Clean         *bool           `json:"clean"`
		LockChanged   bool            `json:"lock_changed"`
		ConflictCount int             `json:"conflict_count"`
		Actions       []struct {
			Code string `json:"code"`
			Kind string `json:"kind"`
		} `json:"actions"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(document, &output) != nil || output.SchemaVersion != 1 || output.Command != "" || rawJSONPresent(output.Data) {
		return "", false
	}
	if output.Error != nil || !output.OK {
		if output.Error != nil {
			switch output.Error.Code {
			case "conflicts":
				return DomainConflict, true
			case "missing_manifest", "manifest_invalid", "input_invalid", "workspace_invalid", "workspace_out_of_scope":
				return DomainBlocked, true
			}
		}
		return DomainFailed, true
	}
	if output.ConflictCount > 0 {
		return DomainConflict, true
	}
	for _, action := range output.Actions {
		if IsBobConflictCode(action.Code) || action.Kind == "conflict" {
			return DomainConflict, true
		}
	}
	if output.LockChanged || (output.Clean != nil && !*output.Clean) {
		return DomainDrift, true
	}
	return DomainSucceeded, true
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

func projectFileCheapReceipt(operation string, receipt RawReceipt) (DomainState, EvidenceState, bool) {
	document, ok := receiptDocument(receipt)
	if !ok || (!strings.HasPrefix(operation, "fcheap_") && !strings.HasPrefix(operation, "filecheap_")) {
		return "", EvidenceNone, false
	}
	var output struct {
		Status     string `json:"status"`
		Verified   *bool  `json:"verified"`
		Failed     []any  `json:"failed"`
		Mismatches []any  `json:"mismatches"`
		Error      any    `json:"error"`
	}
	if json.Unmarshal(document, &output) != nil {
		return "", EvidenceNone, false
	}
	if output.Error != nil {
		return DomainFailed, EvidenceNone, true
	}
	switch output.Status {
	case "saved", "restored":
		if output.Verified != nil && *output.Verified {
			return DomainSucceeded, EvidenceVerified, true
		}
		return DomainSucceeded, EvidenceSupported, true
	case "saved_with_failures", "restored_unverified":
		return DomainAttention, EvidenceSupported, true
	case "restored_with_mismatches":
		return DomainAttention, EvidenceContradicted, true
	}
	if len(output.Failed) > 0 || len(output.Mismatches) > 0 || receipt.ToolError {
		return DomainAttention, EvidenceNone, true
	}
	return "", EvidenceNone, false
}

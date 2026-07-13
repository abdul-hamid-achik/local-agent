package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
)

type semanticOutputRecorder struct {
	projection ecosystem.ToolProjection
	result     string
}

func (*semanticOutputRecorder) StreamText(string)                            {}
func (*semanticOutputRecorder) StreamReasoning(string)                       {}
func (*semanticOutputRecorder) StreamDone(int, int)                          {}
func (*semanticOutputRecorder) ToolCallStart(string, string, map[string]any) {}
func (*semanticOutputRecorder) SystemMessage(string)                         {}
func (*semanticOutputRecorder) Error(string)                                 {}
func (r *semanticOutputRecorder) ToolCallResult(_ string, _ string, result string, _ bool, _ time.Duration) {
	r.result = result
}
func (r *semanticOutputRecorder) ToolCallSemanticResult(_ string, _ string, result string, _ bool, _ time.Duration, projection ecosystem.ToolProjection) {
	r.result, r.projection = result, projection
}

func TestEmitSemanticToolResultDoesNotForwardRawStructuredContent(t *testing.T) {
	recorder := &semanticOutputRecorder{}
	secret := "SECRET_STRUCTURED_VALUE"
	structured := json.RawMessage(`{"schema_version":1,"ok":true,"clean":false,"conflict_count":0,"private":"` + secret + `"}`)
	projection := projectSemanticToolReceipt(
		"bob__bob_check", nil, "repository checked", structured, nil, false, false, false,
	)
	emitSemanticToolResult(
		recorder,
		"call-1", "bob__bob_check", "repository checked", structured,
		false, false, time.Millisecond, projection,
	)
	if recorder.projection.Domain != ecosystem.DomainDrift || recorder.projection.Transport != ecosystem.TransportSucceeded {
		t.Fatalf("semantic projection = %#v", recorder.projection)
	}
	encoded, err := json.Marshal(recorder.projection)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.result != ecosystem.SafeReceiptText(projection) || strings.Contains(string(encoded), secret) || strings.Contains(recorder.result, secret) {
		t.Fatalf("raw structured content crossed output boundary: projection=%s result=%q", encoded, recorder.result)
	}
}

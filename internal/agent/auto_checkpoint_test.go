package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	executionpkg "github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestAutoIterationLimitReturnsProductiveCheckpoint(t *testing.T) {
	workspace := t.TempDir()
	responses := make([][]llm.StreamChunk, 3)
	for index := range responses {
		name := fmt.Sprintf("progress-%d.txt", index)
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("ok"), 0o600); err != nil {
			t.Fatal(err)
		}
		responses[index] = []llm.StreamChunk{{
			ToolCalls: []llm.ToolCall{{
				ID: fmt.Sprintf("exists-%d", index), Name: "exists",
				Arguments: map[string]any{"path": name},
			}},
			Done: true, EvalCount: index + 1,
		}}
	}

	ag := New(&scriptedClient{responses: responses}, nil, 4096)
	ag.SetWorkDir(workspace)
	ag.SetAuthorityMode(AuthorityAutoScoped)
	ag.SetToolsConfig(config.ToolsConfig{MaxIterations: 3, AutoMaxIterations: 3})
	out := &outputRecorder{}
	err := ag.RunTurn(context.Background(), out, "turn_productive_checkpoint")

	var checkpoint *AutoIterationCheckpointError
	if !errors.As(err, &checkpoint) || !errors.Is(err, ErrAutoIterationCheckpoint) {
		t.Fatalf("RunTurn error = %T %v, want productive AUTO checkpoint", err, err)
	}
	if checkpoint.TurnID != "turn_productive_checkpoint" || checkpoint.Iterations != 3 ||
		checkpoint.ToolCalls != 3 || checkpoint.SuccessfulToolCalls != 3 ||
		checkpoint.DistinctSuccessfulCalls != 3 || checkpoint.EvalTokens != 6 ||
		len(checkpoint.ProgressDigest) != sha256.Size*2 ||
		checkpoint.LastTool != "exists" || checkpoint.LastEffect != executionpkg.EffectReadOnly ||
		checkpoint.LastDomain != ecosystem.DomainSucceeded || checkpoint.Elapsed <= 0 {
		t.Fatalf("checkpoint metadata = %#v", checkpoint)
	}
	if joined := strings.Join(out.errors, "\n"); strings.Contains(joined, "reached max iterations") {
		t.Fatalf("productive checkpoint rendered as an error: %s", joined)
	}
}

func TestAutoProgressDigestIsOpaqueOrderIndependentAndChangesWithProgress(t *testing.T) {
	first := map[string]struct{}{
		"read\x00aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {},
		"grep\x00bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": {},
	}
	second := map[string]struct{}{
		"grep\x00bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": {},
		"read\x00aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {},
	}
	firstDigest := autoProgressDigest(first)
	secondDigest := autoProgressDigest(second)
	if firstDigest != secondDigest {
		t.Fatalf("digest depends on map/insertion order: %q != %q", firstDigest, secondDigest)
	}
	if len(firstDigest) != sha256.Size*2 || strings.Contains(firstDigest, "read") || strings.Contains(firstDigest, "aaaa") {
		t.Fatalf("digest is not bounded and opaque: %q", firstDigest)
	}
	second["exists\x00cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"] = struct{}{}
	if changed := autoProgressDigest(second); changed == firstDigest {
		t.Fatalf("digest did not change after distinct progress: %q", changed)
	}
	if got := autoProgressDigest(nil); got != "" {
		t.Fatalf("empty progress digest = %q, want empty", got)
	}
}

func TestAutoIterationCheckpointRejectsRepeatedOrBlockedWork(t *testing.T) {
	t.Run("repeated exact read is not progress", func(t *testing.T) {
		responses := make([][]llm.StreamChunk, 3)
		for index := range responses {
			responses[index] = []llm.StreamChunk{{
				ToolCalls: []llm.ToolCall{{
					ID: fmt.Sprintf("exists-%d", index), Name: "exists",
					Arguments: map[string]any{"path": "."},
				}},
				Done: true,
			}}
		}
		ag := New(&scriptedClient{responses: responses}, nil, 4096)
		ag.SetWorkDir(t.TempDir())
		ag.SetAuthorityMode(AuthorityAutoScoped)
		ag.SetToolsConfig(config.ToolsConfig{MaxIterations: 3, AutoMaxIterations: 3})
		err := ag.Run(context.Background(), &outputRecorder{})
		if errors.Is(err, ErrAutoIterationCheckpoint) || err == nil ||
			!strings.Contains(err.Error(), "reached max iterations (3)") {
			t.Fatalf("repeated work error = %T %v", err, err)
		}
	})

	t.Run("blocked write is terminal", func(t *testing.T) {
		ag := New(&scriptedClient{responses: [][]llm.StreamChunk{{{
			ToolCalls: []llm.ToolCall{{
				ID: "outside-write", Name: "write",
				Arguments: map[string]any{"path": "../outside.txt", "content": "no"},
			}},
			Done: true,
		}}}}, nil, 4096)
		ag.SetWorkDir(t.TempDir())
		ag.SetAuthorityMode(AuthorityAutoScoped)
		ag.SetToolsConfig(config.ToolsConfig{MaxIterations: 1, AutoMaxIterations: 1})
		err := ag.Run(context.Background(), &outputRecorder{})
		if errors.Is(err, ErrAutoIterationCheckpoint) || err == nil ||
			!strings.Contains(err.Error(), "reached max iterations (1)") {
			t.Fatalf("blocked work error = %T %v", err, err)
		}
	})
}

func TestAutoIterationCheckpointRequiresExactSuccessfulDomain(t *testing.T) {
	tests := []struct {
		name       string
		terminal   executionpkg.EventType
		projection ecosystem.ToolProjection
	}{
		{
			name: "unknown domain", terminal: executionpkg.EventCompleted,
			projection: ecosystem.ToolProjection{
				Transport: ecosystem.TransportSucceeded, Domain: ecosystem.DomainUnknown,
			},
		},
		{
			name: "typed blocked domain", terminal: executionpkg.EventCompleted,
			projection: ecosystem.ToolProjection{
				Transport: ecosystem.TransportSucceeded, Domain: ecosystem.DomainBlocked, DomainTyped: true,
			},
		},
		{
			name: "untyped success", terminal: executionpkg.EventCompleted,
			projection: ecosystem.ToolProjection{
				Transport: ecosystem.TransportSucceeded, Domain: ecosystem.DomainSucceeded,
			},
		},
		{
			name: "failed terminal", terminal: executionpkg.EventFailed,
			projection: ecosystem.ToolProjection{
				Transport: ecosystem.TransportSucceeded, Domain: ecosystem.DomainSucceeded, DomainTyped: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			progress := newAutoTurnProgress()
			progress.beginIteration(1)
			progress.settle("tool", "hash", executionpkg.EffectReadOnly, tt.terminal, tt.projection)
			if checkpoint := progress.checkpoint("turn", 40, 1, time.Second); checkpoint != nil {
				t.Fatalf("unsafe state produced checkpoint: %#v", checkpoint)
			}
		})
	}
}

func TestInteractiveIterationLimitRemainsTerminal(t *testing.T) {
	ag := New(&scriptedClient{responses: [][]llm.StreamChunk{{{
		ToolCalls: []llm.ToolCall{{ID: "exists", Name: "exists", Arguments: map[string]any{"path": "."}}},
		Done:      true,
	}}}}, nil, 4096)
	ag.SetWorkDir(t.TempDir())
	ag.SetAuthorityMode(AuthorityNormal)
	ag.SetToolsConfig(config.ToolsConfig{MaxIterations: 1, AutoMaxIterations: 1})
	err := ag.Run(context.Background(), &outputRecorder{})
	if errors.Is(err, ErrAutoIterationCheckpoint) || err == nil ||
		!strings.Contains(err.Error(), "reached max iterations (1)") {
		t.Fatalf("interactive limit error = %T %v", err, err)
	}
}

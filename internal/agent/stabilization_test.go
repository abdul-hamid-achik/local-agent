package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

type scriptedClient struct {
	responses [][]llm.StreamChunk
	calls     int
}

func (c *scriptedClient) ChatStream(_ context.Context, _ llm.ChatOptions, fn func(llm.StreamChunk) error) error {
	if c.calls >= len(c.responses) {
		return nil
	}
	chunks := c.responses[c.calls]
	c.calls++
	for _, chunk := range chunks {
		if err := fn(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (c *scriptedClient) Ping() error   { return nil }
func (c *scriptedClient) Model() string { return "test-model" }
func (c *scriptedClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}

type outputRecorder struct {
	toolResults []string
}

func (o *outputRecorder) StreamText(string)                    {}
func (o *outputRecorder) StreamDone(int, int)                  {}
func (o *outputRecorder) ToolCallStart(string, map[string]any) {}
func (o *outputRecorder) ToolCallResult(_ string, result string, _ bool, _ time.Duration) {
	o.toolResults = append(o.toolResults, result)
}
func (o *outputRecorder) SystemMessage(string) {}
func (o *outputRecorder) Error(string)         {}

func TestModeToolPolicies_BlockMutationOutsideBuild(t *testing.T) {
	cases := []struct {
		name        string
		policy      ToolPolicy
		expectWrite bool
	}{
		{name: "ask blocks write", policy: AskToolPolicy()},
		{name: "plan blocks write", policy: PlanToolPolicy()},
		{name: "build allows write", policy: BuildToolPolicy(), expectWrite: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			target := filepath.Join(tmpDir, "out.txt")

			client := &scriptedClient{
				responses: [][]llm.StreamChunk{
					{{
						ToolCalls: []llm.ToolCall{{
							ID:   "call-1",
							Name: "write",
							Arguments: map[string]any{
								"path":    "out.txt",
								"content": "hello from policy test",
							},
						}},
						Done: true,
					}},
					{{Text: "done", Done: true}},
				},
			}

			ag := New(client, nil, 0)
			ag.SetWorkDir(tmpDir)
			ag.SetModeContext("test", tt.policy)
			ag.AddUserMessage("write the output file")

			out := &outputRecorder{}
			ag.Run(context.Background(), out)

			data, err := os.ReadFile(target)
			if tt.expectWrite {
				if err != nil {
					t.Fatalf("expected write to succeed: %v", err)
				}
				if string(data) != "hello from policy test" {
					t.Fatalf("unexpected file content: %q", string(data))
				}
				return
			}

			if err == nil {
				t.Fatalf("expected write to be blocked, file content=%q", string(data))
			}
			if !strings.Contains(strings.Join(out.toolResults, "\n"), "blocked in current mode") {
				t.Fatalf("expected blocked tool result, got %v", out.toolResults)
			}
		})
	}
}

func TestHandleRead_TruncationCount(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne"), 0o644); err != nil {
		t.Fatal(err)
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(tmpDir)

	got, isErr := ag.handleRead(map[string]any{
		"path":  path,
		"limit": 2,
	})
	if isErr {
		t.Fatalf("handleRead returned error: %s", got)
	}
	if !strings.Contains(got, "... (3 more lines)") {
		t.Fatalf("expected remaining line count, got %q", got)
	}
}

func TestHandleFind_ShellWildcards(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"main.go", "mainxgo", "file1.txt", "file12.txt"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(tmpDir)

	got, isErr := ag.handleFind(map[string]any{"name": "*.go"})
	if isErr {
		t.Fatalf("handleFind returned error: %s", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Fatalf("expected literal dot match, got %q", got)
	}
	if strings.Contains(got, "mainxgo") {
		t.Fatalf("pattern should not treat '.' as wildcard, got %q", got)
	}

	got, isErr = ag.handleFind(map[string]any{"name": "file?.txt"})
	if isErr {
		t.Fatalf("handleFind returned error: %s", got)
	}
	if !strings.Contains(got, "file1.txt") {
		t.Fatalf("expected single-character wildcard match, got %q", got)
	}
	if strings.Contains(got, "file12.txt") {
		t.Fatalf("single-character wildcard should not match multiple chars, got %q", got)
	}
}

func TestHandleGlob_RecursiveDoubleStar(t *testing.T) {
	tmpDir := t.TempDir()
	files := []string{
		filepath.Join(tmpDir, "main.go"),
		filepath.Join(tmpDir, "nested", "inner.go"),
		filepath.Join(tmpDir, "nested", "note.txt"),
	}
	for _, name := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ag := New(nil, nil, 0)
	ag.SetWorkDir(tmpDir)

	got, isErr := ag.handleGlob(map[string]any{"pattern": "**/*.go"})
	if isErr {
		t.Fatalf("handleGlob returned error: %s", got)
	}
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "nested/inner.go") {
		t.Fatalf("expected recursive glob matches, got %q", got)
	}
	if strings.Contains(got, "note.txt") {
		t.Fatalf("unexpected non-matching file in output: %q", got)
	}
}

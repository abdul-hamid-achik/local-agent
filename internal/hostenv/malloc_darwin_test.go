//go:build darwin

package hostenv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMallocFilterRestoresStderrAndReturns(t *testing.T) {
	if os.Getenv("LOCAL_AGENT_TEST_MALLOC_RESTORE") == "1" {
		restore := FilterMallocDiagnostics()
		fmt.Fprintln(os.Stderr, "during filter")
		restore()
		fmt.Fprintln(os.Stderr, "after restore")
		return
	}
	// File descriptor 2 is process-wide; exercise it in an isolated child.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMallocFilterRestoresStderrAndReturns$")
	cmd.Env = append(os.Environ(), "LOCAL_AGENT_TEST_MALLOC_RESTORE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stderr restoration failed or hung: %v\n%s", err, out)
	}
	for _, want := range []string{"during filter", "after restore"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("stderr lost %q: %s", want, out)
		}
	}
}

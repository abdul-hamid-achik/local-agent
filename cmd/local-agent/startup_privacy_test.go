package main

import (
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/memory"
)

func TestStartupLegacyMemoryInventoryNeverClaims(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	legacyPath := memory.DefaultPathForWorkspace("")
	legacy := memory.NewStore(legacyPath)
	if _, err := legacy.Save("provenance-free", nil); err != nil {
		t.Fatal(err)
	}

	notice := legacyMemoryQuarantineNotice(workspace)
	if !strings.Contains(notice, "1 provenance-free memories quarantined") {
		t.Fatalf("startup notice = %q", notice)
	}
	for _, path := range []string{
		legacyPath + ".pre-workspace.bak",
		legacyPath + ".workspace-claim.json",
		memory.DefaultPathForWorkspace(workspace),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("read-only startup inventory created %s: %v", path, err)
		}
	}
	if got := memory.NewStore(legacyPath).Count(); got != 1 {
		t.Fatalf("startup inventory changed global legacy memory count to %d", got)
	}
}

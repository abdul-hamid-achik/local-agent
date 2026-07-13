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

func TestInteractiveStartupDefersLegacyMemoryDiagnosticsUntilRequested(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	legacy := memory.NewStore(memory.DefaultPathForWorkspace(""))
	if _, err := legacy.Save("provenance-free", nil); err != nil {
		t.Fatal(err)
	}

	if notice := legacyMemoryNoticeForLaunch(workspace, false); notice != "" {
		t.Fatalf("interactive startup exposed optional maintenance state: %q", notice)
	}
	if notice := legacyMemoryNoticeForLaunch(workspace, true); !strings.Contains(notice, "1 provenance-free memories quarantined") {
		t.Fatalf("headless diagnostic was lost: %q", notice)
	}
	if got := memory.NewStore(memory.DefaultPathForWorkspace("")).Count(); got != 1 {
		t.Fatalf("launch projection changed global legacy memory count to %d", got)
	}
}

func TestStartupLegacyMemoryClaimedByAnotherWorkspaceIsSilent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	legacyPath := memory.DefaultPathForWorkspace("")
	legacy := memory.NewStore(legacyPath)
	if _, err := legacy.Save("workspace A fact", nil); err != nil {
		t.Fatal(err)
	}

	preview, err := memory.PreviewDefaultLegacyForWorkspace(workspaceA)
	if err != nil {
		t.Fatal(err)
	}
	result, err := memory.ClaimPreviewedDefaultLegacyForWorkspace(workspaceA, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Claimed {
		t.Fatalf("claim result = %#v", result)
	}

	if notice := legacyMemoryQuarantineNotice(workspaceB); notice != "" {
		t.Fatalf("completed claim produced startup noise: %q", notice)
	}
	if _, err := os.Stat(memory.DefaultPathForWorkspace(workspaceB)); !os.IsNotExist(err) {
		t.Fatalf("silent inventory created workspace B store: %v", err)
	}
	for _, path := range []string{result.MarkerPath, result.BackupPath, preview.ScopedPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("completed claim artifact %s changed: %v", path, err)
		}
	}
}

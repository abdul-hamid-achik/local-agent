package ui

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func TestNewToolCardStylesUsesLightDarkPalette(t *testing.T) {
	tests := []struct {
		name   string
		isDark bool
		want   map[string]string
	}{
		{
			name:   "light",
			isDark: false,
			want: map[string]string{
				"border running": "#5e81ac",
				"border success": "#4f8f38",
				"border error":   "#c94f4f",
				"title running":  "#4f8f8f",
				"title success":  "#4f8f38",
				"title error":    "#c94f4f",
				"args":           "#4c566a",
				"result":         "#4c566a",
				"error":          "#c94f4f",
				"dimmed":         "#5b6779",
				"elapsed":        "#5e81ac",
			},
		},
		{
			name:   "dark",
			isDark: true,
			want: map[string]string{
				"border running": "#81a1c1",
				"border success": "#a3be8c",
				"border error":   "#bf616a",
				"title running":  "#88c0d0",
				"title success":  "#a3be8c",
				"title error":    "#bf616a",
				"args":           "#d8dee9",
				"result":         "#d8dee9",
				"error":          "#bf616a",
				"dimmed":         "#8b97ad",
				"elapsed":        "#81a1c1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			styles := NewToolCardStyles(tt.isDark)
			got := map[string]lipgloss.Style{
				"border running": styles.BorderRunning,
				"border success": styles.BorderSuccess,
				"border error":   styles.BorderError,
				"title running":  styles.TitleRunning,
				"title success":  styles.TitleSuccess,
				"title error":    styles.TitleError,
				"args":           styles.Args,
				"result":         styles.Result,
				"error":          styles.Error,
				"dimmed":         styles.Dimmed,
				"elapsed":        styles.Elapsed,
			}

			for name, style := range got {
				assertToolCardForeground(t, name, style, tt.want[name])
			}
		})
	}
}

func TestToolCardManagerSetDarkUpdatesStylesAndPreservesCallID(t *testing.T) {
	mgr := NewToolCardManager(false)
	mgr.AddCardWithID("call-42", "read_file", ToolCardFile, time.Now())

	if len(mgr.Cards) != 1 {
		t.Fatalf("card count = %d, want 1", len(mgr.Cards))
	}
	assertToolCardForeground(t, "light running title", mgr.Cards[0].Styles.TitleRunning, "#4f8f8f")

	mgr.SetDark(true)

	card := mgr.Cards[0]
	if card.ID != "call-42" {
		t.Fatalf("call ID = %q, want %q", card.ID, "call-42")
	}
	if !mgr.IsDark {
		t.Fatal("manager should use the dark theme")
	}
	assertToolCardForeground(t, "dark running title", card.Styles.TitleRunning, "#88c0d0")
}

func assertToolCardForeground(t *testing.T, name string, style lipgloss.Style, wantHex string) {
	t.Helper()

	got := style.GetForeground()
	want := lipgloss.Color(wantHex)
	gotR, gotG, gotB, gotA := got.RGBA()
	wantR, wantG, wantB, wantA := want.RGBA()
	if gotR != wantR || gotG != wantG || gotB != wantB || gotA != wantA {
		t.Errorf("%s foreground = %#v, want %s", name, got, wantHex)
	}
}

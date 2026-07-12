package ui

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func TestNewToolCardStylesUsesLightDarkPalette(t *testing.T) {
	previous := noColor
	noColor = false
	t.Cleanup(func() { noColor = previous })

	tests := []struct {
		name   string
		isDark bool
		want   map[string]string
	}{
		{
			name:   "light",
			isDark: false,
			want: map[string]string{
				"border running": "#50759f",
				"border success": "#477f33",
				"border error":   "#c34848",
				"title running":  "#447c7c",
				"title success":  "#477f33",
				"title error":    "#c34848",
				"args":           "#4c566a",
				"result":         "#4c566a",
				"error":          "#c34848",
				"dimmed":         "#5b6779",
				"elapsed":        "#50759f",
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
	previous := noColor
	noColor = false
	t.Cleanup(func() { noColor = previous })

	mgr := NewToolCardManager(false)
	mgr.AddCardWithID("call-42", "read_file", ToolCardFile, time.Now())

	if len(mgr.Cards) != 1 {
		t.Fatalf("card count = %d, want 1", len(mgr.Cards))
	}
	assertToolCardForeground(t, "light running title", mgr.Cards[0].Styles.TitleRunning, "#447c7c")

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

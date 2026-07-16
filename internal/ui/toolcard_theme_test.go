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
				"border running":   "#50759f",
				"border success":   "#477f33",
				"border attention": "#8a6500",
				"border error":     "#c34848",
				"title running":    "#447c7c",
				"title success":    "#477f33",
				"title attention":  "#8a6500",
				"title error":      "#c34848",
				"args":             "#4c566a",
				"result":           "#4c566a",
				"error":            "#c34848",
				"warning":          "#8a6500",
				"dimmed":           "#5b6779",
				"elapsed":          "#50759f",
				"diff added":       "#477f33",
				"diff removed":     "#c34848",
				"diff header":      "#447c7c",
				"search path":      "#447c7c",
				"search location":  "#5b6779",
				"search match":     "#7b5a83",
			},
		},
		{
			name:   "dark",
			isDark: true,
			want: map[string]string{
				"border running":   "#81a1c1",
				"border success":   "#a3be8c",
				"border attention": "#ebcb8b",
				"border error":     "#bf616a",
				"title running":    "#88c0d0",
				"title success":    "#a3be8c",
				"title attention":  "#ebcb8b",
				"title error":      "#bf616a",
				"args":             "#d8dee9",
				"result":           "#d8dee9",
				"error":            "#bf616a",
				"warning":          "#ebcb8b",
				"dimmed":           "#8b97ad",
				"elapsed":          "#81a1c1",
				"diff added":       "#a3be8c",
				"diff removed":     "#bf616a",
				"diff header":      "#88c0d0",
				"search path":      "#88c0d0",
				"search location":  "#8b97ad",
				"search match":     "#b48ead",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			styles := NewToolCardStyles(tt.isDark)
			got := map[string]lipgloss.Style{
				"border running":   styles.BorderRunning,
				"border success":   styles.BorderSuccess,
				"border attention": styles.BorderAttention,
				"border error":     styles.BorderError,
				"title running":    styles.TitleRunning,
				"title success":    styles.TitleSuccess,
				"title attention":  styles.TitleAttention,
				"title error":      styles.TitleError,
				"args":             styles.Args,
				"result":           styles.Result,
				"error":            styles.Error,
				"warning":          styles.Warning,
				"dimmed":           styles.Dimmed,
				"elapsed":          styles.Elapsed,
				"diff added":       styles.DiffAdded,
				"diff removed":     styles.DiffRemoved,
				"diff header":      styles.DiffHeader,
				"search path":      styles.SearchPath,
				"search location":  styles.SearchLocation,
				"search match":     styles.SearchMatch,
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

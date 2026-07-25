package ui

import (
	"fmt"
	"image/color"
	"math"
	"testing"
)

// Every registered theme must be legible in both modes. A theme is a set of
// answers to one semantic vocabulary, so a new scheme cannot be added without
// its foregrounds clearing WCAG AA for normal text — measured against that
// scheme's own darkest surface, since Local Agent never paints a background.
func TestThemeForegroundsMeetContrastInBothModes(t *testing.T) {
	previous := noColor
	noColor = false
	t.Cleanup(func() { noColor = previous })

	for _, id := range themeIDs() {
		theme := resolveTheme(id)
		t.Run(theme.ID, func(t *testing.T) {
			modes := []struct {
				name       string
				isDark     bool
				background color.Color
			}{
				{name: "light", background: color.White},
				{name: "dark", isDark: true, background: hexColor(t, theme.DarkReference)},
			}
			for _, mode := range modes {
				t.Run(mode.name, func(t *testing.T) {
					palette := newSemanticPalette(mode.isDark, theme.ID)
					foregrounds := []struct {
						name  string
						color color.Color
					}{
						{name: "dim", color: palette.Dim},
						{name: "muted", color: palette.Muted},
						{name: "text", color: palette.Text},
						{name: "accent", color: palette.Accent},
						{name: "accent2", color: palette.Accent2},
						{name: "error", color: palette.Error},
						{name: "success", color: palette.Success},
						{name: "special", color: palette.Special},
						{name: "warning", color: palette.Warning},
					}
					for _, foreground := range foregrounds {
						t.Run(foreground.name, func(t *testing.T) {
							const minimumContrast = 4.5
							ratio := contrastRatio(foreground.color, mode.background)
							if ratio < minimumContrast {
								t.Fatalf("%s/%s %s contrast = %.2f:1, want >= %.1f:1",
									theme.ID, mode.name, foreground.name, ratio, minimumContrast)
							}
						})
					}
				})
			}
		})
	}
}

// The default theme must stay the one the product has always shipped, and an
// unknown ID must never leave the UI colorless.
func TestThemeResolutionFallsBackToTheDefault(t *testing.T) {
	if got := resolveTheme("no-such-theme").ID; got != defaultThemeID {
		t.Fatalf("unknown theme resolved to %q, want %q", got, defaultThemeID)
	}
	if got := resolveTheme("").ID; got != defaultThemeID {
		t.Fatalf("empty theme resolved to %q, want %q", got, defaultThemeID)
	}
	if got := resolveTheme("  CATPPUCCIN  ").ID; got != "catppuccin" {
		t.Fatalf("theme id is not normalized: %q", got)
	}
	if knownThemeID("no-such-theme") {
		t.Fatal("knownThemeID accepted an unregistered theme")
	}
	if ids := themeIDs(); len(ids) == 0 || ids[0] != defaultThemeID {
		t.Fatalf("theme listing must lead with the default: %v", ids)
	}
}

func hexColor(t *testing.T, value string) color.Color {
	t.Helper()
	if len(value) != 7 || value[0] != '#' {
		t.Fatalf("invalid test color %q", value)
	}
	var red, green, blue uint8
	if _, err := fmt.Sscanf(value, "#%02x%02x%02x", &red, &green, &blue); err != nil {
		t.Fatalf("parse test color %q: %v", value, err)
	}
	return color.RGBA{R: red, G: green, B: blue, A: 0xff}
}

func contrastRatio(a, b color.Color) float64 {
	aLuminance := relativeLuminance(a)
	bLuminance := relativeLuminance(b)
	light, dark := math.Max(aLuminance, bLuminance), math.Min(aLuminance, bLuminance)
	return (light + 0.05) / (dark + 0.05)
}

func relativeLuminance(value color.Color) float64 {
	red, green, blue, _ := value.RGBA()
	linear := func(component uint32) float64 {
		srgb := float64(component) / 65535.0
		if srgb <= 0.04045 {
			return srgb / 12.92
		}
		return math.Pow((srgb+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(red) + 0.7152*linear(green) + 0.0722*linear(blue)
}

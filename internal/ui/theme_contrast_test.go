package ui

import (
	"fmt"
	"image/color"
	"math"
	"testing"
)

func TestSemanticForegroundsMeetNormalTextContrastInBothThemes(t *testing.T) {
	previous := noColor
	noColor = false
	t.Cleanup(func() { noColor = previous })

	themes := []struct {
		name       string
		isDark     bool
		background color.Color
	}{
		{name: "light", background: color.White},
		// Local Agent does not paint over the terminal background. Nord's
		// darkest surface is the conservative reference used by the dark
		// palette instead of assuming pure black.
		{name: "dark", isDark: true, background: hexColor(t, "#2E3440")},
	}

	for _, theme := range themes {
		t.Run(theme.name, func(t *testing.T) {
			palette := newSemanticPalette(theme.isDark)
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
					ratio := contrastRatio(foreground.color, theme.background)
					if ratio < minimumContrast {
						t.Fatalf("%s %s contrast = %.2f:1, want >= %.1f:1",
							theme.name, foreground.name, ratio, minimumContrast)
					}
				})
			}
		})
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

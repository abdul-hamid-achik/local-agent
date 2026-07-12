package ui

import (
	"image/color"
	"math"
	"testing"
)

func TestLightSemanticForegroundsMeetNormalTextContrast(t *testing.T) {
	previous := noColor
	noColor = false
	t.Cleanup(func() { noColor = previous })

	palette := newSemanticPalette(false)
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
			ratio := contrastRatio(foreground.color, color.White)
			if ratio < minimumContrast {
				t.Fatalf("light %s contrast on white = %.2f:1, want >= %.1f:1", foreground.name, ratio, minimumContrast)
			}
		})
	}
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

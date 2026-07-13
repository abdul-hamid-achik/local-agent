package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/ui"
)

func parseHeadlessMode(value string, headless bool) (ui.Mode, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "normal"
	}
	if !headless && value != "normal" {
		return ui.ModeNormal, fmt.Errorf("--mode currently applies to -p headless runs")
	}
	switch value {
	case "normal":
		return ui.ModeNormal, nil
	case "plan":
		return ui.ModePlan, nil
	case "auto":
		return ui.ModeAuto, nil
	default:
		return ui.ModeNormal, fmt.Errorf("unknown authority %q (want normal, plan, or auto)", value)
	}
}

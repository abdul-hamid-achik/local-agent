//go:build windows || plan9 || js || wasip1

package tui

import "os/exec"

// Non-Unix platforms retain exec.CommandContext's single-process fallback.
func configureTUICommandProcessGroup(*exec.Cmd) {}

func cleanupTUICommandProcessGroup(*exec.Cmd) {}

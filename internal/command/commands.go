package command

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// RegisterBuiltins adds all built-in slash commands to the registry.
func RegisterBuiltins(r *Registry) {
	registerGoalActions(r)

	r.Register(&Command{
		Name:        "help",
		Aliases:     []string{"h", "?"},
		Description: "Show help overlay with shortcuts and commands",
		Handler: func(_ *Context, _ []string) Result {
			return Result{Action: ActionShowHelp}
		},
	})

	r.Register(&Command{
		Name:        "clear",
		Aliases:     []string{"new"},
		Description: "Clear conversation history",
		Handler: func(_ *Context, _ []string) Result {
			return Result{
				Text:   "Conversation cleared.",
				Action: ActionClear,
			}
		},
	})

	r.Register(&Command{
		Name:        "goal",
		Aliases:     []string{"g"},
		Description: "Create, inspect, pause, resume, budget, or drop a durable goal",
		Usage:       "/goal [<duration> <prompt>|new [objective]|show|pause|resume|budget|drop]",
		Handler: func(ctx *Context, args []string) Result {
			if ctx == nil {
				ctx = &Context{}
			}
			if len(args) == 0 {
				if ctx.GoalConfigured {
					return Result{Action: ActionShowGoal}
				}
				return Result{Action: ActionOpenGoal}
			}

			if spec, ok := r.MatchAction("goal", args[0]); ok {
				if spec.ID == GoalActionNew {
					if state := resolveActionState(spec, ctx); !state.Enabled {
						return Result{Error: state.DisabledReason}
					}
					promptArgs := args[1:]
					request, err := parseGoalRequest(promptArgs)
					if err != nil {
						return Result{Error: err.Error()}
					}
					if request == nil {
						return Result{Action: spec.Action}
					}
					return Result{Action: spec.Action, Data: request.Prompt, Goal: request}
				}
				if len(args) != 1 {
					return Result{Error: "usage: " + spec.CommandText()}
				}
				if state := resolveActionState(spec, ctx); !state.Enabled {
					return Result{Error: state.DisabledReason}
				}
				return Result{Action: spec.Action}
			}
			switch args[0] {
			default:
				// A free-form suffix is the shortest path from `/goal ship the
				// release` to the reviewed form. Lifecycle subcommands remain
				// closed so a flag typo cannot become a state transition.
				if len(args) == 1 && strings.HasPrefix(args[0], "-") {
					return Result{Error: "usage: /goal [new [objective]|show|pause|resume|budget|drop]"}
				}
				if spec, exists := r.Action(GoalActionNew); exists {
					if state := resolveActionState(spec, ctx); !state.Enabled {
						return Result{Error: state.DisabledReason}
					}
				}
				request, err := parseGoalRequest(args)
				if err != nil {
					return Result{Error: err.Error()}
				}
				return Result{Action: ActionOpenGoal, Data: request.Prompt, Goal: request}
			}
		},
	})

	r.Register(&Command{
		Name:        "model",
		Aliases:     []string{"m", "models", "ml"},
		Description: "Show, switch, or list models",
		Usage:       "/model [name|list|auto]",
		Handler: func(ctx *Context, args []string) Result {
			if len(args) == 0 {
				return Result{Action: ActionShowModelPicker}
			}

			switch args[0] {
			case "auto":
				return Result{
					Text:   "Automatic model routing enabled",
					Action: ActionEnableAutoModel,
				}
			case "list", "ls":
				var b strings.Builder
				b.WriteString("Available models:\n")
				for _, m := range ctx.ModelList {
					marker := "  "
					if m == ctx.Model {
						marker = "* "
					}
					fmt.Fprintf(&b, "  %s%s\n", marker, m)
				}
				b.WriteString("\n* = current")
				return Result{Text: b.String()}

			default:
				for _, m := range ctx.ModelList {
					if m == args[0] {
						return Result{
							Text:   fmt.Sprintf("Switching to model: %s", m),
							Action: ActionSwitchModel,
							Data:   m,
						}
					}
				}
				return Result{Error: fmt.Sprintf("Unknown model: %s (use /model list to see available)", args[0])}
			}
		},
	})

	r.Register(&Command{
		Name:        "recover",
		Description: "Review a paused execution and record typed evidence",
		Usage:       "/recover",
		Handler: func(_ *Context, args []string) Result {
			if len(args) != 0 {
				return Result{Error: "usage: /recover"}
			}
			return Result{Action: ActionRecoverExecution}
		},
	})

	r.Register(&Command{
		Name:        "agent",
		Aliases:     []string{"a"},
		Description: "Show or switch agent profile",
		Usage:       "/agent [name|list]",
		Handler: func(ctx *Context, args []string) Result {
			if len(args) == 0 || args[0] == "list" {
				var b strings.Builder
				if len(ctx.AgentList) == 0 {
					b.WriteString("No agent profiles found in ~/.agents/agents/")
					return Result{Text: b.String()}
				}
				b.WriteString("Available agent profiles:\n")
				for _, a := range ctx.AgentList {
					marker := "  "
					if a == ctx.AgentProfile {
						marker = "* "
					}
					fmt.Fprintf(&b, "  %s%s\n", marker, a)
				}
				b.WriteString("\n* = current")
				return Result{Text: b.String()}
			}

			for _, a := range ctx.AgentList {
				if a == args[0] {
					return Result{
						Text:   fmt.Sprintf("Switching to agent: %s", a),
						Action: ActionSwitchAgent,
						Data:   a,
					}
				}
			}
			return Result{Error: fmt.Sprintf("Unknown agent: %s (use /agent list to see available)", args[0])}
		},
	})

	r.Register(&Command{
		Name:        "load",
		Aliases:     []string{"l"},
		Description: "Load a markdown file as context",
		Usage:       "/load <path>",
		Handler: func(_ *Context, args []string) Result {
			if len(args) == 0 {
				return Result{Error: "Usage: /load <path>"}
			}
			path := expandHomePath(strings.TrimSpace(strings.Join(args, " ")))
			return Result{
				Text:   fmt.Sprintf("Loading context from: %s", path),
				Action: ActionLoadContext,
				Data:   path,
			}
		},
	})

	r.Register(&Command{
		Name:        "unload",
		Description: "Remove loaded context file",
		Handler: func(ctx *Context, _ []string) Result {
			if ctx.LoadedFile == "" {
				return Result{Text: "No context file loaded."}
			}
			return Result{
				Text:   "Context unloaded.",
				Action: ActionUnloadContext,
			}
		},
	})

	r.Register(&Command{
		Name:        "skill",
		Aliases:     []string{"sk"},
		Description: "Manage skills (list, activate, deactivate)",
		Usage:       "/skill [list|activate|deactivate] [name]",
		Handler: func(ctx *Context, args []string) Result {
			if len(args) == 0 || args[0] == "list" {
				return skillList(ctx)
			}
			if len(args) < 2 {
				return Result{Error: "Usage: /skill [list|activate|deactivate] <name>"}
			}
			switch args[0] {
			case "activate", "on":
				return Result{
					Text:   fmt.Sprintf("Activated skill: %s", args[1]),
					Action: ActionActivateSkill,
					Data:   args[1],
				}
			case "deactivate", "off":
				return Result{
					Text:   fmt.Sprintf("Deactivated skill: %s", args[1]),
					Action: ActionDeactivateSkill,
					Data:   args[1],
				}
			default:
				return Result{Error: fmt.Sprintf("Unknown skill action: %s (use list, activate, or deactivate)", args[0])}
			}
		},
	})

	r.Register(&Command{
		Name:        "servers",
		Description: "List connected MCP servers",
		Handler: func(ctx *Context, _ []string) Result {
			if len(ctx.ServerNames) == 0 {
				return Result{Text: "No MCP servers connected."}
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Connected servers (%d):\n", len(ctx.ServerNames))
			for _, name := range ctx.ServerNames {
				fmt.Fprintf(&b, "  - %s\n", name)
			}
			fmt.Fprintf(&b, "\nTotal tools: %d", ctx.ToolCount)
			return Result{Text: b.String()}
		},
	})

	r.Register(&Command{
		Name:        "ice",
		Description: "Show Infinite Context Engine status",
		Handler: func(ctx *Context, _ []string) Result {
			if !ctx.ICEEnabled {
				return Result{Text: "ICE is not enabled. Add `ice: {enabled: true}` to your config.yaml"}
			}
			var b strings.Builder
			b.WriteString("Infinite Context Engine (ICE)\n")
			fmt.Fprintf(&b, "  Status:        enabled\n")
			fmt.Fprintf(&b, "  Conversations: %d stored\n", ctx.ICEConversations)
			fmt.Fprintf(&b, "  Session ID:    %s\n", ctx.ICESessionID)
			fmt.Fprintf(&b, "  Embed model:   nomic-embed-text\n")
			return Result{Text: b.String()}
		},
	})

	r.Register(&Command{
		Name:        "sessions",
		Aliases:     []string{"ss"},
		Description: "Browse and restore saved sessions",
		Handler: func(_ *Context, _ []string) Result {
			return Result{Action: ActionShowSessions}
		},
	})

	r.Register(&Command{
		Name:        "changes",
		Description: "List files modified by the agent this session",
		Handler: func(ctx *Context, _ []string) Result {
			if len(ctx.FileChanges) == 0 {
				return Result{Text: "No files modified this session."}
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Files modified (%d):\n", len(ctx.FileChanges))
			for path, count := range ctx.FileChanges {
				if count > 1 {
					fmt.Fprintf(&b, "  ✎ %s (%dx)\n", path, count)
				} else {
					fmt.Fprintf(&b, "  ✎ %s\n", path)
				}
			}
			return Result{Text: b.String()}
		},
	})

	r.Register(&Command{
		Name:        "commit",
		Aliases:     []string{"ci"},
		Description: "Generate commit message from staged changes and commit",
		Handler: func(_ *Context, args []string) Result {
			return Result{Action: ActionCommit, Data: strings.Join(args, " ")}
		},
	})

	r.Register(&Command{
		Name:        "stats",
		Description: "Show token usage statistics for this session",
		Handler: func(ctx *Context, _ []string) Result {
			if ctx.SessionTurnCount == 0 {
				return Result{Text: "No token usage recorded yet."}
			}
			var b strings.Builder
			b.WriteString("Session Token Stats\n")
			fmt.Fprintf(&b, "  Model:           %s\n", ctx.CurrentModel)
			fmt.Fprintf(&b, "  Turns:           %d\n", ctx.SessionTurnCount)
			fmt.Fprintf(&b, "  Output tokens:   %d\n", ctx.SessionEvalTotal)
			fmt.Fprintf(&b, "  Prompt processed:%8d\n", ctx.SessionPromptTotal)
			if ctx.LatestPromptTokens > 0 {
				fmt.Fprintf(&b, "  Current request:%8d\n", ctx.LatestPromptTokens)
			}
			if ctx.NumCtx > 0 {
				fmt.Fprintf(&b, "  Context window:  %d\n", ctx.NumCtx)
				if ctx.LatestPromptTokens > 0 {
					pct := min(100, max(0, ctx.LatestPromptTokens*100/ctx.NumCtx))
					fmt.Fprintf(&b, "  Context used:    %d%%\n", pct)
				}
			}
			avgOut := ctx.SessionEvalTotal / ctx.SessionTurnCount
			fmt.Fprintf(&b, "  Avg out/turn:    %d\n", avgOut)
			return Result{Text: b.String()}
		},
	})

	r.Register(&Command{
		Name:        "export",
		Description: "Export readable Markdown with a typed v2 transcript",
		Usage:       "/export [--force] <path>",
		Handler: func(_ *Context, args []string) Result {
			force := len(args) > 0 && args[0] == "--force"
			if force {
				args = args[1:]
			}
			path := expandHomePath(strings.TrimSpace(strings.Join(args, " ")))
			if path == "" {
				return Result{Error: "usage: /export [--force] <filepath>"}
			}
			return Result{
				Text:   fmt.Sprintf("Exporting conversation to: %s", path),
				Action: ActionExport,
				Data:   path,
				Force:  force,
			}
		},
	})

	r.Register(&Command{
		Name:        "import",
		Description: "Import a typed v2 transcript into a fresh session",
		Usage:       "/import [path]",
		Handler: func(_ *Context, args []string) Result {
			path := expandHomePath(strings.TrimSpace(strings.Join(args, " ")))
			if path == "" {
				return Result{Error: "usage: /import <filepath>"}
			}
			return Result{
				Text:   fmt.Sprintf("Importing conversation from: %s", path),
				Action: ActionImport,
				Data:   path,
			}
		},
	})

	r.Register(&Command{
		Name:        "checkpoint",
		Aliases:     []string{"cp"},
		Description: "Save a checkpoint of the current conversation",
		Usage:       "/checkpoint [label]",
		Handler: func(_ *Context, args []string) Result {
			return Result{Action: ActionCheckpoint, Data: strings.Join(args, " ")}
		},
	})

	r.Register(&Command{
		Name:        "checkpoints",
		Description: "List saved checkpoints (use /restore <id> to rewind)",
		Handler: func(_ *Context, _ []string) Result {
			return Result{Action: ActionListCheckpoints}
		},
	})

	r.Register(&Command{
		Name:        "restore",
		Description: "Restore the conversation to a saved checkpoint",
		Usage:       "/restore <id>",
		Handler: func(_ *Context, args []string) Result {
			if len(args) < 1 || args[0] == "" {
				return Result{Error: "usage: /restore <id> — see /checkpoints for ids"}
			}
			return Result{Action: ActionRestoreCheckpoint, Data: args[0]}
		},
	})

	r.Register(&Command{
		Name:        "exit",
		Aliases:     []string{"quit", "q"},
		Description: "Quit local-agent",
		Handler: func(_ *Context, _ []string) Result {
			return Result{Action: ActionQuit}
		},
	})
}

func parseGoalRequest(args []string) (*GoalRequest, error) {
	if len(args) == 0 {
		return nil, nil
	}
	promptStart := 0
	request := &GoalRequest{}
	if duration, err := time.ParseDuration(args[0]); err == nil {
		if duration <= 0 {
			return nil, errors.New("goal duration must be positive")
		}
		request.TimeBudget = duration
		request.TimeExplicit = true
		promptStart = 1
	} else if looksLikeGoalDuration(args[0]) {
		return nil, fmt.Errorf("invalid goal duration %q; use Go duration syntax such as 30m or 1h30m", args[0])
	}
	request.Prompt = strings.TrimSpace(strings.Join(args[promptStart:], " "))
	if request.TimeExplicit && request.Prompt == "" {
		return nil, errors.New("usage: /goal <duration> <prompt>")
	}
	return request, nil
}

// looksLikeGoalDuration distinguishes a mistyped leading duration from a
// free-form objective. It recognizes Go's units plus common long-form units,
// while leaving numeric prompts such as "2026 roadmap" untouched.
func looksLikeGoalDuration(value string) bool {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return false
	}
	index := 0
	if runes[index] == '+' || runes[index] == '-' {
		index++
	}
	if index >= len(runes) || runes[index] < '0' || runes[index] > '9' {
		return false
	}

	sawUnit := false
	for index < len(runes) {
		digits := 0
		dots := 0
		for index < len(runes) {
			r := runes[index]
			if r >= '0' && r <= '9' {
				digits++
				index++
				continue
			}
			if r == '.' && dots == 0 {
				dots++
				index++
				continue
			}
			break
		}
		if digits == 0 {
			return sawUnit
		}
		unitStart := index
		for index < len(runes) {
			r := runes[index]
			if (r >= 'a' && r <= 'z') || r == 'µ' || r == 'μ' {
				index++
				continue
			}
			break
		}
		if unitStart == index {
			return sawUnit
		}
		if !isGoalDurationUnit(string(runes[unitStart:index])) {
			return sawUnit
		}
		sawUnit = true
	}
	return sawUnit
}

func isGoalDurationUnit(unit string) bool {
	switch unit {
	case "ns", "us", "µs", "μs", "ms", "s", "m", "h",
		"sec", "secs", "second", "seconds",
		"min", "mins", "minute", "minutes",
		"hr", "hrs", "hour", "hours",
		"d", "day", "days":
		return true
	default:
		return false
	}
}

func expandHomePath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

func skillList(ctx *Context) Result {
	if len(ctx.Skills) == 0 {
		return Result{Text: "No skills found. Add .md files to ~/.config/local-agent/skills/"}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skills (%d):\n", len(ctx.Skills))
	for _, s := range ctx.Skills {
		status := "  "
		if s.Active {
			status = "* "
		}
		fmt.Fprintf(&b, "  %s%s — %s\n", status, s.Name, s.Description)
	}
	b.WriteString("\n* = active")
	return Result{Text: b.String()}
}

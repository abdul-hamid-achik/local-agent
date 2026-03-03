package command

import (
	"fmt"
	"os"
	"strings"
)

const maxContextFileSize = 32 * 1024 // 32KB

// RegisterBuiltins adds all built-in slash commands to the registry.
func RegisterBuiltins(r *Registry) {
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
		Description: "Clear conversation history",
		Handler: func(_ *Context, _ []string) Result {
			return Result{
				Text:   "Conversation cleared.",
				Action: ActionClear,
			}
		},
	})

	r.Register(&Command{
		Name:        "new",
		Description: "Start a fresh conversation",
		Handler: func(_ *Context, _ []string) Result {
			return Result{
				Text:   "New conversation started.",
				Action: ActionClear,
			}
		},
	})

	r.Register(&Command{
		Name:        "model",
		Aliases:     []string{"m"},
		Description: "Show, switch, or list models",
		Usage:       "/model [name|list|fast|smart]",
		Handler: func(ctx *Context, args []string) Result {
			if len(args) == 0 {
				return Result{Action: ActionShowModelPicker}
			}

			switch args[0] {
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

			case "fast":
				if len(ctx.ModelList) > 0 {
					return Result{
						Text:   fmt.Sprintf("Switching to fastest model: %s", ctx.ModelList[0]),
						Action: ActionSwitchModel,
						Data:   ctx.ModelList[0],
					}
				}
				return Result{Error: "No models available"}

			case "smart":
				if len(ctx.ModelList) > 0 {
					smartModel := ctx.ModelList[len(ctx.ModelList)-1]
					return Result{
						Text:   fmt.Sprintf("Switching to smartest model: %s", smartModel),
						Action: ActionSwitchModel,
						Data:   smartModel,
					}
				}
				return Result{Error: "No models available"}

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
		Name:        "models",
		Aliases:     []string{"ml"},
		Description: "Open model picker",
		Handler: func(_ *Context, _ []string) Result {
			return Result{Action: ActionShowModelPicker}
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
			path := strings.Join(args, " ")

			// Expand ~ to home directory.
			if strings.HasPrefix(path, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					path = home + path[1:]
				}
			}

			info, err := os.Stat(path)
			if err != nil {
				return Result{Error: fmt.Sprintf("Cannot access %s: %v", path, err)}
			}
			if info.Size() > maxContextFileSize {
				return Result{Error: fmt.Sprintf("File too large (%d bytes, max %d)", info.Size(), maxContextFileSize)}
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return Result{Error: fmt.Sprintf("Cannot read %s: %v", path, err)}
			}

			return Result{
				Text:   fmt.Sprintf("Loaded context: %s (%d bytes)", path, len(data)),
				Action: ActionLoadContext,
				Data:   path + "\x00" + string(data), // path\0content
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
			b.WriteString(fmt.Sprintf("Connected servers (%d):\n", len(ctx.ServerNames)))
			for _, name := range ctx.ServerNames {
				fmt.Fprintf(&b, "  - %s\n", name)
			}
			b.WriteString(fmt.Sprintf("\nTotal tools: %d", ctx.ToolCount))
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
			fmt.Fprintf(&b, "  Prompt tokens:   %d (last turn)\n", ctx.SessionPromptTotal)
			if ctx.NumCtx > 0 {
				fmt.Fprintf(&b, "  Context window:  %d\n", ctx.NumCtx)
				pct := ctx.SessionPromptTotal * 100 / ctx.NumCtx
				fmt.Fprintf(&b, "  Context used:    %d%%\n", pct)
			}
			avgOut := ctx.SessionEvalTotal / ctx.SessionTurnCount
			fmt.Fprintf(&b, "  Avg out/turn:    %d\n", avgOut)
			return Result{Text: b.String()}
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

func skillList(ctx *Context) Result {
	if len(ctx.Skills) == 0 {
		return Result{Text: "No skills found. Add .md files to ~/.config/local-agent/skills/"}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Skills (%d):\n", len(ctx.Skills)))
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

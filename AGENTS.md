# AGENTS.md

Global instructions for the local-agent project.

## TUI Development Rules

- **Always use Charm libraries** for all TUI components: [BubbleTea v2](https://charm.land/bubbletea/v2), [Bubbles v2](https://charm.land/bubbles/v2), [Lip Gloss v2](https://charm.land/lipgloss/v2), [Glamour](https://github.com/charmbracelet/glamour).
- Prefer existing Bubbles components (spinner, viewport, textarea, textinput, list, table, paginator, progress, stopwatch, timer, key) over custom implementations.
- Follow the Charm "smart parent, dumb child" pattern: the main `Model` processes all messages; child components expose methods returning `tea.Cmd`.
- Use `lipgloss.LightDark()` for adaptive theming. Never hardcode ANSI colors.
- Render cached content where possible to avoid per-frame re-rendering overhead.

## Code Style

- Go 1.25+, idiomatic Go conventions
- Tests for all new TUI components and message handlers
- Protect shared state with `sync.RWMutex`
- Use `context.Context` for cancellable operations

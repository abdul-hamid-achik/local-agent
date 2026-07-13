# AGENTS.md

Global instructions for the local-agent project.

## Public Website Boundary

- `docs/` is the source for the public website at [local-agent.dev](https://local-agent.dev). Treat every file under it as intentionally publishable.
- Keep `docs/` as a standalone product and documentation website. Add only landing-page copy, public user documentation, or public static assets.
- Do not create ADRs under `docs/` or anywhere else in this repository. Store project ADRs in `~/notes/projects/local-agent/adrs/`; `docs/architecture.md` may summarize only the stable public architecture contract.
- Never link the public website to private Notes paths or assume those files ship with the repository.
- Never place handoffs, scratch notes, implementation plans, agent transcripts, generated diagnostics, test artifacts, private paths, credentials, or internal-only operational material in `docs/`.
- Put temporary or internal material outside `docs/` in an appropriate ignored workspace location. If a document is not meant to be public, stop and choose another location.
- Keep public claims factual and source-backed. Do not invent adoption numbers, benchmarks, testimonials, compatibility, release status, or security guarantees.
- Website changes must preserve responsive layout, keyboard navigation, accessible contrast, reduced-motion behavior, canonical metadata, and a successful production build.

## TUI Development Rules

- **Always use Charm libraries** for all TUI components: [BubbleTea v2](https://charm.land/bubbletea/v2), [Bubbles v2](https://charm.land/bubbles/v2), [Lip Gloss v2](https://charm.land/lipgloss/v2), [Glamour](https://github.com/charmbracelet/glamour).
- Prefer existing Bubbles components (spinner, viewport, textarea, textinput, list, table, paginator, progress, stopwatch, timer, key) over custom implementations.
- Follow the Charm "smart parent, dumb child" pattern: the main `Model` processes all messages; child components expose methods returning `tea.Cmd`.
- Use `lipgloss.LightDark()` for adaptive theming. Never hardcode ANSI colors.
- Render cached content where possible to avoid per-frame re-rendering overhead.
- Never render a successful MCP transport as domain success or verified evidence. Use the bounded `internal/ecosystem` projection and keep transport, domain, and evidence states separate.
- Keep raw MCP `StructuredContent` inside the agent parser boundary. Do not concatenate it into transcript text or persist it in UI/session state.

## Code Style

- Go 1.25+, idiomatic Go conventions
- Tests for all new TUI components and message handlers
- Protect shared state with `sync.RWMutex`
- Use `context.Context` for cancellable operations

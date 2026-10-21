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

Git auto-builds **`main` only** (`vercel.json` at repo root). Feature branches do not create Preview deployments. `ignoreCommand` skips non-docs commits. Do not `vercel promote`; `main` is the site release. The CLI ships from tags, not from a docs deploy.

## Bun monorepo

- The TypeScript rewrite is experimental. The Go source was retired at the user’s request. Historical releases remain available; removal does not establish feature parity. Preserve the historical reference in `reference/GO_IMPLEMENTATION.md` while completing the migration.
- Use Bun 1.4.2 workspaces, TypeScript 7, and exact external dependency versions. Run `bun install --frozen-lockfile` and `bun run check`.
- Use native package import aliases (`#cli/*`, `#runtime/*`, `#tui/*`, `#playground/*`) within each workspace and `@local-agent/*` exports across workspaces. Do not reach into a sibling's source with relative paths.
- Unit/component tests use Vitest through `bun run test`, not `bun test`. Integration contracts use Glyphrun against deterministic loopback fixtures; never inherit live provider credentials or invoke real inference in CI.
- `apps/cli` composes the application. `packages/runtime` owns inference and future host authority; it must not import Vue, terminal libraries, or application code.
- `packages/vue-tui` is an internal, independently extractable framework. It must not import the runtime, CLI, or Chalupa. Tuimark (driven from Bun over `tuimark host`) is the target UI. Vue TermUI/OpenTUI remains only until the Tuimark client replaces it, the inline mode included.
- Fullscreen, native-scrollback inline, and headless are different presentations. Headless must not initialize the native renderer. Inline must not pretend a viewport is native scrollback.
- Layout belongs to containers; child components use the space provided. Test resize, input, and cleanup with the terminal test renderer when changing components.
- Framework/CLI `.ts` files are checked by TypeScript 7. `apps/tui-playground` exercises `.vue` SFCs through the official Vue TermUI Vite plugin. Its template checker is isolated on `vue-tsc` 3.3.11 + TypeScript 6.0.3 because that checker cannot access TypeScript 7's compiler API; never silently skip template checking.
- Chalupa owns infrastructure, placement and tunnels. Consume its process-scoped provider profile; never provision, destroy, or infer GPU ownership from a loopback URL.
- `docs/` stays in place as the VitePress workspace. Its public claims describe released functionality, not the experimental client. Run `bun run site:build` after dependency or website changes.

- MCP transport success is not domain success or verified evidence. Keep raw structured content inside the parser boundary, never in transcript or session state.

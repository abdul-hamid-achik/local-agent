# Local Agent Workbench (Electron)

Project workbench for this repository. It reviews and exercises every feature of
the Bun client through the real `@local-agent/runtime` code paths — no mocks.

## Architecture

- `src/renderer/` — Vue 3 UI (Vite). Views: Chat, Sessions, Recovery,
  Workspace, MCP, Providers, Project, Reference.
- `src/main/` — Electron main process (Node). Owns the window, dialogs, and
  the sidecar lifecycle. It never imports the runtime.
- `src/sidecar/sidecar.ts` — Bun JSON-RPC host over stdio. This is the only
  place that imports `@local-agent/runtime`, so `bun:sqlite` and the
  fd-pinned workspace keep running on Bun.
- `src/shared/protocol.ts` — RPC shapes shared by main, sidecar and renderer.

The runtime stays Bun-only; Electron/Node only relays framed messages.

## Run

```bash
bun install --frozen-lockfile
bun run --cwd apps/desktop dev
```

`dev` builds main/preload, starts the Vite renderer on `127.0.0.1:5174`, and
opens Electron with the sidecar attached to this checkout. Production load
uses `dist-renderer/index.html` via `bun run --cwd apps/desktop build`.

Packaging (`bun run --cwd apps/desktop dist`) produces a macOS build, but the
sidecar still runs against a repository checkout with Bun 1.4.2 on `PATH`:
standalone distribution remains out of scope for the experimental client.

## Icon

`build/icon.svg` adapts the `docs/public/logo.svg` motif to a 1024 square.
`build/icon.png` and `build/icon.icns` are committed generated artifacts.
Regenerate on macOS with:

```bash
qlmanage -t -s 1024 -o /tmp/iconwork apps/desktop/build/icon.svg
cp /tmp/iconwork/icon.svg.png apps/desktop/build/icon.png
# ... then sips-resize an icon.iconset and `iconutil -c icns`
```

## Requirements

- Bun 1.4.2 on `PATH` (sidecar host).
- A configured provider for agent turns: local Ollama on loopback, or a
  Chalupa process profile (`LOCAL_AGENT_PROVIDER=chalupa`). Review views
  (sessions, recovery, workspace, MCP status, project) work without a model.

---
title: Testing and verification
description: Verify the Bun workspaces with Vitest, TypeScript and deterministic Glyphrun terminal contracts.
outline: deep
currentImplementation: true
---

# Testing and verification

Current development uses Bun 1.4.2 workspaces. The Go implementation and its
release automation were retired from the working tree; parity remains incomplete.
Historical behavior is documented in the
[Go implementation reference](https://github.com/abdul-hamid-achik/local-agent/blob/main/reference/GO_IMPLEMENTATION.md).

## Repository verification

```bash
bun install --frozen-lockfile
bun run check
bun run site:build
bun run site:check-links
```

`check` runs Biome, TypeScript 7, the isolated Vue SFC template checker,
Vitest, the CLI workspace build and the Vue playground build. Tests use
`bun run test`, not `bun test`. The rendered-site checker verifies same-origin
links, assets and HTML anchors without making network requests.

## Terminal contracts

Install Glyphrun v0.23.0 and run:

```bash
go install github.com/abdul-hamid-achik/glyphrun/cmd/glyph@v0.23.0
bun run test:integration
```

The Go toolchain here installs the external Glyphrun runner; it does not build
Local Agent. Contracts under `specs/bun/` use deterministic loopback fixtures.
They exercise fullscreen resize, native inline scrollback, headless output,
approval and denial, command verification and clean exit. They do not download
models or contact GPUs. Native component tests also exercise input and cleanup.

## Continuous integration

The workflow verifies Bun on Linux and macOS and builds the website separately.
External package versions are exact and installation uses the frozen lockfile.
The site job also audits dependencies and checks rendered links after building.
A green workflow establishes the behavior covered by those tests; it does not
establish complete parity with historical Go features or a standalone release.

## Compiler boundary

Framework and CLI `.ts` files use TypeScript 7.0.2. Vue SFCs in the playground
use `vue-tsc` 3.3.11 with its isolated TypeScript 6.0.3 dependency because its
checker requires compiler APIs unavailable in TypeScript 7. Both checks run in
`bun run typecheck`; template checking is not skipped.

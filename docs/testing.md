---
title: Testing and verification
description: Verify Local Agent with Go race tests, linting, vulnerability checks, and deterministic Glyphrun terminal contracts.
outline: deep
---

# Testing and verification

Local Agent combines code-level Go gates with black-box terminal behavior specs.

## Repository verification

```bash
task verify
```

The verification task runs:

- the VitePress production website build;
- `go mod tidy -diff`;
- `golangci-lint`;
- `go vet`;
- the complete Go test suite with the race detector;
- `govulncheck`.

## Terminal behavior

[Glyphrun](https://glyphrun.dev/) drives the compiled application through a real pseudo-terminal and verifies user-visible outcomes against a deterministic terminal emulator.

```bash
task glyphrun
```

Committed scenarios cover normal and minimum terminal sizes, authority modes, goal review and receipts, the Ollama inventory, approval decisions, composer paste behavior, sessions, and Help.

Refresh snapshots only for an intentional visual change:

```bash
task glyphrun-snapshots
git diff -- .glyphrun/snapshots
```

Snapshots are evidence of rendering; they are not a substitute for outcome assertions.

## Optional live Ollama proof

With `qwen3.5:4b` installed, run the opt-in small-model tool scenario separately:

```bash
glyph run specs/glyphrun/live_ollama_tool.yml --format md
```

This proof depends on a live local model and is intentionally outside the deterministic default suite.

## Related evidence tools

- [Cairntrace](https://cairntrace.dev/) describes and verifies browser behavior.
- [Vidtrace](https://vidtrace.dev/) converts bug recordings into timestamped evidence bundles.
- [file.cheap](https://file.cheap) stores and compares agent workflow artifacts.

These are related tools in the wider ecosystem, not bundled Local Agent dependencies.

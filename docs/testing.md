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

- the VitePress production website build and rendered internal-link check;
- `go mod tidy -diff`;
- `golangci-lint`;
- `go vet`;
- the complete Go test suite with the race detector;
- `govulncheck` v1.6.0.

`task verify` covers the local Go and public-site gates. Glyphrun has separate
fast and full tasks because it exercises the compiled program through a
pseudo-terminal.

## Pinned versions

The repository currently fixes these versions or version ranges:

| Surface | Repository contract |
|---|---|
| Application toolchain | Go 1.25.12 from `go.mod` |
| Website runtime | Node 22 or newer; CI uses Node 24 |
| Website packages | VitePress 1.6.4 and Vue 3.5.39 |
| CI linter | golangci-lint 2.12.2 |
| Vulnerability scanner | govulncheck 1.6.0 in `Taskfile.yml` and CI |
| CLI terminal contracts | Glyphrun 0.14.0 in CI, installed with Go 1.26.x; local-agent is then rebuilt with the Go 1.25.12 application toolchain |

The local `golangci-lint` and `glyph` tasks use the executables on `PATH`; use
the CI versions above when reproducing CI exactly.

## Continuous integration

The verification workflow has four independent jobs:

- Linux Go verification: module diff, golangci-lint, vet, race tests, and
  govulncheck;
- VitePress production build plus a test of links in the rendered site;
- the fast Glyphrun CLI contracts from `task glyphrun-cli`;
- a macOS build, `--version` smoke test, and Darwin-sensitive package tests.

## Terminal behavior

[Glyphrun](https://glyphrun.dev/) drives the compiled application through a real pseudo-terminal and verifies user-visible outcomes against a deterministic terminal emulator.

```bash
task glyphrun-cli
task glyphrun
```

`task glyphrun-cli` runs the public flag and skip-approval compatibility specs
and is the Glyphrun subset enforced in CI. `task glyphrun` adds the complete
committed terminal suite.

Committed scenarios cover normal and minimum terminal sizes, authority modes,
public CLI parsing, inline goal review and approvals, goal receipts, the Ollama
inventory, composer paste behavior, sessions, and Help.

Refresh snapshots only for an intentional visual change:

```bash
task glyphrun-snapshots
git diff -- .glyphrun/snapshots
```

Snapshots are evidence of rendering; they are not a substitute for outcome assertions.

## Optional live Ollama proof

With `qwen3.5:0.8b` installed, run the opt-in constrained-model tool scenario separately:

```bash
glyph run specs/live_ollama_tool.yml --format md
```

This proof depends on a live local model and is intentionally outside the deterministic default suite.

## Related evidence tools

- [Cairntrace](https://cairntrace.dev/) describes and verifies browser behavior.
- [Vidtrace](https://vidtrace.dev/) converts bug recordings into timestamped evidence bundles.
- [file.cheap](https://file.cheap) stores and compares agent workflow artifacts.

These are related tools in the wider ecosystem, not bundled Local Agent dependencies.

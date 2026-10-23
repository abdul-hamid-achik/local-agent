#!/bin/sh
# Vercel ignoreCommand (exit 0 skips the deployment, anything else deploys).
# Vercel clones shallowly, so the previously deployed commit can be absent:
# when it is, or when no previous deployment exists, deploy rather than fail.
set -eu
prev="${VERCEL_GIT_PREVIOUS_SHA:-}"
[ -n "$prev" ] || exit 1
git cat-file -e "$prev^{commit}" 2>/dev/null || exit 1
git diff --quiet "$prev" HEAD -- docs/ package.json bun.lock bunfig.toml \
  .bun-version 'apps/*/package.json' 'packages/*/package.json' vercel.json scripts/

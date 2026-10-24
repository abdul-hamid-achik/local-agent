#!/usr/bin/env -S bun --no-env-file --config=/dev/null
// The `local-agent` command: runs src/main.ts with Bun, from the checkout this
// file is in. `bun link` (see the README) puts it on PATH as a symlink, so the
// checkout is found through the real path of this file. The working directory
// is left alone: it is the workspace the agent edits.
//
// That workspace is often a repository nobody has vetted, and Bun reads the
// working directory's bunfig.toml (whose `preload` runs code before this file)
// and .env (which could set LOCAL_AGENT_LOCAL_ONLY=false and a remote
// provider) on startup. The shebang turns both off; the runtime needs neither.

import { realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";

const here = dirname(realpathSync(import.meta.path));
await import(pathToFileURL(join(here, "src/main.ts")).href);

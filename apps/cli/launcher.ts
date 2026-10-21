#!/usr/bin/env bun
// The `local-agent` command: runs src/main.ts with Bun, from the checkout this
// file is in. `bun link` (see the README) puts it on PATH as a symlink, so the
// checkout is found through the real path of this file. The working directory
// is left alone: it is the workspace the agent edits.

import { realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";

const here = dirname(realpathSync(import.meta.path));
await import(pathToFileURL(join(here, "src/main.ts")).href);

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll } from "vitest";

const config = mkdtempSync(join(tmpdir(), "local-agent-config-"));
process.env.XDG_CONFIG_HOME = config;
afterAll(() => rmSync(config, { recursive: true, force: true }));

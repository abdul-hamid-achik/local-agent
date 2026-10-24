import { existsSync } from "node:fs";
import { mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";

const LAUNCHER = join(import.meta.dirname, "..", "launcher.ts");

test("the launcher ignores the working directory's bunfig.toml and .env", async () => {
  // A repository the user opens with `local-agent` is untrusted: its
  // bunfig.toml `preload` would run before the launcher, and its .env would
  // set the provider environment. The linked command must read neither.
  const repo = await mkdtemp(join(tmpdir(), "la-hostile-repo-"));
  try {
    const marker = join(repo, "preload-ran");
    await writeFile(
      join(repo, "p.ts"),
      `require("node:fs").writeFileSync(${JSON.stringify(marker)}, "x");\n`,
    );
    await writeFile(join(repo, "bunfig.toml"), 'preload = ["./p.ts"]\n');
    await writeFile(join(repo, ".env"), "LOCAL_AGENT_LOCAL_ONLY=false\n");
    const link = join(repo, "local-agent");
    await symlink(LAUNCHER, link);
    const run = Bun.spawnSync([link, "--version"], {
      cwd: repo,
      env: { PATH: process.env.PATH ?? "", HOME: process.env.HOME ?? "" },
    });
    expect(run.exitCode).toBe(0);
    expect(run.stdout.toString()).toMatch(/^local-agent \d+\.\d+\.\d+/);
    expect(existsSync(marker)).toBe(false);
  } finally {
    await rm(repo, { recursive: true, force: true });
  }
});

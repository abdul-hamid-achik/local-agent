// Checks a release archive the way a user meets it, from outside this checkout:
//
//   bun scripts/release-smoke.ts dist/release/local-agent_<version>_<os>_<arch>.tar.gz
//
// It unpacks the archive into a temporary folder and runs the executable it
// holds (no Bun, no repository), then fails unless:
//   - `--version` names the version the archive is named for;
//   - ui/ is next to the executable, with the documents, the kit and the
//     themes, and without test material;
//   - in a hostile workspace, the workspace's bunfig.toml preload never runs
//     and its .env is never read. A positive control shows the .env check can
//     fail: the same variable set in the real environment is refused.
// No inference: Ollama is pointed at a closed loopback port.

import { existsSync } from "node:fs";
import { mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";

const archive = Bun.argv[2];
if (!archive) throw new Error("usage: release-smoke.ts ARCHIVE.tar.gz");
const name = basename(archive, ".tar.gz");
const version = /^local-agent_(.+)_[a-z]+_[a-z0-9]+$/.exec(name)?.[1];
if (!version) throw new Error(`not a release archive name: ${name}`);

const failures: string[] = [];
const scratch = await mkdtemp(join(tmpdir(), "local-agent-smoke-"));
try {
  // Where an install puts it: a folder of its own (the archive is flat).
  const folder = join(scratch, "share", name);
  await mkdir(folder, { recursive: true });
  const untar = Bun.spawnSync(["tar", "-xzf", archive, "-C", folder]);
  if (untar.exitCode !== 0) throw new Error(untar.stderr.toString());
  const top = (await readdir(folder)).sort();
  if (top.join(" ") !== "LICENSE README.md local-agent ui")
    failures.push(`the archive holds ${top.join(", ")}`);
  const bin = join(folder, "local-agent");

  const run = (
    args: string[],
    cwd: string,
    env: Record<string, string> = {},
  ) => {
    const child = Bun.spawnSync([bin, ...args], {
      cwd,
      // A clean environment: only what the executable needs to start.
      env: {
        PATH: "/usr/bin:/bin",
        HOME: scratch,
        OLLAMA_HOST: "127.0.0.1:9",
        ...env,
      },
      timeout: 60_000,
    });
    return {
      code: child.exitCode,
      out: child.stdout.toString() + child.stderr.toString(),
    };
  };

  const shown = run(["--version"], scratch);
  if (shown.code !== 0 || shown.out.trim() !== `local-agent ${version}`)
    failures.push(`--version printed ${JSON.stringify(shown.out.trim())}`);

  for (const file of [
    "ui/app.tui",
    "ui/inline.tui",
    "ui/app.tcss",
    "ui/kit/kit.tui",
    "ui/themes/nord.tcss",
    "LICENSE",
  ])
    if (!existsSync(join(folder, file))) failures.push(`missing ${file}`);
  const ui = await readdir(join(folder, "ui"));
  for (const entry of ui)
    if (entry === "golden" || entry === "samples" || entry.startsWith("."))
      failures.push(`ui/${entry} is test material`);

  // A workspace that tries both: a preload that leaves a marker, and an
  // invalid LOCAL_AGENT_CONTEXT that the CLI refuses on sight.
  const workspace = join(scratch, "hostile");
  await Bun.write(join(workspace, ".keep"), "");
  const marker = join(scratch, "preload-ran");
  await writeFile(join(workspace, "bunfig.toml"), 'preload = ["./evil.ts"]\n');
  await writeFile(
    join(workspace, "evil.ts"),
    `require("node:fs").writeFileSync(${JSON.stringify(marker)}, "ran");\n`,
  );
  await writeFile(
    join(workspace, ".env"),
    "LOCAL_AGENT_CONTEXT=dotenv-sentinel\n",
  );
  const refused = /LOCAL_AGENT_CONTEXT must be a token count/;
  const hostile = run(
    ["--mode", "headless", "-p", "hello", "--workspace", workspace],
    workspace,
  );
  if (existsSync(marker))
    failures.push("the workspace's bunfig.toml preload ran");
  if (refused.test(hostile.out))
    failures.push("the workspace's .env was read (LOCAL_AGENT_CONTEXT)");
  const control = run(
    ["--mode", "headless", "-p", "hello", "--workspace", workspace],
    workspace,
    { LOCAL_AGENT_CONTEXT: "env-sentinel" },
  );
  if (!refused.test(control.out))
    failures.push(
      `control: LOCAL_AGENT_CONTEXT in the environment was not refused: ${control.out.trim()}`,
    );
} finally {
  await rm(scratch, { recursive: true, force: true });
}

if (failures.length > 0) {
  for (const failure of failures) console.error(`FAIL ${failure}`);
  process.exit(1);
}
console.log(`ok: ${name}`);

// docs/public/install.sh against a release served from loopback: a fake
// archive whose `local-agent` is a shell script, and its SHA256SUMS.txt.

import { existsSync, readlinkSync } from "node:fs";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, beforeAll, expect, test } from "vitest";
import { platform } from "./release-binary";

const script = join(import.meta.dirname, "../docs/public/install.sh");
const VERSION = "v9.9.9";
const archive = `local-agent_9.9.9_${platform()}.tar.gz`;

let scratch: string;
let server: ReturnType<typeof Bun.serve>;
let sums: string;

beforeAll(async () => {
  scratch = await mkdtemp(join(tmpdir(), "local-agent-install-"));
  const stage = join(scratch, "stage");
  await mkdir(join(stage, "ui"), { recursive: true });
  await writeFile(
    join(stage, "local-agent"),
    '#!/bin/sh\necho "local-agent 9.9.9"\n',
    { mode: 0o755 },
  );
  await writeFile(join(stage, "ui", "app.tui"), "<tui/>");
  for (const file of ["LICENSE", "README.md"])
    await writeFile(join(stage, file), file);
  const tar = Bun.spawnSync([
    "tar",
    "-czf",
    join(scratch, archive),
    "-C",
    stage,
    "local-agent",
    "ui",
    "LICENSE",
    "README.md",
  ]);
  expect(tar.exitCode).toBe(0);
  const hash = new Bun.CryptoHasher("sha256")
    .update(await Bun.file(join(scratch, archive)).arrayBuffer())
    .digest("hex");
  sums = `${hash}  ${archive}\n`;
  server = Bun.serve({
    port: 0,
    hostname: "127.0.0.1",
    fetch: (request) => {
      const path = new URL(request.url).pathname;
      if (path === `/${VERSION}/${archive}`)
        return new Response(Bun.file(join(scratch, archive)));
      if (path === `/${VERSION}/SHA256SUMS.txt`) return new Response(sums);
      return new Response("not found", { status: 404 });
    },
  });
});

afterAll(async () => {
  server.stop(true);
  await rm(scratch, { recursive: true, force: true });
});

// Asynchronous: the server answering the installer runs in this process.
async function install(home: string, version = VERSION) {
  const child = Bun.spawn(["sh", script], {
    env: {
      PATH: "/usr/bin:/bin:/usr/sbin:/sbin",
      HOME: home,
      LOCAL_AGENT_VERSION: version,
      LOCAL_AGENT_DOWNLOAD_BASE: `http://127.0.0.1:${server.port}`,
    },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ]);
  return { stdout, stderr, exitCode };
}

test("the installer checks the archive, unpacks it and links the command", async () => {
  const home = await mkdtemp(join(scratch, "home-"));
  const first = await install(home);
  const out = first.stdout;
  expect(first.exitCode, first.stderr).toBe(0);
  expect(out).toContain("Installed local-agent 9.9.9");
  const link = join(home, ".local/bin/local-agent");
  const target = join(home, ".local/share/local-agent", VERSION);
  expect(readlinkSync(link)).toBe(join(target, "local-agent"));
  expect(existsSync(join(target, "ui", "app.tui"))).toBe(true);
  // ~/.local/bin is not on this PATH: the installer says how to add it.
  expect(out).toContain("is not on your PATH");
  // Again, over the first: the same result, no leftovers.
  expect((await install(home)).exitCode).toBe(0);
  expect(existsSync(`${target}.partial`)).toBe(false);
});

test("an archive that does not match its checksum installs nothing", async () => {
  const home = await mkdtemp(join(scratch, "home-"));
  const good = sums;
  sums = `${"0".repeat(64)}  ${archive}\n`;
  try {
    const run = await install(home);
    expect(run.exitCode).not.toBe(0);
    expect(run.stderr).toContain("does not match its checksum");
    expect(existsSync(join(home, ".local/bin/local-agent"))).toBe(false);
    expect(existsSync(join(home, ".local/share/local-agent", VERSION))).toBe(
      false,
    );
  } finally {
    sums = good;
  }
});

test("a version that is not a plain tag is refused before anything is written", async () => {
  const home = await mkdtemp(join(scratch, "home-"));
  for (const version of ["v1/../../escape", "v1..2", "v1 2"]) {
    const run = await install(home, version);
    expect(run.exitCode, version).not.toBe(0);
    expect(run.stderr).toContain(`not a release tag: ${version}`);
  }
  expect(existsSync(join(home, ".local"))).toBe(false);
});

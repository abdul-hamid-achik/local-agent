import { execFileSync } from "node:child_process";
import {
  mkdir,
  mkdtemp,
  readFile,
  readlink,
  realpath,
  rm,
  stat,
  symlink,
  writeFile,
} from "node:fs/promises";
import { createServer, type Server } from "node:net";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import {
  type CommandResult,
  createCommandProposal,
  executeApprovedCommand,
} from "#runtime/command";
import { bunNodeFolder, sandboxUnavailable } from "#runtime/sandbox";

describe("availability", () => {
  let home: string;
  let workspace: string;
  beforeAll(async () => {
    home = await realpath(await mkdtemp(join(tmpdir(), "la-sbx-avail-")));
    workspace = join(home, "projects", "app");
    await mkdir(workspace, { recursive: true });
  });
  afterAll(() => rm(home, { recursive: true, force: true }));
  const host = (over: Partial<Parameters<typeof sandboxUnavailable>[2]>) => ({
    platform: "darwin" as const,
    exists: () => true,
    probe: () => undefined,
    ...over,
  });

  test("macOS with sandbox-exec, a workspace inside the home folder", () => {
    expect(sandboxUnavailable(workspace, home, host({}))).toBeUndefined();
  });

  test("each reason it cannot run is named", () => {
    expect(
      sandboxUnavailable(workspace, home, host({ platform: "linux" })),
    ).toBe("the command sandbox needs macOS (Seatbelt)");
    expect(
      sandboxUnavailable(workspace, home, host({ exists: () => false })),
    ).toBe("/usr/bin/sandbox-exec is missing");
    for (const root of [home, join(home, ".."), "/"])
      expect(sandboxUnavailable(root, home, host({}))).toBe(
        "the workspace contains your home folder",
      );
    expect(sandboxUnavailable(join(home, "missing"), home, host({}))).toMatch(
      /cannot be resolved/,
    );
    expect(
      sandboxUnavailable(workspace, home, host({ probe: () => "nested" })),
    ).toBe("nested");
  });
});

// macOS CI must really run these: an unavailable sandbox there is a failure,
// not a skip.
const ciMac = process.platform === "darwin" && process.env.CI !== undefined;
const available = sandboxUnavailable(tmpdir()) === undefined;
test.runIf(ciMac)("the sandbox is available on macOS CI", () => {
  expect(sandboxUnavailable(tmpdir())).toBeUndefined();
});

describe.runIf(available)("a sandboxed command", () => {
  let base: string;
  let home: string;
  let workspace: string;
  /** Planted where the sandbox keeps reads from: shared and user temp. */
  let sharedTmp: string;
  let userTmp: string;
  const servers: Server[] = [];
  let port: number;
  let port6: number;

  const listen = (host: string, path?: string) =>
    new Promise<number>((resolve, reject) => {
      const server = createServer((socket) => socket.end("ok"));
      servers.push(server);
      server.on("error", reject);
      if (path) server.listen(path, () => resolve(0));
      else
        server.listen(0, host, () =>
          resolve((server.address() as { port: number }).port),
        );
    });

  beforeAll(async () => {
    // A home folder of the test's own, outside the user's temporary folder
    // so each read rule is tested alone: what the sandbox keeps from a
    // command is planted here, never in the real one.
    base = await realpath(await mkdtemp("/private/var/tmp/la-sbx-"));
    home = join(base, "home");
    workspace = join(home, "projects", "app");
    await mkdir(join(workspace, ".git", "hooks"), { recursive: true });
    await writeFile(join(workspace, ".git", "config"), "[core]\n");
    await mkdir(join(workspace, "vendor", "lib", ".git", "hooks"), {
      recursive: true,
    });
    await writeFile(join(workspace, "vendor", "lib", ".git", "config"), "");
    await writeFile(join(workspace, "notes.txt"), "in the workspace\n");
    await writeFile(join(home, "secret.txt"), "planted secret\n");
    await mkdir(join(home, ".bun"), { recursive: true });
    await writeFile(join(home, ".bun", "tool.txt"), "toolchain\n");
    await mkdir(join(home, ".config", "git"), { recursive: true });
    await writeFile(join(home, ".config", "git", "ignore"), "*.log\n");
    await writeFile(join(home, ".config", "git", "credentials"), "token\n");
    await mkdir(join(home, ".cargo"), { recursive: true });
    await writeFile(join(home, ".cargo", "config.toml"), "[build]\n");
    await writeFile(join(home, ".cargo", "credentials.toml"), "token\n");
    sharedTmp = await realpath(await mkdtemp("/private/tmp/la-sbx-"));
    await writeFile(join(sharedTmp, "planted.txt"), "x");
    userTmp = await realpath(await mkdtemp(join(tmpdir(), "la-sbx-")));
    await writeFile(join(userTmp, "planted.txt"), "x");
    port = await listen("127.0.0.1");
    port6 = await listen("::1");
    await listen("", join(workspace, "s.sock"));
    await listen("", join(base, "o.sock"));
    await symlink(join(base, "o.sock"), join(workspace, "o.sock"));
  });
  afterAll(async () => {
    for (const server of servers)
      await new Promise((resolve) => server.close(resolve));
    for (const folder of [base, sharedTmp, userTmp])
      await rm(folder, { recursive: true, force: true });
  });

  /** Runs a script under Bun in `cwd`, sandboxed unless `confined` is false. */
  async function run(
    script: string,
    { confined = true, cwd = workspace } = {},
  ): Promise<CommandResult> {
    const proposal = await createCommandProposal(
      [process.execPath, "-e", script],
      cwd,
      20_000,
    );
    return executeApprovedCommand(
      proposal,
      AbortSignal.timeout(20_000),
      confined ? { home } : undefined,
    );
  }
  /** What `body` reports: "ok", or the error code it failed with. */
  const attempt = async (
    body: string,
    options: { confined?: boolean; cwd?: string } = {},
  ) => {
    const result = await run(
      `const fs = require("node:fs");
       (async () => { ${body} })().then(() => console.log("ok"), (e) => console.log(e.code ?? e.cause?.code ?? e.message));`,
      options,
    );
    expect(result.sandboxed).toBe(
      options.confined === false ? undefined : true,
    );
    return result.output.trim();
  };
  const read = (path: string) =>
    attempt(`fs.readFileSync(${JSON.stringify(path)})`);

  test("writes only in the workspace", async () => {
    expect(await attempt(`fs.writeFileSync("out.txt", "x")`)).toBe("ok");
    expect(await readFile(join(workspace, "out.txt"), "utf8")).toBe("x");
    for (const outside of [join(home, "out.txt"), join(base, "out.txt")])
      expect(
        await attempt(`fs.writeFileSync(${JSON.stringify(outside)}, "x")`),
      ).toBe("EPERM");
    await expect(stat(join(home, "out.txt"))).rejects.toThrow();
  });

  test("never what runs later as the user, at any depth and in any case", async () => {
    for (const body of [
      `fs.writeFileSync(".git/hooks/pre-commit", "#!/bin/sh")`,
      `fs.appendFileSync(".git/config", "[core]\\nhooksPath = /tmp")`,
      `fs.writeFileSync(".git/commondir", "..")`,
      `fs.renameSync(".git", ".g")`,
      `fs.renameSync(".git/hooks", ".git/h")`,
      `fs.mkdirSync(".git/modules/sub/hooks", { recursive: true })`,
      `fs.mkdirSync(".git/worktrees/x", { recursive: true })`,
      // Attributes pick a driver from the user's own git config.
      `fs.mkdirSync(".git/info", { recursive: true }); fs.writeFileSync(".git/info/attributes", "* diff=x")`,
      `fs.writeFileSync(".gitattributes", "* diff=x")`,
      `fs.writeFileSync("vendor/.GitAttributes", "* diff=x")`,
      `fs.mkdirSync(".local-agent")`,
      // Nor through a link made in the workspace.
      `fs.linkSync(".git/config", "config-link")`,
      `fs.symlinkSync(".git/hooks", "hooks-link"); fs.writeFileSync("hooks-link/pre-commit", "x")`,
      // A repository already nested in the workspace.
      `fs.writeFileSync("vendor/lib/.git/hooks/pre-commit", "#!/bin/sh")`,
      `fs.appendFileSync("vendor/lib/.git/config", "[core]\\nhooksPath = h")`,
      // A new one, by any spelling or route.
      `fs.mkdirSync("n1/.git", { recursive: true })`,
      `fs.mkdirSync("n2/.GIT/hooks", { recursive: true })`,
      `fs.mkdirSync("n3/x", { recursive: true }); fs.renameSync("n3/x", "n3/.Git")`,
      `fs.mkdirSync("n4", { recursive: true }); fs.writeFileSync("n4/.git", "gitdir: ../evil")`,
      `fs.mkdirSync("n5/.Local-Agent", { recursive: true })`,
    ])
      expect(await attempt(body), body).toBe("EPERM");
    expect(await readFile(join(workspace, ".git", "config"), "utf8")).toBe(
      "[core]\n",
    );
    // Git's own data stays writable: a commit works.
    expect(await attempt(`fs.writeFileSync(".git/index.test", "x")`)).toBe(
      "ok",
    );
  });

  test("a workspace path with regular-expression characters keeps the rules", async () => {
    const odd = join(home, "projects", "a+b (x).[y]");
    await mkdir(join(odd, ".git", "hooks"), { recursive: true });
    expect(await attempt(`fs.writeFileSync("ok.txt", "x")`, { cwd: odd })).toBe(
      "ok",
    );
    expect(
      await attempt(`fs.writeFileSync(".git/hooks/pre-commit", "x")`, {
        cwd: odd,
      }),
    ).toBe("EPERM");
  });

  test("reads in the home folder only the workspace and toolchains", async () => {
    expect(await read("notes.txt")).toBe("ok");
    expect(await read(join(home, ".bun", "tool.txt"))).toBe("ok");
    expect(await read(join(home, "secret.txt"))).toBe("EPERM");
    expect(await attempt(`fs.readdirSync(${JSON.stringify(home)})`)).toBe(
      "EPERM",
    );
    // Toolchain folders, but never the tokens kept in them.
    expect(await read(join(home, ".config", "git", "ignore"))).toBe("ok");
    expect(await read(join(home, ".config", "git", "credentials"))).toBe(
      "EPERM",
    );
    expect(await read(join(home, ".cargo", "config.toml"))).toBe("ok");
    expect(await read(join(home, ".cargo", "credentials.toml"))).toBe("EPERM");
    // A link in the workspace does not carry a read out of it.
    expect(
      await attempt(
        `fs.symlinkSync(${JSON.stringify(join(home, "secret.txt"))}, "link"); fs.readFileSync("link")`,
      ),
    ).toBe("EPERM");
  });

  test("reads nothing in temporary folders, other volumes or the user's caches", async () => {
    expect(await read(join(sharedTmp, "planted.txt"))).toBe("EPERM");
    expect(await read(join(userTmp, "planted.txt"))).toBe("EPERM");
    expect(await attempt(`fs.readdirSync("/Volumes")`)).toBe("EPERM");
    const system = await realpath(tmpdir());
    if (basename(system) === "T")
      expect(
        await attempt(
          `fs.readdirSync(${JSON.stringify(join(dirname(system), "C"))})`,
        ),
      ).toBe("EPERM");
  });

  test("gets a HOME, and a TMPDIR and GOCACHE of the sandbox's own", async () => {
    const result = await run(
      `const os = require("node:os"); require("node:fs").writeFileSync(require("node:path").join(os.tmpdir(), "t"), "x");
       console.log(JSON.stringify({ home: process.env.HOME, tmp: process.env.TMPDIR, go: process.env.GOCACHE }))`,
    );
    const env = JSON.parse(result.output) as Record<string, string>;
    const own = join(home, "Library", "Caches", "local-agent", "sandbox");
    expect(env).toEqual({
      home,
      tmp: `${join(own, "tmp")}/`,
      go: join(own, "go-build"),
    });
  });

  test("reaches no network, not even this machine", async () => {
    const connect = (to: string) =>
      `await new Promise((resolve, reject) => { const s = require("node:net").connect(${to}); s.on("connect", resolve); s.on("error", reject); })`;
    // Outside the sandbox the fixtures answer...
    for (const to of [
      `${port}, "127.0.0.1"`,
      `${port6}, "::1"`,
      JSON.stringify(join(workspace, "s.sock")),
    ])
      expect(await attempt(connect(to), { confined: false }), to).toBe("ok");
    // ...inside, nothing does: TCP, a socket in the workspace or linked
    // into it, UDP, or a server of its own.
    // (Bun reports each refused connect as ECONNREFUSED.)
    for (const to of [
      `${port}, "127.0.0.1"`,
      `${port6}, "::1"`,
      `80, "192.0.2.1"`,
      JSON.stringify("s.sock"),
      JSON.stringify("o.sock"),
    ])
      expect(await attempt(connect(to)), to).toBe("ECONNREFUSED");
    expect(
      await attempt(
        `await new Promise((resolve, reject) => { const s = require("node:dgram").createSocket("udp4"); s.on("error", reject); s.send("x", ${port}, "127.0.0.1", (e) => (e ? reject(e) : resolve())); })`,
      ),
    ).toBe("EPERM");
    for (const host of ["127.0.0.1", "0.0.0.0"])
      expect(
        await attempt(
          `await new Promise((resolve, reject) => require("node:net").createServer().on("error", reject).listen(0, ${JSON.stringify(host)}, resolve))`,
        ),
        host,
      ).toBe("EPERM");
  });

  test("bun --bun runs Bun as node, through links the host made", async () => {
    const result = await run(
      `console.log(require("node:child_process").execFileSync(process.execPath, ["--bun", "-e", "console.log(require('node:child_process').execFileSync('node', ['-e', 'console.log(typeof Bun)']).toString().trim())"]).toString().trim())`,
    );
    expect(result.output.trim()).toBe("object");
  });

  test("git status and commit work", async () => {
    execFileSync("git", ["init", "-q", join(base, "repo-src")]);
    const repo = join(home, "projects", "repo");
    execFileSync("git", ["clone", "-q", join(base, "repo-src"), repo]);
    const commit = await createCommandProposal(
      [
        "git",
        "-c",
        "user.name=t",
        "-c",
        "user.email=t@example.com",
        "-c",
        "commit.gpgsign=false",
        "-c",
        "core.hooksPath=/dev/null",
        "commit",
        "-q",
        "--allow-empty",
        "-m",
        "sandboxed",
      ],
      repo,
      20_000,
    );
    const result = await executeApprovedCommand(
      commit,
      AbortSignal.timeout(20_000),
      { home },
    );
    // (Apple's git shim may log once while it fills its cache.)
    expect(result.exitCode).toBe(0);
    expect(
      execFileSync("git", ["-C", repo, "log", "--format=%s"], {
        encoding: "utf8",
      }).trim(),
    ).toBe("sandboxed");
  });

  test("refuses to run when the sandbox is unavailable, never runs unconfined", async () => {
    // The home folder itself as the workspace: nothing would stay unread.
    const proposal = await createCommandProposal(
      [process.execPath, "-e", `require("node:fs").writeFileSync("ran", "x")`],
      home,
      20_000,
    );
    await expect(
      executeApprovedCommand(proposal, AbortSignal.timeout(20_000), { home }),
    ).rejects.toThrow(
      "Command sandbox unavailable: the workspace contains your home folder",
    );
    await expect(stat(join(home, "ran"))).rejects.toThrow();
  });
});

describe("Bun's node links", () => {
  let root: string;
  beforeAll(async () => {
    root = await realpath(await mkdtemp(join(tmpdir(), "la-sbx-bun-")));
  });
  afterAll(() => rm(root, { recursive: true, force: true }));

  test("are made to this Bun, and a link elsewhere is not offered", async () => {
    const folder = bunNodeFolder(process.env.PATH, root);
    expect(folder).toBeDefined();
    const bun = await realpath(
      execFileSync("/usr/bin/which", ["bun"], {
        encoding: "utf8",
        env: { PATH: process.env.PATH },
      }).trim(),
    );
    for (const name of ["node", "bun"])
      expect(await realpath(join(folder ?? "", name))).toBe(bun);
    // Made by someone else, pointing elsewhere: left alone, not offered.
    await rm(join(folder ?? "", "node"));
    await symlink("/usr/bin/true", join(folder ?? "", "node"));
    expect(bunNodeFolder(process.env.PATH, root)).toBeUndefined();
    expect(await readlink(join(folder ?? "", "node"))).toBe("/usr/bin/true");
  });
});

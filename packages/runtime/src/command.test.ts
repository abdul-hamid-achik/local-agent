import { spawn } from "node:child_process";
import {
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  stat,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test, vi } from "vitest";
import {
  type ApprovedCommandProposal,
  createCommandProposal,
  executeApprovedCommand,
  killLiveCommandGroups,
  parseArgv,
  splitCommandLine,
} from "./command";

const roots: string[] = [];
afterEach(async () => {
  vi.restoreAllMocks();
  for (const root of roots.splice(0))
    await rm(root, { recursive: true, force: true });
});
async function temp() {
  const root = await mkdtemp(join(tmpdir(), "local-agent-command-"));
  roots.push(root);
  return root;
}

test("runs argv without a shell and scrubs provider credentials", async () => {
  const root = await temp();
  // The credentials are really there to scrub (the environment is taken
  // when the proposal is made): unset, the test could not fail.
  const planted = ["OLLAMA_HOST", "OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY"];
  const saved = planted.map((name) => process.env[name]);
  for (const name of planted) process.env[name] = "planted";
  const proposal = await createCommandProposal(
    [
      process.execPath,
      "-e",
      "process.stdout.write(JSON.stringify({argv:process.argv.slice(1),env:Object.keys(process.env).sort()}))",
      "literal;$(echo shell)",
    ],
    root,
    2000,
  ).finally(() =>
    planted.forEach((name, index) => {
      const value = saved[index];
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
    }),
  );
  expect(Object.isFrozen(proposal)).toBe(true);
  expect(Object.isFrozen(proposal.argv)).toBe(true);
  const result = await executeApprovedCommand(
    proposal,
    new AbortController().signal,
  );
  expect(result.exitCode).toBe(0);
  expect(result.signal).toBeNull();
  expect(result.timedOut).toBe(false);
  expect(result.cancelled).toBe(false);
  const output = JSON.parse(result.output) as { argv: string[]; env: string[] };
  expect(output.argv).toEqual(["literal;$(echo shell)"]);
  for (const name of planted) expect(output.env).not.toContain(name);
  expect(output.env).toContain("PATH");
  await expect(
    executeApprovedCommand(proposal, new AbortController().signal),
  ).rejects.toThrow("already consumed");
});

test("bounds combined stdout and stderr", async () => {
  const root = await temp();
  const proposal = await createCommandProposal(
    [
      process.execPath,
      "-e",
      "process.stdout.write('o'.repeat(700000)); process.stderr.write('e'.repeat(700000))",
    ],
    root,
    2000,
  );
  const result = await executeApprovedCommand(
    proposal,
    new AbortController().signal,
  );
  expect(result.exitCode).toBe(0);
  expect(Buffer.byteLength(result.output)).toBe(1 << 20);
  expect(result.truncated).toBe(true);
});

test("terminates a process group and its grandchild on timeout", async () => {
  const root = await temp();
  const proposal = await createCommandProposal(
    [
      process.execPath,
      "-e",
      [
        "const {spawn}=require('node:child_process');",
        "const {writeFileSync}=require('node:fs');",
        "spawn(process.execPath,['-e',\"setTimeout(()=>writeFileSync('grandchild-alive','yes'),700)\"],{stdio:'ignore'});",
        "setTimeout(()=>{},10000);",
      ].join(""),
    ],
    root,
    100,
  );
  const result = await executeApprovedCommand(
    proposal,
    new AbortController().signal,
  );
  expect(result.timedOut).toBe(true);
  expect(result.cancelled).toBe(false);
  expect(result.exitCode).toBeNull();
  await new Promise((resolve) => setTimeout(resolve, 900));
  await expect(stat(join(root, "grandchild-alive"))).rejects.toThrow();
});

test("cleans descendants after the leader exits normally", async () => {
  const root = await temp();
  const proposal = await createCommandProposal(
    [
      process.execPath,
      "-e",
      [
        "const {spawn}=require('node:child_process');",
        "const {writeFileSync}=require('node:fs');",
        "const child=spawn(process.execPath,['-e',\"process.on('SIGTERM',()=>{});setTimeout(()=>{},10000)\"],{stdio:'ignore'});child.unref();",
        "writeFileSync('grandchild.pid',String(child.pid));",
      ].join(""),
    ],
    root,
    2000,
  );
  const result = await executeApprovedCommand(
    proposal,
    new AbortController().signal,
  );
  expect(result.exitCode).toBe(0);
  const pid = Number(await readFile(join(root, "grandchild.pid"), "utf8"));
  const deadline = Date.now() + 1000;
  while (Date.now() < deadline) {
    try {
      process.kill(pid, 0);
    } catch {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error(`grandchild ${pid} survived group cleanup`);
});

test("returns output after cleaning descendants that inherit its pipes", async () => {
  const root = await temp();
  const proposal = await createCommandProposal(
    [
      process.execPath,
      "-e",
      "const {spawn}=require('node:child_process');const child=spawn(process.execPath,['-e',\"process.on('SIGTERM',()=>{});setTimeout(()=>{},10000)\"],{stdio:['ignore','inherit','inherit']});child.unref();process.stdout.write('final-result');",
    ],
    root,
    2000,
  );
  const started = Date.now();
  const result = await executeApprovedCommand(
    proposal,
    new AbortController().signal,
  );
  expect(Date.now() - started).toBeLessThan(1500);
  expect(result.exitCode).toBe(0);
  expect(result.output).toContain("final-result");
});

test("bounds drain when a detached grandchild escapes the process group", async () => {
  const root = await temp();
  let escapedPid = 0;
  try {
    const proposal = await createCommandProposal(
      [
        process.execPath,
        "-e",
        [
          "const {spawn}=require('node:child_process');",
          "const {writeFileSync}=require('node:fs');",
          "const child=spawn(process.execPath,['-e',\"setTimeout(()=>{},10000)\"],{detached:true,stdio:['ignore','inherit','inherit']});",
          "child.unref();writeFileSync('escaped.pid',String(child.pid));process.stdout.write('leader-done');",
        ].join(""),
      ],
      root,
      2000,
    );
    const started = Date.now();
    const result = await executeApprovedCommand(
      proposal,
      new AbortController().signal,
    );
    expect(Date.now() - started).toBeLessThan(1500);
    expect(result.exitCode).toBe(0);
    expect(result.output).toContain("leader-done");
    expect(result.cleanupIncomplete).toBe(true);
    escapedPid = Number(await readFile(join(root, "escaped.pid"), "utf8"));
  } finally {
    if (escapedPid > 0) {
      try {
        process.kill(-escapedPid, "SIGKILL");
      } catch {}
      try {
        process.kill(escapedPid, "SIGKILL");
      } catch {}
    }
  }
});

test("aborts a running process and reports cancellation", async () => {
  const root = await temp();
  const controller = new AbortController();
  const proposal = await createCommandProposal(
    [process.execPath, "-e", "setTimeout(()=>{},10000)"],
    root,
    5000,
  );
  const run = executeApprovedCommand(proposal, controller.signal);
  setTimeout(() => controller.abort(), 50);
  const result = await run;
  expect(result.cancelled).toBe(true);
  expect(result.timedOut).toBe(false);
  expect(result.exitCode).toBeNull();
});

test("rejects malformed proposals and non-directory cwd", async () => {
  await expect(createCommandProposal([], process.cwd(), 1000)).rejects.toThrow(
    "argv",
  );
  await expect(
    createCommandProposal([process.execPath], __filename, 1000),
  ).rejects.toThrow("directory");
  const forged = {
    kind: "command",
    argv: [process.execPath],
    cwd: process.cwd(),
    timeoutMs: 1000,
  } as unknown as ApprovedCommandProposal;
  await expect(
    executeApprovedCommand(forged, new AbortController().signal),
  ).rejects.toThrow();
});

test("rejects a cwd replaced after approval", async () => {
  const root = await temp();
  const proposal = await createCommandProposal(
    [process.execPath, "-e", ""],
    root,
    1000,
  );
  await rm(root, { recursive: true, force: true });
  await mkdir(root);
  await expect(
    executeApprovedCommand(proposal, new AbortController().signal),
  ).rejects.toThrow("cwd changed");
});

test("splits a command line a model sent as a string, quote-aware and without a shell", () => {
  expect(splitCommandLine("bun test tests/stats.test.ts")).toEqual([
    "bun",
    "test",
    "tests/stats.test.ts",
  ]);
  expect(splitCommandLine("  grep   -rn  slugify   src ")).toEqual([
    "grep",
    "-rn",
    "slugify",
    "src",
  ]);
  expect(splitCommandLine(`grep -rn "a b" 'c d' e\\ f ""`)).toEqual([
    "grep",
    "-rn",
    "a b",
    "c d",
    "e f",
    "",
  ]);
  // Operators inside quotes are plain text, as are escaped quotes and a lone dollar.
  expect(
    splitCommandLine(
      `grep "a|b" 'x && y;z' "say \\"hi\\"" $HOME 5>6`.replace("5>6", "56"),
    ),
  ).toEqual(["grep", "a|b", "x && y;z", 'say "hi"', "$HOME", "56"]);
  expect(splitCommandLine(`echo 'it'"'"'s'`)).toEqual(["echo", "it's"]);
  expect(splitCommandLine('git commit -m "fix: a \\$(b)"')).toEqual([
    "git",
    "commit",
    "-m",
    "fix: a $(b)",
  ]);
});

test.each([
  ["find . | head -10", "|"],
  ["bun test && bun lint", "&&"],
  ["a ; b", ";"],
  ["a & b", "&"],
  ["cat a > out", ">"],
  ["cat < in", "<"],
  ["echo $(whoami)", "$("],
  ['echo "$(whoami)"', "$("],
  ["echo `whoami`", "backtick"],
  ["a\nb", "second line"],
])("refuses shell syntax in %j", (line) => {
  expect(() => splitCommandLine(line)).toThrow(
    /no shell.*Run one command per call.*"argv"/s,
  );
});

test("refuses unterminated quotes and empty command lines", () => {
  expect(() => splitCommandLine('echo "oops')).toThrow("unterminated");
  expect(() => splitCommandLine("   ")).toThrow("must not be empty");
  expect(() => splitCommandLine("echo \\")).toThrow("backslash");
});

test("parseArgv keeps an array as it is and also reads a JSON array sent as a string", () => {
  expect(parseArgv(["a b", "c"])).toEqual(["a b", "c"]);
  expect(parseArgv('["grep","-rn","a b","src"]')).toEqual([
    "grep",
    "-rn",
    "a b",
    "src",
  ]);
  expect(parseArgv("ls -la")).toEqual(["ls", "-la"]);
});

test("a command whose arguments name a secret path is flagged on its proposal, and still runs", async () => {
  const root = await temp();
  const flagged = await createCommandProposal(
    [
      process.execPath,
      "-e",
      "process.stdout.write(process.argv[1] ?? '')",
      ".env",
      "--env-file=config/.env.production",
      ".env",
    ],
    root,
    2000,
  );
  expect(flagged.secretPaths).toEqual([".env", "config/.env.production"]);
  expect(Object.isFrozen(flagged.secretPaths)).toBe(true);
  // Flagged is not refused: the host decides, and shows the note.
  const result = await executeApprovedCommand(
    flagged,
    new AbortController().signal,
  );
  expect(result.exitCode).toBe(0);
  expect(result.output).toBe(".env");
  for (const argv of [
    ["cat", "README.md"],
    ["cat", ".env.example"],
    ["curl", "https://example.com/.env"],
    ["grep", "-rn", "slugify", "src"],
  ]) {
    const plain = await createCommandProposal(argv, root, 2000);
    expect(plain.secretPaths, argv.join(" ")).toBeUndefined();
    expect("secretPaths" in plain).toBe(false);
  }
});

test("a command that carries a secret path in one argument, or names a link to one, is flagged", async () => {
  const root = await temp();
  const elsewhere = await temp();
  await writeFile(join(root, ".env"), "TOKEN=planted");
  await writeFile(join(root, "plain.txt"), "plain");
  await mkdir(join(root, ".aws"));
  await writeFile(join(root, ".aws", "config"), "x");
  await mkdir(join(elsewhere, ".ssh"));
  await writeFile(join(elsewhere, ".ssh", "id_ed25519"), "key");
  await writeFile(join(elsewhere, "harmless"), "fine");
  await symlink(".env", join(root, "notes.txt"));
  await symlink("ghost/.env", join(root, "dangling.txt"));
  await symlink(".aws", join(root, "cloud"));
  await symlink(join(elsewhere, ".ssh", "id_ed25519"), join(root, "key.txt"));
  await symlink(join(elsewhere, "harmless"), join(root, "out.txt"));
  await symlink("plain.txt", join(root, "alias.txt"));
  const secretsOf = async (...argv: string[]) =>
    (await createCommandProposal(argv, root, 1000)).secretPaths;
  // One argument that embeds a command.
  expect(await secretsOf("sh", "-c", "cat .env")).toEqual([".env"]);
  expect(await secretsOf("python3", "-c", "open('.env').read()")).toEqual([
    ".env",
  ]);
  expect(await secretsOf("curl", "-d@.env", "https://example.com")).toEqual([
    ".env",
  ]);
  // A link whose target the file tools would hide, shown with its target.
  expect(await secretsOf("cat", "notes.txt")).toEqual(["notes.txt -> .env"]);
  expect(await secretsOf("cat", "dangling.txt")).toEqual([
    "dangling.txt -> ghost/.env",
  ]);
  expect(await secretsOf("cat", "cloud/config")).toEqual([
    "cloud/config -> .aws/config",
  ]);
  expect(await secretsOf("cat", "key.txt")).toEqual([
    `key.txt -> ${await realpath(join(elsewhere, ".ssh", "id_ed25519"))}`,
  ]);
  expect(await secretsOf("cat", "--file=notes.txt")).toEqual([
    "notes.txt -> .env",
  ]);
  // Links to ordinary files, and the link's own name, are not.
  expect(await secretsOf("cat", "alias.txt")).toBeUndefined();
  expect(await secretsOf("cat", "out.txt")).toBeUndefined();
  expect(await secretsOf("cat", "plain.txt")).toBeUndefined();
});

function alive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

async function until(done: () => boolean | Promise<boolean>, what: string) {
  for (let i = 0; i < 1000; i++) {
    if (await done()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`Timed out waiting for ${what}`);
}

async function grandchildPid(file: string): Promise<number> {
  let pid = 0;
  await until(async () => {
    pid = Number((await readFile(file, "utf8").catch(() => "")).trim());
    return pid > 0;
  }, "the grandchild's pid");
  return pid;
}

test("killLiveCommandGroups ends the descendants of a command that is still running", async () => {
  const root = await temp();
  const pidfile = join(root, "pid");
  const proposal = await createCommandProposal(
    ["/bin/sh", "-c", `sleep 300 & echo $! > '${pidfile}'; wait`],
    root,
    60_000,
  );
  const running = executeApprovedCommand(
    proposal,
    new AbortController().signal,
  );
  const grandchild = await grandchildPid(pidfile);
  expect(alive(grandchild)).toBe(true);
  // (This process is the leader's parent and has not reaped it, so a real
  // probe still sees a zombie: report the group gone once SIGTERM is sent.)
  const send = process.kill.bind(process);
  const kill = vi.spyOn(process, "kill").mockImplementation((pid, signal) => {
    if (signal === 0)
      throw Object.assign(new Error("no such process"), { code: "ESRCH" });
    return send(pid, signal);
  });
  killLiveCommandGroups();
  // SIGTERM ended the group, so no SIGKILL goes to a pid that may be reused.
  expect(kill.mock.calls.map(([, signal]) => signal)).not.toContain("SIGKILL");
  kill.mockRestore();
  await until(() => !alive(grandchild), "the grandchild to die");
  // The command's own run still settles, as a failed one.
  expect((await running).exitCode).not.toBe(0);
});

// The host dies for real: a child bun runs the command, then the parent
// leaves it by exit, by a signal, or by a signal another handler owns.
test.each([
  ["exit", undefined, { code: 3, signal: null }],
  // Only the SIGKILL after the grace period ends this one.
  ["stubborn", undefined, { code: 3, signal: null }],
  ["signal", "SIGTERM", { code: null, signal: "SIGTERM" }],
  ["signal", "SIGHUP", { code: null, signal: "SIGHUP" }],
  ["signal", "SIGINT", { code: null, signal: "SIGINT" }],
  // A handler of the host's own keeps deciding how it exits.
  ["handled", "SIGTERM", { code: 7, signal: null }],
  // The host's handler keeps it running: the signal alone kills nothing, and
  // the exit hook cleans up when the host does leave.
  ["kept", "SIGTERM", { code: 9, signal: null }],
  // Its handler cancels the command, and the cancel path cleans the group.
  ["cancel", "SIGTERM", { code: 8, signal: null }],
] as const)(
  "a host that leaves by %s (%s) takes its command's descendants with it",
  { timeout: 30_000 },
  async (mode, signal, expected) => {
    const root = await temp();
    const pidfile = join(root, "pid");
    const host = spawn(
      process.execPath,
      [join(import.meta.dirname, "fixtures/detached_orphan.ts"), pidfile, mode],
      { cwd: root, stdio: ["ignore", "pipe", "inherit"] },
    );
    const exited = new Promise<{
      code: number | null;
      signal: NodeJS.Signals | null;
    }>((resolve) =>
      host.once("exit", (code, exitSignal) =>
        resolve({ code, signal: exitSignal }),
      ),
    );
    let ready = false;
    host.stdout.on("data", (chunk: Buffer) => {
      if (chunk.toString().includes("ready")) ready = true;
    });
    await until(() => ready, "the host to run its command");
    const grandchild = await grandchildPid(pidfile);
    if (signal) host.kill(signal);
    if (mode === "kept") {
      await new Promise((resolve) => setTimeout(resolve, 400));
      expect(alive(grandchild)).toBe(true);
    }
    expect(await exited).toEqual(expected);
    await until(() => !alive(grandchild), "the grandchild to die");
  },
);

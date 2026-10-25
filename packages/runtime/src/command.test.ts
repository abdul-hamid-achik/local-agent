import { execFileSync, spawn } from "node:child_process";
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
  commandLine,
  createCommandProposal,
  executeApprovedCommand,
  killLiveCommandGroups,
  parseArgv,
  secretPathNotes,
  shellWord,
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

test("a cut at the output cap never leaves half of a character", async () => {
  const root = await temp();
  // One ASCII byte, then two-byte characters: the cap falls on a lead byte.
  const proposal = await createCommandProposal(
    [process.execPath, "-e", "process.stdout.write('a' + 'é'.repeat(600000))"],
    root,
    5000,
  );
  const result = await executeApprovedCommand(
    proposal,
    new AbortController().signal,
  );
  expect(result.truncated).toBe(true);
  expect(result.output).not.toContain("\ufffd");
  expect(result.output.endsWith("é")).toBe(true);
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

test("a host environment addition is HOME alone, never a variable that loads code", async () => {
  const root = await temp();
  for (const name of [
    "LD_PRELOAD",
    "DYLD_INSERT_LIBRARIES",
    "NODE_OPTIONS",
    "PATH",
  ])
    await expect(
      createCommandProposal(["true"], root, 1000, { [name]: "x" }),
    ).rejects.toThrow(`${name} is not allowed`);
  const proposal = await createCommandProposal(
    [process.execPath, "-e", "process.stdout.write(process.env.HOME ?? '')"],
    root,
    5000,
    { HOME: "/host/home" },
  );
  const result = await executeApprovedCommand(
    proposal,
    new AbortController().signal,
  );
  expect(result.output).toBe("/host/home");
});

test("a program that does not exist rejects with ENOENT, and its proposal is spent", async () => {
  const root = await temp();
  const proposal = await createCommandProposal(
    ["__no_such_program__"],
    root,
    1000,
  );
  await expect(
    executeApprovedCommand(proposal, new AbortController().signal),
  ).rejects.toMatchObject({ code: "ENOENT" });
  await expect(
    executeApprovedCommand(proposal, new AbortController().signal),
  ).rejects.toThrow();
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
    // Before any assertion can fail: the finally below kills what it names.
    escapedPid = Number(await readFile(join(root, "escaped.pid"), "utf8"));
    expect(Date.now() - started).toBeLessThan(1500);
    expect(result.exitCode).toBe(0);
    expect(result.output).toContain("leader-done");
    expect(result.cleanupIncomplete).toBe(true);
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

test("parseArgv refuses a JSON array with a non-string element instead of running it as a command line", () => {
  expect(() => parseArgv('["ls", 1]')).toThrow("strings");
  expect(() => parseArgv('["rm", ["-rf"]]')).toThrow("strings");
  // Not JSON: still a command line (here the `[` builtin).
  expect(parseArgv("[ -f x ]")).toEqual(["[", "-f", "x", "]"]);
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

test("more path words than are checked for links flags the command instead of passing the rest", async () => {
  const root = await temp();
  await writeFile(join(root, ".env"), "TOKEN=planted");
  await symlink(".env", join(root, "last.txt"));
  const words = Array.from({ length: 64 }, (_, i) => `w${i}.txt`);
  const secretsOf = async (...argv: string[]) =>
    (await createCommandProposal(argv, root, 1000)).secretPaths;
  // At the limit every word is looked at, and the link is seen.
  expect(await secretsOf("cat", ...words.slice(1), "last.txt")).toEqual([
    "last.txt -> .env",
  ]);
  // Past it the link is not looked at, so the proposal says it is unchecked.
  expect(await secretsOf("cat", ...words, "last.txt")).toEqual([
    "65 path words, only 64 checked",
  ]);
  // Past it, harmless words alone are flagged too: the check cannot tell.
  expect(await secretsOf("cat", ...words, "plain.txt")).toEqual([
    "65 path words, only 64 checked",
  ]);
  // The proposal keeps 8 entries, and the note is one of them.
  const many = Array.from({ length: 9 }, (_, i) => `s${i}/.env`);
  const flagged = await secretsOf("cat", ...many, ...words);
  expect(flagged).toHaveLength(8);
  expect(flagged?.[0]).toBe("73 path words, only 64 checked");
  // For display it is a count, not a path.
  expect(secretPathNotes(flagged)).toEqual({
    paths: many.slice(0, 7),
    unchecked: 73,
  });
  expect(secretPathNotes(["notes.txt -> .env"])).toEqual({
    paths: ["notes.txt -> .env"],
  });
  expect(secretPathNotes(undefined)).toEqual({ paths: [] });
});

test("shellWord shows an argument as a shell reads it, hidden characters as escapes", () => {
  expect(shellWord("tests/a-b_c.test.ts")).toBe("tests/a-b_c.test.ts");
  expect(shellWord("a b")).toBe("'a b'");
  expect(shellWord("it's")).toBe("'it'\\''s'");
  expect(shellWord("")).toBe("''");
  expect(shellWord("a|b")).toBe("'a|b'");
  expect(shellWord("é 🙂")).toBe("'é 🙂'");
  // A line break, an escape sequence, a direction override, a space that is
  // not one, a tab next to a quote, and a tag character outside the BMP.
  expect(shellWord("a\nb")).toBe("$'a\\nb'");
  expect(shellWord("x\u001b[2Ky")).toBe("$'x\\x1b[2Ky'");
  expect(shellWord("\u202eevil")).toBe("$'\\xe2\\x80\\xaeevil'");
  expect(shellWord("zero\u200bwidth")).toBe("$'zero\\xe2\\x80\\x8bwidth'");
  expect(shellWord("nb\u00a0sp")).toBe("$'nb\\xc2\\xa0sp'");
  expect(shellWord("it's\tok\\")).toBe("$'it\\'s\\tok\\\\'");
  expect(shellWord("\u{e0001}tag")).toBe("$'\\xf3\\xa0\\x80\\x81tag'");
  // Letters and marks that draw nothing: a Hangul filler looks like a
  // space, a variation selector like nothing at all.
  expect(shellWord("a\u3164b")).toBe("$'a\\xe3\\x85\\xa4b'");
  expect(shellWord("a\u2800b")).toBe("$'a\\xe2\\xa0\\x80b'");
  expect(shellWord("x\ufe0f")).toBe("$'x\\xef\\xb8\\x8f'");
  expect(shellWord("a\u034fb")).toBe("$'a\\xcd\\x8fb'");
  expect(shellWord("a\ufffcb")).toBe("$'a\\xef\\xbf\\xbcb'");
  expect(shellWord("x\u{e0100}")).toBe("$'x\\xf3\\xa0\\x84\\x80'");
  // An accent that combines with its letter still shows as one.
  expect(shellWord("cafe\u0301")).toBe("'cafe\u0301'");
  expect(commandLine(["rm", "-rf", "a b", ""])).toBe("rm -rf 'a b' ''");
});

test("what shellWord shows, bash and zsh read back as the very argument", () => {
  const values = [
    "a b",
    "it's",
    "",
    "a\nb",
    "x\u001b[2Ky",
    "\u202eevil",
    "zero\u200bwidth",
    "nb\u00a0sp",
    "\u0085nel",
    "a\u2028b",
    "tab\there 'q' \\ $HOME `x`",
    "é 🙂",
    "\u{e0001}tag",
    "a\u3164b x\ufe0f a\u2800b",
  ];
  // macOS ships bash 3.2 as /bin/bash and zsh as the login shell; zsh may
  // not be installed elsewhere.
  const shells: [string, ...string[]][] = [
    ["bash", "-c"],
    ["/bin/bash", "-c"],
  ];
  try {
    execFileSync("zsh", ["-f", "-c", ":"]);
    shells.push(["zsh", "-f", "-c"]);
  } catch (error) {
    // Only a missing zsh is skipped; one that fails to start is a failure.
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
  }
  for (const [shell, ...flags] of shells)
    for (const value of values) {
      const read = execFileSync(shell, [
        ...flags,
        `printf %s ${shellWord(value)}`,
      ]);
      expect(
        read.equals(Buffer.from(value)),
        `${shell} ${JSON.stringify(value)}`,
      ).toBe(true);
    }
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

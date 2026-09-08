import { mkdir, mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test } from "vitest";
import {
  type ApprovedCommandProposal,
  createCommandProposal,
  executeApprovedCommand,
} from "./command";

const roots: string[] = [];
afterEach(async () => {
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
  const proposal = await createCommandProposal(
    [
      process.execPath,
      "-e",
      "process.stdout.write(JSON.stringify({argv:process.argv.slice(1),env:Object.keys(process.env).sort()}))",
      "literal;$(echo shell)",
    ],
    root,
    2000,
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
  expect(output.env).not.toContain("OLLAMA_HOST");
  expect(output.env).not.toContain("OPENAI_API_KEY");
  expect(output.env).not.toContain("AWS_SECRET_ACCESS_KEY");
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

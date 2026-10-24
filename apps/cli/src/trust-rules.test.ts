import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { loadPermissionFile, type PermissionRule } from "@local-agent/runtime";
import { expect, test } from "vitest";
import { type TrustIO, trustRulesCommand } from "#cli/trust-rules";

async function workspaceWith(rules: PermissionRule[]): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), "la-trust-cli-"));
  await mkdir(join(root, ".local-agent"));
  await writeFile(
    join(root, ".local-agent", "permissions.json"),
    JSON.stringify({ version: 1, rules }),
  );
  return root;
}

function io(answer: string | undefined): TrustIO & { lines: string[] } {
  const lines: string[] = [];
  return {
    lines,
    print: (line) => lines.push(line),
    ask: async () => answer,
  };
}

const allowAll: PermissionRule = {
  effect: "allow",
  tool: "run_command",
  pattern: "*",
};

test("--trust-rules lists the untrusted rules and trusts them on yes", async () => {
  const root = await workspaceWith([
    allowAll,
    { effect: "deny", tool: "run_command", pattern: "rm *" },
  ]);
  const out = io("yes");
  expect(await trustRulesCommand(root, out)).toBe(0);
  expect(out.lines[0]).toContain("1 allow rule in");
  expect(out.lines[1]).toBe('  allow run_command "*"');
  expect(out.lines.at(-1)).toBe("Trusted 1 rule");
  expect((await loadPermissionFile(root)).untrusted).toEqual([]);
  // Nothing left to trust: says so and succeeds.
  const again = io(undefined);
  expect(await trustRulesCommand(root, again)).toBe(0);
  expect(again.lines).toEqual([
    `No untrusted allow rules in ${join(root, ".local-agent", "permissions.json")}`,
  ]);
});

test("--trust-rules trusts nothing without a typed yes", async () => {
  for (const answer of [undefined, "", "y", "no"]) {
    const root = await workspaceWith([allowAll]);
    const out = io(answer);
    expect(await trustRulesCommand(root, out)).toBe(1);
    expect((await loadPermissionFile(root)).untrusted).toEqual([allowAll]);
    expect(out.lines.at(-1)).toBe(
      answer === undefined
        ? "--trust-rules asks for confirmation; run it in a terminal"
        : "Nothing trusted",
    );
  }
});

test("--trust-rules reports a broken rules file instead of trusting it", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-trust-cli-"));
  await mkdir(join(root, ".local-agent"));
  await writeFile(join(root, ".local-agent", "permissions.json"), "{");
  const out = io("yes");
  expect(await trustRulesCommand(root, out)).toBe(1);
  expect(out.lines[0]).toContain("not valid JSON");
});

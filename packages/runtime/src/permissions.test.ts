import {
  mkdir,
  mkdtemp,
  readdir,
  readFile,
  rename,
  rm,
  stat,
  symlink,
  utimes,
  writeFile,
} from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { userConfigDir } from "#runtime/paths";
import {
  appendRule,
  clearRules,
  evaluate,
  loadPermissionFile,
  type PermissionRequest,
  type PermissionRule,
  permissionRequest,
  proposeRule,
  readPermissionFile,
  trustRules,
  trustStorePath,
} from "#runtime/permissions";

const edit = (target: string): PermissionRequest => ({ tool: "edit", target });
const run = (target: string, argv?: readonly string[]): PermissionRequest => ({
  tool: "run_command",
  target,
  argv,
});
const rule = (
  effect: PermissionRule["effect"],
  tool: PermissionRule["tool"],
  pattern: string,
): PermissionRule => ({ effect, tool, pattern });

async function tempRoot(): Promise<string> {
  return mkdtemp(join(tmpdir(), "la-perms-"));
}

// ------------------------------------------------------------------- matchers

test("edit patterns: globs, anchoring and escaped literals", () => {
  const rules = [rule("allow", "edit", "/src/**")];
  expect(evaluate(rules, edit("src/a.ts")).effect).toBe("allow");
  expect(evaluate(rules, edit("src/deep/b.ts")).effect).toBe("allow");
  // Unanchored: a segment-boundary suffix matches.
  expect(evaluate(rules, edit("lib/src/a.ts")).effect).toBe("ask");
  expect(evaluate(rules, edit("srcx/a.ts")).effect).toBe("ask");
  expect(
    evaluate([rule("allow", "edit", "src/*")], edit("src/deep/a.ts")).effect,
  ).toBe("ask");
  expect(
    evaluate([rule("allow", "edit", "*")], edit("anything/x")).effect,
  ).toBe("allow");
  expect(evaluate([rule("allow", "edit", "")], edit("anything/x")).effect).toBe(
    "allow",
  );
  // An escaped star matches only the literal filename.
  expect(
    evaluate([rule("allow", "edit", "/a\\*b.txt")], edit("a*b.txt")).effect,
  ).toBe("allow");
  expect(
    evaluate([rule("allow", "edit", "/a\\*b.txt")], edit("axxb.txt")).effect,
  ).toBe("ask");
});

test("run_command patterns: star prefixes, exact matches, one argv each", () => {
  const rules = [rule("allow", "run_command", "npm test *")];
  expect(evaluate(rules, run("npm test")).effect).toBe("allow");
  expect(evaluate(rules, run("npm test -- foo.ts")).effect).toBe("allow");
  expect(evaluate(rules, run("npm run test")).effect).toBe("ask");
  expect(
    evaluate([rule("deny", "run_command", "rm *")], run("rm -rf /")).effect,
  ).toBe("deny");
  // Without a star the match is exact: `rm` alone does not cover `rm -rf /`.
  expect(
    evaluate([rule("deny", "run_command", "rm")], run("rm -rf /")).effect,
  ).toBe("ask");
  expect(evaluate([rule("deny", "run_command", "rm")], run("rm")).effect).toBe(
    "deny",
  );
  // No shell: each target is one command, so nothing is split further.
  expect(
    evaluate([rule("allow", "run_command", "git status")], run("git status"))
      .effect,
  ).toBe("allow");
  expect(
    evaluate(
      [rule("allow", "run_command", "git status")],
      run("git status --short"),
    ).effect,
  ).toBe("ask");
});

test("fetch and web_search patterns: domains, subdomains, catch-all only", () => {
  const rules = [rule("allow", "fetch", "domain:*.example.com")];
  expect(
    evaluate(rules, { tool: "fetch", target: "a.example.com" }).effect,
  ).toBe("allow");
  expect(evaluate(rules, { tool: "fetch", target: "example.com" }).effect).toBe(
    "ask",
  );
  expect(
    evaluate([rule("allow", "fetch", "domain:example.com")], {
      tool: "fetch",
      target: "example.com",
    }).effect,
  ).toBe("allow");
  expect(
    evaluate([rule("allow", "fetch", "*")], {
      tool: "fetch",
      target: "any.host",
    }).effect,
  ).toBe("allow");
  // A search has no host: only the catch-all matches it.
  expect(
    evaluate([rule("allow", "web_search", "domain:example.com")], {
      tool: "web_search",
      target: "example.com news",
    }).effect,
  ).toBe("ask");
  expect(
    evaluate([rule("allow", "web_search", "*")], {
      tool: "web_search",
      target: "anything",
    }).effect,
  ).toBe("allow");
});

test("mcp patterns: exact names and wildcards anywhere", () => {
  const rules = [rule("allow", "mcp", "fs__read*")];
  expect(evaluate(rules, { tool: "mcp", target: "fs__readFile" }).effect).toBe(
    "allow",
  );
  expect(evaluate(rules, { tool: "mcp", target: "fs__write" }).effect).toBe(
    "ask",
  );
  expect(
    evaluate([rule("deny", "mcp", "*__exec*")], {
      tool: "mcp",
      target: "sh__exec2",
    }).effect,
  ).toBe("deny");
  expect(
    evaluate([rule("allow", "mcp", "fs__readFile")], {
      tool: "mcp",
      target: "fs__readFile",
    }).effect,
  ).toBe("allow");
});

// ------------------------------------------------------------------ evaluation

test("evaluation order: deny beats ask beats allow, first rule per effect", () => {
  const rules = [
    rule("allow", "run_command", "npm *"),
    rule("deny", "run_command", "npm publish *"),
    rule("ask", "run_command", "npm test *"),
  ];
  expect(evaluate(rules, run("npm publish")).effect).toBe("deny");
  const ask = evaluate(rules, run("npm test"));
  expect(ask.effect).toBe("ask");
  expect(ask.rule?.pattern).toBe("npm test *");
  const allow = evaluate(rules, run("npm run build"));
  expect(allow.effect).toBe("allow");
  expect(allow.rule?.pattern).toBe("npm *");
  // No match at all: ask, with no rule named.
  expect(evaluate(rules, run("bun test"))).toEqual({ effect: "ask" });
});

test("tools never cross-match", () => {
  const rules = [rule("allow", "edit", "*")];
  expect(evaluate(rules, run("edit")).effect).toBe("ask");
  expect(
    evaluate([rule("deny", "run_command", "*")], edit("a.txt")).effect,
  ).toBe("ask");
});

// ------------------------------------------------------------------ proposeRule

test("proposeRule: paths escaped, commands narrowed, hosts and tools exact", () => {
  expect(proposeRule(edit("src/a*b.ts?"))).toEqual({
    effect: "allow",
    tool: "edit",
    pattern: "src/a\\*b.ts\\?",
  });
  expect(proposeRule(run("bun test", ["bun", "test"]))).toEqual({
    effect: "allow",
    tool: "run_command",
    pattern: "bun test",
  });
  // "Always" saves the exact argv: never a program-wide star.
  expect(
    proposeRule(run("npm test -- --watch", ["npm", "test", "--", "--watch"])),
  ).toEqual({
    effect: "allow",
    tool: "run_command",
    pattern: "npm test -- --watch",
  });
  // Words that would not survive bare are quoted, and match back exactly.
  const odd = ["git", "commit", "-m", 'fix "it" now', "a\\b", "*", "x*", ""];
  const quoted = proposeRule(run(odd.join(" "), odd));
  expect(quoted.pattern).toBe(
    'git commit -m "fix \\"it\\" now" "a\\\\b" "*" "x*" ""',
  );
  expect(evaluate([quoted], run(odd.join(" "), odd)).effect).toBe("allow");
  expect(
    evaluate([quoted], run("git commit -m x", ["git", "commit", "-m", "x"]))
      .effect,
  ).toBe("ask");
  expect(proposeRule({ tool: "fetch", target: "docs.example.com" })).toEqual({
    effect: "allow",
    tool: "fetch",
    pattern: "domain:docs.example.com",
  });
  expect(proposeRule({ tool: "web_search", target: "anything" })).toEqual({
    effect: "allow",
    tool: "web_search",
    pattern: "*",
  });
});

test("permissionRequest maps every approval kind", () => {
  expect(
    permissionRequest({
      kind: "edit",
      path: "a.txt",
      before: "",
      after: "",
      diff: "",
      identity: "i",
    }),
  ).toEqual({ tool: "edit", target: "a.txt" });
  expect(
    permissionRequest({
      kind: "write",
      path: "b/c.txt",
      created: true,
      before: "",
      after: "x",
      diff: "",
      identity: "i",
    }),
  ).toEqual({ tool: "edit", target: "b/c.txt" });
  expect(
    permissionRequest({
      kind: "command",
      argv: ["bun", "test"],
      cwd: "/w",
      timeoutMs: 1,
    }),
  ).toEqual({ tool: "run_command", target: "bun test", argv: ["bun", "test"] });
  expect(
    permissionRequest({
      kind: "mcp",
      name: "fs__readFile",
      argumentsJson: "{}",
    }),
  ).toEqual({ tool: "mcp", target: "fs__readFile" });
  // Through a gateway: the tool that runs, with every other name it has.
  expect(
    permissionRequest({
      kind: "mcp",
      name: "hub__call_tool",
      argumentsJson: "{}",
      target: "hub__cortex__status",
      inner: "cortex__status",
      gateway: "hub",
      aliases: ["hub__cortex__cortex_status"],
    }),
  ).toEqual({
    tool: "mcp",
    target: "hub__cortex__status",
    also: ["hub__call_tool", "hub__cortex__cortex_status"],
  });
});

test("rules match a gateway call by the tool it runs or any other name", () => {
  const request = permissionRequest({
    kind: "mcp",
    name: "hub__call_tool",
    argumentsJson: "{}",
    target: "hub__cortex__status",
    inner: "cortex__status",
    gateway: "hub",
    aliases: ["hub__cortex__cortex_status"],
  });
  const rule = (effect: "allow" | "deny", pattern: string) =>
    ({ effect, tool: "mcp", pattern }) as const;
  // An "always" saves the downstream tool, never the gateway's call_tool.
  expect(proposeRule(request)).toEqual(rule("allow", "hub__cortex__status"));
  for (const pattern of [
    "hub__cortex__status",
    "hub__cortex__*",
    "hub__cortex__cortex_status",
    "hub__call_tool",
  ])
    expect(evaluate([rule("deny", pattern)], request).effect).toBe("deny");
  expect(evaluate([rule("allow", "hub__cortex__status")], request).effect).toBe(
    "allow",
  );
  // Another downstream tool is not this one.
  expect(evaluate([rule("allow", "hub__monitor__*")], request).effect).toBe(
    "ask",
  );
  // The other names block, never allow: an alias can be a different tool
  // on the same server, and call_tool is every tool behind the gateway.
  for (const pattern of ["hub__cortex__cortex_status", "hub__call_tool"])
    expect(evaluate([rule("allow", pattern)], request).effect).toBe("ask");
  expect(
    evaluate(
      [rule("allow", "hub__cortex__*"), rule("deny", "hub__call_tool")],
      request,
    ).effect,
  ).toBe("deny");
});

test("loadPermissionFile: missing and invalid files degrade to empty", async () => {
  const root = await tempRoot();
  expect(await loadPermissionFile(root)).toEqual({
    version: 1,
    rules: [],
    untrusted: [],
  });
});

test("loadPermissionFile: schema violations carry a warning, never throw", async () => {
  const root = await tempRoot();
  const dir = join(root, ".local-agent");
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, "permissions.json"), "not json");
  const broken = await loadPermissionFile(root);
  expect(broken.rules).toEqual([]);
  expect(broken.warning).toBeTruthy();
  await writeFile(
    join(dir, "permissions.json"),
    JSON.stringify({ version: 2, rules: [] }),
  );
  const versioned = await loadPermissionFile(root);
  expect(versioned.rules).toEqual([]);
  expect(versioned.warning).toContain("version");
  await writeFile(
    join(dir, "permissions.json"),
    JSON.stringify({
      version: 1,
      rules: [{ effect: "maybe", tool: "edit", pattern: "*" }],
    }),
  );
  const effects = await loadPermissionFile(root);
  expect(effects.rules).toEqual([]);
  expect(effects.warning).toBeTruthy();
});

test("appendRule: creates the file, adds the gitignore entry, reads back", async () => {
  const root = await tempRoot();
  await writeFile(join(root, ".gitignore"), "dist/\n");
  const saved = await appendRule(root, rule("allow", "edit", "/src/**"));
  expect(saved.created).toBe(true);
  expect(saved.gitignoreAdded).toBe(true);
  const loaded = await loadPermissionFile(root);
  expect(loaded.rules).toEqual([rule("allow", "edit", "/src/**")]);
  expect(await readFile(join(root, ".gitignore"), "utf8")).toBe(
    "dist/\n.local-agent/\n",
  );
  // A second rule appends; the gitignore entry is not duplicated.
  const again = await appendRule(root, rule("deny", "run_command", "rm *"));
  expect(again.created).toBe(false);
  expect(again.gitignoreAdded).toBe(false);
  expect((await loadPermissionFile(root)).rules).toHaveLength(2);
  expect(await readFile(join(root, ".gitignore"), "utf8")).toBe(
    "dist/\n.local-agent/\n",
  );
});

test("appendRule: refuses a rule a deny rule already covers", async () => {
  const root = await tempRoot();
  await appendRule(root, rule("deny", "run_command", "rm *"));
  await expect(
    appendRule(root, rule("allow", "run_command", "rm *")),
  ).rejects.toThrow(/deny rule/);
  // The file is untouched.
  expect((await loadPermissionFile(root)).rules).toHaveLength(1);
});

test("clearRules rewrites the file empty and drops its trust", async () => {
  const root = await tempRoot();
  await appendRule(root, rule("allow", "edit", "*"));
  await clearRules(root);
  expect(await loadPermissionFile(root)).toEqual({
    version: 1,
    rules: [],
    untrusted: [],
  });
  // The same rule written back by hand is not trusted any more.
  await writeFile(
    join(root, ".local-agent", "permissions.json"),
    JSON.stringify({ version: 1, rules: [rule("allow", "edit", "*")] }),
  );
  expect((await loadPermissionFile(root)).rules).toEqual([]);
});

// ---------------------------------------------------------------------- trust

async function plant(root: string, rules: PermissionRule[]): Promise<void> {
  await mkdir(join(root, ".local-agent"), { recursive: true });
  await writeFile(
    join(root, ".local-agent", "permissions.json"),
    JSON.stringify({ version: 1, rules }),
  );
}

test("a repository's allow rules are ignored until trusted; deny and ask always apply", async () => {
  const root = await tempRoot();
  const planted = [
    rule("allow", "run_command", "*"),
    rule("deny", "run_command", "rm *"),
    rule("ask", "edit", "/src/**"),
  ];
  await plant(root, planted);
  const loaded = await loadPermissionFile(root);
  expect(loaded.rules).toEqual(planted.slice(1));
  expect(loaded.untrusted).toEqual(planted.slice(0, 1));
  expect(loaded.warning).toContain("1 allow rule not trusted");
  expect(loaded.warning).toContain("/permissions trust");
  expect(
    evaluate(loaded.rules, run("curl evil", ["curl", "evil"])).effect,
  ).toBe("ask");
  expect(
    evaluate(loaded.rules, run("rm -rf /", ["rm", "-rf", "/"])).effect,
  ).toBe("deny");
  // The raw read still sees every rule (for rewriting and listing).
  expect((await readPermissionFile(root)).rules).toEqual(planted);
});

test("'always' trusts only the rule it adds, never the planted ones beside it", async () => {
  const root = await tempRoot();
  await plant(root, [rule("allow", "run_command", "curl *")]);
  await appendRule(root, rule("allow", "run_command", "bun test"));
  const loaded = await loadPermissionFile(root);
  expect(loaded.rules).toEqual([rule("allow", "run_command", "bun test")]);
  expect(loaded.untrusted).toEqual([rule("allow", "run_command", "curl *")]);
  // Appending a rule the file already holds trusts it without duplicating it.
  await appendRule(root, rule("allow", "run_command", "curl *"));
  expect((await readPermissionFile(root)).rules).toHaveLength(2);
  expect((await loadPermissionFile(root)).untrusted).toEqual([]);
});

test("trustRules trusts the file as it is now, per workspace real path", async () => {
  const root = await tempRoot();
  await plant(root, [
    rule("allow", "edit", "/src/**"),
    rule("deny", "run_command", "rm *"),
  ]);
  expect((await trustRules(root)).trusted).toEqual([
    rule("allow", "edit", "/src/**"),
  ]);
  expect((await trustRules(root)).trusted).toEqual([]);
  expect((await loadPermissionFile(root)).untrusted).toEqual([]);
  // A rule added to the file afterwards is not covered by that trust.
  await plant(root, [
    rule("allow", "edit", "/src/**"),
    rule("allow", "edit", "*"),
  ]);
  expect((await loadPermissionFile(root)).untrusted).toEqual([
    rule("allow", "edit", "*"),
  ]);
  // The same file in another checkout starts untrusted.
  const clone = await tempRoot();
  await plant(clone, [rule("allow", "edit", "/src/**")]);
  expect((await loadPermissionFile(clone)).rules).toEqual([]);
  // The store is private and lives outside every workspace.
  const store = trustStorePath();
  expect(store.startsWith(process.env.XDG_CONFIG_HOME ?? "\0")).toBe(true);
  expect((await stat(store)).mode & 0o777).toBe(0o600);
});

test("a damaged trust store trusts nothing", async () => {
  const root = await tempRoot();
  await appendRule(root, rule("allow", "edit", "*"));
  await writeFile(trustStorePath(), "{not json");
  expect((await loadPermissionFile(root)).untrusted).toEqual([
    rule("allow", "edit", "*"),
  ]);
  await rm(trustStorePath());
});

test("command deny rules match the program by basename; allow rules never do", () => {
  const argv = ["/bin/rm", "-rf", "/"];
  expect(
    evaluate([rule("deny", "run_command", "rm *")], run(argv.join(" "), argv))
      .effect,
  ).toBe("deny");
  expect(
    evaluate([rule("ask", "run_command", "rm *")], run(argv.join(" "), argv))
      .effect,
  ).toBe("ask");
  const planted = ["./git", "status"];
  expect(
    evaluate(
      [rule("allow", "run_command", "git *")],
      run(planted.join(" "), planted),
    ).effect,
  ).toBe("ask");
  // The older `git*` spelling is still a prefix.
  expect(
    evaluate(
      [rule("allow", "run_command", "git*")],
      run("git status", ["git", "status"]),
    ).effect,
  ).toBe("allow");
  // Argv words compare one to one: a space inside one word is not two words.
  expect(
    evaluate(
      [rule("allow", "run_command", "echo a b")],
      run("echo a b", ["echo", "a b"]),
    ).effect,
  ).toBe("ask");
});

test("deny and ask rules look through wrappers, shells and find -exec", () => {
  const deny = [rule("deny", "run_command", "rm *")];
  for (const argv of [
    ["sudo", "-u", "root", "rm", "-rf", "/"],
    ["env", "FOO=1", "rm", "x"],
    ["timeout", "-s", "KILL", "5", "rm", "x"],
    ["nice", "-n", "10", "/bin/rm", "x"],
    ["xargs", "-n", "1", "rm"],
    ["ssh", "-p", "22", "host", "rm", "-rf", "/"],
    ["bash", "-c", "echo hi; rm -rf /"],
    ["sh", "-ec", "true && RM -rf /"],
    ["find", ".", "-name", "*.log", "-exec", "rm", "{}", ";"],
    ["sudo", "bash", "-c", "rm -rf /"],
    ["RM", "-rf", "/"],
    ["env", "-S", "rm x"],
    ["env", "--split-string=rm x"],
    ["su", "root", "-c", "rm x"],
    ["ssh", "-p", "22", "host", "echo hi; rm -rf /"],
    ["script", "-c", "rm x", "/dev/null"],
    ["script", "-q", "/dev/null", "rm", "x"],
    ["busybox", "rm", "x"],
    ["exec", "-a", "name", "rm", "x"],
    ["sh", "-c", "r''m x"],
    ["sh", "-c", "r\\m x"],
  ]) {
    expect(
      evaluate(deny, run(argv.join(" "), argv)).effect,
      argv.join(" "),
    ).toBe("deny");
  }
  // Naming `rm` is not running it.
  for (const argv of [
    ["git", "rm", "x"],
    ["echo", "rm"],
    ["bash", "-c", "echo rm"],
    ["ssh", "host", "ls"],
    ["env", "-S", "ls rm"],
  ])
    expect(
      evaluate(deny, run(argv.join(" "), argv)).effect,
      argv.join(" "),
    ).toBe("ask");
  // Allow rules never look inside: `rm *` does not allow `sudo rm`.
  const allow = [rule("allow", "run_command", "rm *")];
  const sudo = ["sudo", "rm", "x"];
  expect(evaluate(allow, run(sudo.join(" "), sudo)).effect).toBe("ask");
  const upper = ["RM", "x"];
  expect(evaluate(allow, run(upper.join(" "), upper)).effect).toBe("ask");
});

test("an empty or malformed restricting pattern still restricts", () => {
  const argv = ["make", "all"];
  const request = run(argv.join(" "), argv);
  expect(evaluate([rule("deny", "run_command", "")], request).effect).toBe(
    "deny",
  );
  expect(evaluate([rule("allow", "run_command", "")], request).effect).toBe(
    "ask",
  );
  // An unbalanced quote: a deny rule falls back to plain words, an allow
  // rule matches nothing.
  const quoted = ["git", "push", '"origin'];
  const push = run(quoted.join(" "), quoted);
  expect(
    evaluate([rule("deny", "run_command", 'git push "origin')], push).effect,
  ).toBe("deny");
  expect(
    evaluate([rule("allow", "run_command", 'git push "origin')], push).effect,
  ).toBe("ask");
  // `cat notes*` in an allow rule is not a glob that also means "any
  // further arguments": it matches nothing (the program word keeps the old
  // `git*` spelling).
  const extra = ["cat", "notes", "/etc/shadow"];
  expect(
    evaluate(
      [rule("allow", "run_command", "cat notes*")],
      run(extra.join(" "), extra),
    ).effect,
  ).toBe("ask");
  expect(
    evaluate(
      [rule("deny", "run_command", "cat notes*")],
      run(extra.join(" "), extra),
    ).effect,
  ).toBe("deny");
});

test("appendRule says whether it added the rule or only trusted it", async () => {
  const root = await tempRoot();
  const once = await appendRule(root, rule("allow", "edit", "/src/**"));
  expect(once).toMatchObject({ created: true, added: true });
  const twice = await appendRule(root, rule("allow", "edit", "/src/**"));
  expect(twice).toMatchObject({ created: false, added: false });
});

test("trustRules can be limited to the rules the user was shown", async () => {
  const root = await tempRoot();
  const shown = rule("allow", "edit", "/src/**");
  const late = rule("allow", "run_command", "curl *");
  await plant(root, [shown, late]);
  expect((await trustRules(root, [shown])).trusted).toEqual([shown]);
  expect((await loadPermissionFile(root)).untrusted).toEqual([late]);
  // A shown rule no longer in the file is not trusted.
  expect(
    (await trustRules(root, [rule("allow", "edit", "*")])).trusted,
  ).toEqual([]);
});

test("trust follows the directory, not only its path", async () => {
  const root = await tempRoot();
  await appendRule(root, rule("allow", "edit", "*"));
  expect((await loadPermissionFile(root)).untrusted).toEqual([]);
  // Another directory at the same path starts untrusted. (The old one is
  // moved aside, not deleted: a filesystem may hand a freed directory's
  // inode to the next one.)
  await rename(root, `${root}-old`);
  await mkdir(root);
  await plant(root, [rule("allow", "edit", "*")]);
  expect((await loadPermissionFile(root)).untrusted).toEqual([
    rule("allow", "edit", "*"),
  ]);
});

test("the trust store serializes writers and takes over a stale lock", async () => {
  const roots = await Promise.all(Array.from({ length: 6 }, tempRoot));
  await Promise.all(
    roots.map((root) => appendRule(root, rule("allow", "edit", "*"))),
  );
  for (const root of roots)
    expect((await loadPermissionFile(root)).untrusted).toEqual([]);
  // A crashed writer's lock (older than ten seconds) does not wedge trust.
  const lock = `${trustStorePath()}.lock`;
  await writeFile(lock, "");
  const old = new Date(Date.now() - 60_000);
  await utimes(lock, old, old);
  const root = await tempRoot();
  await appendRule(root, rule("allow", "edit", "/a"));
  expect((await loadPermissionFile(root)).untrusted).toEqual([]);
  await expect(stat(lock)).rejects.toThrow();
});

test("a damaged trust store is never rewritten", async () => {
  const root = await tempRoot();
  await writeFile(trustStorePath(), "{not json");
  await expect(appendRule(root, rule("allow", "edit", "*"))).rejects.toThrow(
    "damaged",
  );
  expect(await readFile(trustStorePath(), "utf8")).toBe("{not json");
  expect((await loadPermissionFile(root)).warning).toContain("damaged");
  await rm(trustStorePath());
});

test("the config directory ignores a relative XDG_CONFIG_HOME", () => {
  expect(userConfigDir({ XDG_CONFIG_HOME: "/x" })).toBe("/x/local-agent");
  for (const value of ["rel", "./rel", "", undefined])
    expect(userConfigDir({ XDG_CONFIG_HOME: value })).toBe(
      join(homedir(), ".config", "local-agent"),
    );
  expect(trustStorePath({ XDG_CONFIG_HOME: "rel" })).toBe(
    join(homedir(), ".config", "local-agent", "trusted-rules.json"),
  );
});

test("concurrent appends to one workspace keep every rule", async () => {
  const root = await tempRoot();
  const rules = Array.from({ length: 20 }, (_, i) =>
    rule(i % 2 ? "deny" : "allow", "run_command", `tool-${i}`),
  );
  await Promise.all(rules.map((each) => appendRule(root, each)));
  const file = await readPermissionFile(root);
  expect(new Set(file.rules.map((each) => each.pattern))).toEqual(
    new Set(rules.map((each) => each.pattern)),
  );
  expect((await loadPermissionFile(root)).untrusted).toEqual([]);
});

test("a planted symlink never redirects a policy write", async () => {
  const outside = await tempRoot();
  const root = await tempRoot();
  await symlink(outside, join(root, ".local-agent"));
  await expect(
    appendRule(root, rule("deny", "run_command", "rm *")),
  ).rejects.toThrow("not a plain directory");
  await expect(clearRules(root)).rejects.toThrow("not a plain directory");
  expect(await readdir(outside)).toEqual([]);
  // A symlinked permissions.json is refused too, and a symlinked
  // .gitignore is skipped, never appended to.
  const other = await tempRoot();
  await mkdir(join(other, ".local-agent"));
  await writeFile(join(outside, "elsewhere.json"), '{"version":1,"rules":[]}');
  await symlink(
    join(outside, "elsewhere.json"),
    join(other, ".local-agent", "permissions.json"),
  );
  await expect(
    appendRule(other, rule("deny", "run_command", "rm *")),
  ).rejects.toThrow("not a plain file");
  const third = await tempRoot();
  await writeFile(join(outside, "ignore"), "dist/\n");
  await symlink(join(outside, "ignore"), join(third, ".gitignore"));
  const saved = await appendRule(third, rule("deny", "run_command", "rm *"));
  expect(saved.gitignoreAdded).toBe(false);
  expect(await readFile(join(outside, "ignore"), "utf8")).toBe("dist/\n");
});

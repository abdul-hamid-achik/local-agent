import { expect, test } from "vitest";
import { AgentIgnore } from "#runtime/agentignore";

const ignores = (rules: string, path: string) =>
  AgentIgnore.parse(rules).ignore.matches(path);

test("a name without a slash matches at any depth and covers what is below it", () => {
  expect(ignores("node_modules", "node_modules")).toBe(true);
  expect(ignores("node_modules", "a/b/node_modules/c.js")).toBe(true);
  expect(ignores("node_modules", "node_modules_x/c.js")).toBe(false);
  expect(ignores("build/", "build/out.js")).toBe(true);
});

test("a pattern with a slash is rooted; ** crosses directories", () => {
  expect(ignores("src/gen", "src/gen/x.ts")).toBe(true);
  expect(ignores("src/gen", "lib/src/gen/x.ts")).toBe(false);
  expect(ignores("/src/gen", "src/gen/x.ts")).toBe(true);
  expect(ignores("private/**", "private/a/b")).toBe(true);
  expect(ignores("**/cache", "a/b/cache/x")).toBe(true);
  expect(ignores("**/cache", "cache/x")).toBe(true);
  expect(ignores("a/**/z", "a/z")).toBe(true);
  expect(ignores("a/**/z", "a/b/c/z")).toBe(true);
});

test("wildcards: * stays in one name, ? is one character, classes", () => {
  expect(ignores("*.log", "x/y/app.log")).toBe(true);
  expect(ignores("src/*.ts", "src/a.ts")).toBe(true);
  expect(ignores("src/*.ts", "src/deep/a.ts")).toBe(false);
  expect(ignores("src/*.ts", "lib/a.ts")).toBe(false);
  expect(ignores("a?c", "abc")).toBe(true);
  expect(ignores("a?c", "a😀c")).toBe(true);
  expect(ignores("file[0-9].txt", "file7.txt")).toBe(true);
  expect(ignores("file[0-9].txt", "filex.txt")).toBe(false);
  expect(ignores("file[!0-9].txt", "filex.txt")).toBe(true);
  expect(ignores("a\\*b", "a*b")).toBe(true);
  expect(ignores("a\\*b", "axb")).toBe(false);
});

test("the last matching line wins; comments and blank lines are skipped", () => {
  const rules = "# c\n\n*.md\n!README.md\nREADME.md/private\n";
  expect(ignores(rules, "docs/a.md")).toBe(true);
  expect(ignores(rules, "README.md")).toBe(false);
  expect(ignores(rules, "README.md/private")).toBe(true);
  expect(ignores("\\#hash", "#hash")).toBe(true);
});

test("Windows line ends and a byte-order mark do not change the rules", () => {
  expect(ignores("﻿a\r\nb\r\n", "a")).toBe(true);
  expect(ignores("﻿a\r\nb\r\n", "b/x")).toBe(true);
});

test("malformed lines are named with their line number and dropped", () => {
  const { ignore, warnings } = AgentIgnore.parse(
    "ok\n[abc\nz[z-a]\nend\\\n/\n",
  );
  expect(ignore.matches("ok")).toBe(true);
  expect(warnings).toEqual([
    '.agentignore line 2: "[abc" has an unterminated "["; the line is ignored',
    '.agentignore line 3: "z[z-a]" has an invalid character range; the line is ignored',
    '.agentignore line 4: "end\\\\" has a trailing backslash; the line is ignored',
    '.agentignore line 5: "/" names no path; the line is ignored',
  ]);
});

test("many malformed lines are counted, not all listed", () => {
  const { warnings } = AgentIgnore.parse("[\n".repeat(9));
  expect(warnings).toHaveLength(6);
  expect(warnings.at(-1)).toBe(".agentignore: 4 more malformed lines");
});

import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const transpiler = new Bun.Transpiler({ loader: "ts" });
const workspaces = {
  runtime: {
    alias: "#runtime/",
    forbidden: /^(?:vue|vue-termui|@vue\/|@opentui\/)/,
  },
  "vue-tui": { alias: "#tui/", forbidden: /chalupa/ },
} as const;

// Every specifier a module names: what Bun would load (dynamic imports and
// re-exports included) plus the static `from` clauses, which also catch the
// type-only imports the transpiler drops.
function specifiers(source: string): string[] {
  const loaded = transpiler.scanImports(source).map((each) => each.path);
  // An import or export statement's `from`, not one in a comment.
  const named = [
    ...source.matchAll(
      /^\s*(?:import|export)\b[^;]*?\bfrom\s*(['"])([^'"]+)\1/gm,
    ),
  ].map((match) => match[2] as string);
  return [...new Set([...loaded, ...named])];
}

// The specifiers in `source` (a file at `path` inside `directory`) that reach
// outside its workspace: another workspace's package or alias, a relative or
// absolute path out of the directory, or a dependency the workspace must not
// have.
function violations(
  workspace: keyof typeof workspaces,
  directory: string,
  path: string,
  source: string,
): string[] {
  const { alias, forbidden } = workspaces[workspace];
  return specifiers(source).filter((specifier) => {
    if (specifier.startsWith("@local-agent/") || forbidden.test(specifier))
      return true;
    if (specifier.startsWith("#")) return !specifier.startsWith(alias);
    if (specifier.startsWith(".") || specifier.startsWith("/"))
      return !resolve(dirname(path), specifier).startsWith(`${directory}/`);
    return false;
  });
}

test("the boundary check sees relative, dynamic, re-exported and type-only reaches", () => {
  const directory = `${root}/packages/vue-tui`;
  const path = `${directory}/src/screen.ts`;
  expect(
    violations(
      "vue-tui",
      directory,
      path,
      [
        'import { a } from "./own";',
        'import { b } from "#tui/own";',
        'import { c } from "../../runtime/src/agent";',
        'export * from "../../../apps/cli/src/main";',
        'const d = await import("@local-agent/runtime");',
        'import type { E } from "@local-agent/runtime";',
        'import { f } from "#runtime/agent";',
        'import "@thelacanians/chalupa-cli";',
        '// re-exported from "../../runtime/src/elsewhere" once',
        'import {\n  g,\n  type H,\n} from "@local-agent/runtime/multi";',
        "console.log(a, b, c, d, f);",
      ].join("\n"),
    ),
  ).toEqual([
    "../../runtime/src/agent",
    "../../../apps/cli/src/main",
    "@local-agent/runtime",
    "#runtime/agent",
    "@thelacanians/chalupa-cli",
    "@local-agent/runtime/multi",
  ]);
  expect(
    violations(
      "runtime",
      `${root}/packages/runtime`,
      `${root}/packages/runtime/src/x.ts`,
      'const v = await import("vue"); export { v };',
    ),
  ).toEqual(["vue"]);
});

test("extractable framework and runtime never depend on each other or application code", async () => {
  for (const workspace of Object.keys(
    workspaces,
  ) as (keyof typeof workspaces)[]) {
    const directory = `${root}/packages/${workspace}`;
    const manifest = await Bun.file(`${directory}/package.json`).json();
    const dependencies = Object.keys({
      ...manifest.dependencies,
      ...manifest.peerDependencies,
    });
    expect(dependencies.some((name) => name.startsWith("@local-agent/"))).toBe(
      false,
    );
    expect(
      dependencies.filter((name) => workspaces[workspace].forbidden.test(name)),
    ).toEqual([]);
    const reaches: string[] = [];
    for await (const path of new Bun.Glob("src/**/*.ts").scan({
      cwd: directory,
      absolute: true,
    })) {
      const source = await Bun.file(path).text();
      for (const specifier of violations(workspace, directory, path, source))
        reaches.push(`${path.slice(root.length + 1)}: ${specifier}`);
    }
    expect(reaches).toEqual([]);
  }
});

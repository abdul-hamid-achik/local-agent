import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const srcDir = dirname(fileURLToPath(import.meta.url));

/** The runtime's own modules (tests excluded), by file name. */
function modules(dir: string): string[] {
  return readdirSync(dir).filter(
    (name) => name.endsWith(".ts") && !name.endsWith(".test.ts"),
  );
}

/**
 * Every module a file imports from the runtime's own `src`: the runtime
 * imports the transpiler reports, plus type-only imports and re-exports it
 * erases, since a type cycle still ties the modules together for a reader
 * and for any later change that makes the type a value.
 */
function localImports(
  dir: string,
  file: string,
  known: Set<string>,
  source = readFileSync(join(dir, file), "utf8"),
): Set<string> {
  const specifiers = new Set(
    new Bun.Transpiler({ loader: "ts" })
      .scanImports(source)
      .map((entry) => entry.path),
  );
  for (const match of source.matchAll(/\b(?:from|import)\s*"([^"]+)"/gu))
    specifiers.add(match[1] as string);
  const found = new Set<string>();
  for (const specifier of specifiers) {
    const target = specifier.startsWith("#runtime/")
      ? `${specifier.slice("#runtime/".length)}.ts`
      : specifier.startsWith(".")
        ? `${resolve(dir, specifier).slice(dir.length + 1)}.ts`
        : undefined;
    if (target !== undefined && known.has(target)) found.add(target);
  }
  return found;
}

/** Each import cycle as a path that ends where it began, or none. */
function importCycles(graph: Map<string, Set<string>>): string[][] {
  const cycles: string[][] = [];
  const state = new Map<string, "open" | "done">();
  const path: string[] = [];
  const visit = (node: string) => {
    state.set(node, "open");
    path.push(node);
    for (const next of graph.get(node) ?? []) {
      if (state.get(next) === "open")
        cycles.push([...path.slice(path.indexOf(next)), next]);
      else if (!state.has(next)) visit(next);
    }
    path.pop();
    state.set(node, "done");
  };
  for (const node of graph.keys()) if (!state.has(node)) visit(node);
  return cycles;
}

describe("runtime module graph", () => {
  it("finds a cycle in a graph that has one", () => {
    const graph = new Map([
      ["index.ts", new Set(["agent.ts"])],
      ["agent.ts", new Set(["index.ts"])],
    ]);
    expect(importCycles(graph)).toEqual([["index.ts", "agent.ts", "index.ts"]]);
  });

  it("sees an import of the index, which re-exports the other modules", () => {
    const known = new Set(["index.ts", "agent.ts"]);
    expect(
      localImports(
        srcDir,
        "agent.ts",
        known,
        `import type { A } from "./index";`,
      ),
    ).toEqual(new Set(["index.ts"]));
    expect(
      localImports(
        srcDir,
        "agent.ts",
        known,
        `import { a } from "#runtime/index";`,
      ),
    ).toEqual(new Set(["index.ts"]));
  });

  it("has no import cycle among its src modules", () => {
    const files = modules(srcDir);
    const known = new Set(files);
    const graph = new Map(
      files.map((file) => [file, localImports(srcDir, file, known)]),
    );
    // index.ts only re-exports: nothing in the runtime may import it back.
    expect(
      files.filter((file) => graph.get(file)?.has("index.ts")),
      "modules importing index.ts",
    ).toEqual([]);
    expect(importCycles(graph).map((cycle) => cycle.join(" -> "))).toEqual([]);
  });
});

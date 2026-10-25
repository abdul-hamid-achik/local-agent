import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

/** Where each tool the README names is pinned: a manifest and its dependency. */
const PINS: Record<string, [manifest: string, dependency: string]> = {
  TypeScript: ["package.json", "typescript"],
  Vue: ["package.json", "vue"],
  Zod: ["packages/runtime/package.json", "zod"],
  "Vue TermUI": ["packages/vue-tui/package.json", "vue-termui"],
  OpenTUI: ["packages/vue-tui/package.json", "@opentui/core"],
  Vitest: ["package.json", "vitest"],
  Biome: ["package.json", "@biomejs/biome"],
};

async function pinned(manifest: string, dependency: string) {
  const json = await Bun.file(`${root}/${manifest}`).json();
  // A resolution is what every workspace gets, so it is read first.
  return (
    json.resolutions?.[dependency] ??
    json.dependencies?.[dependency] ??
    json.devDependencies?.[dependency]
  );
}

test("the README's pinned versions are the manifests' versions", async () => {
  const readme = await Bun.file(`${root}/README.md`).text();
  const sentence =
    readme.match(
      /Use Bun \*\*[^*]+\*\*\. The workspace pins [\s\S]*?\n\n/,
    )?.[0] ?? "";
  expect(sentence, "the README's pins sentence").not.toBe("");
  const named = new Map<string, string>(
    // A name is capitalized words before its bold version: "Vue TermUI **0.3.0**".
    [...sentence.matchAll(/([A-Z]\w*(?: [A-Z]\w*)*) \*\*([^*]+)\*\*/g)].map(
      ([, name = "", version = ""]) => [name.replace(/^Use /, ""), version],
    ),
  );
  const workspace = await Bun.file(`${root}/package.json`).json();
  expect(named.get("Bun")).toBe(workspace.packageManager.replace(/^bun@/, ""));
  expect([...named.keys()].sort()).toEqual(
    ["Bun", ...Object.keys(PINS)].sort(),
  );
  for (const [name, [manifest, dependency]] of Object.entries(PINS))
    expect(named.get(name), `${name} (${manifest} ${dependency})`).toBe(
      await pinned(manifest, dependency),
    );
});

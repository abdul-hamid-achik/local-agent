import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

test("extractable framework and runtime never depend on each other or application code", async () => {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
  for (const workspace of ["runtime", "vue-tui"]) {
    const directory = `${root}/packages/${workspace}`;
    const manifest = await Bun.file(`${directory}/package.json`).json();
    const dependencies = Object.keys({
      ...manifest.dependencies,
      ...manifest.peerDependencies,
    });
    expect(dependencies.some((name) => name.startsWith("@local-agent/"))).toBe(
      false,
    );
    if (workspace === "runtime")
      expect(dependencies.some((name) => /vue|opentui/.test(name))).toBe(false);
    for await (const path of new Bun.Glob("src/**/*.ts").scan({
      cwd: directory,
      absolute: true,
    })) {
      const source = await Bun.file(path).text();
      expect(source).not.toMatch(
        /from\s+['"](?:@local-agent\/|(?:\.\.\/)+(?:apps|packages\/(?:runtime|vue-tui))\/)/,
      );
      if (workspace === "runtime")
        expect(source).not.toMatch(/from\s+['"](?:vue|vue-termui|@opentui\/)/);
    }
  }
});

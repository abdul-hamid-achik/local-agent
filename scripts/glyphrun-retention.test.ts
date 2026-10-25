import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

// glyph prunes its run directories after every spec, and a CI job runs both
// batches: the run of an early failure is still there for the upload only
// while keepRuns holds every spec of the job.
test("Glyphrun keeps every run of one verification for the failure upload", async () => {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
  const config = await Bun.file(`${root}/glyphrun.config.yml`).text();
  const keepRuns = Number(/^\s+keepRuns:\s*(\d+)\s*$/m.exec(config)?.[1]);
  let specs = 0;
  for (const batch of ["bun", "tuimark"])
    for await (const _ of new Bun.Glob("*.yml").scan(`${root}/specs/${batch}`))
      specs++;
  expect(specs).toBeGreaterThan(0);
  expect(keepRuns).toBeGreaterThanOrEqual(specs);
});

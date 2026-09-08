import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { checkSiteLinks } from "./site-links";

test("rendered links resolve clean routes, encoded anchors and assets and report broken references", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-links-"));
  try {
    await mkdir(join(root, "assets"));
    await writeFile(join(root, "assets/logo.svg"), '<svg id="mark"/>');
    await writeFile(
      join(root, "v2.17.html"),
      '<main id="a&amp;b">Guide</main>',
    );
    await writeFile(
      join(root, "index.html"),
      `<a href="https://local-agent.dev:443/v2.17#a%26b:~:text=Guide">Guide</a>
      <img src="/assets/logo.svg#mark"><a href="https://example.com/missing">External</a>
      <a href="mailto:hello@example.com">Email</a>`,
    );
    expect(await checkSiteLinks(root)).toEqual([]);
    await writeFile(
      join(root, "index.html"),
      '<a href="/missing">Missing</a><a href="/v2.17#absent">Anchor</a><img src="/%00">',
    );
    const failures = await checkSiteLinks(root);
    expect(failures).toHaveLength(3);
    expect(failures.join("\n")).toContain("missing target");
    expect(failures.join("\n")).toContain("missing fragment");
    expect(failures.join("\n")).toContain("invalid path");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

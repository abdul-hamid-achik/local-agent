import { readFileSync } from "node:fs";
import { expect, test } from "vitest";

const html = readFileSync(new URL("../../index.html", import.meta.url), "utf8");
const policy = html.match(
  /http-equiv="Content-Security-Policy"\s+content="([^"]*)"/,
)?.[1];

// The built page is the packaged app's page: the dev server's origin is
// added only while `vite` serves it (see vite.config.ts).
test("the page's policy names no dev server and closes the unset directives", () => {
  expect(policy).toBeDefined();
  const directives = new Map(
    (policy ?? "")
      .split(";")
      .map((part) => part.trim().split(/\s+/))
      .filter(([name]) => name)
      .map(([name, ...sources]) => [name, sources.join(" ")]),
  );
  expect(directives.get("default-src")).toBe("'self'");
  expect(directives.get("connect-src")).toBe("'self'");
  // base-uri and form-action do not fall back to default-src.
  expect(directives.get("object-src")).toBe("'none'");
  expect(directives.get("base-uri")).toBe("'none'");
  expect(directives.get("form-action")).toBe("'none'");
  expect(policy).not.toMatch(/127\.0\.0\.1|localhost|5174|ws:/);
});

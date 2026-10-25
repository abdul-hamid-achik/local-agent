import { expect, test } from "vitest";
import { platform, shipped } from "./release-binary";
import { cask, parseSums } from "./release-cask";

const sum = (n: number) => String(n).repeat(64).slice(0, 64);
const SUMS = [
  `${sum(1)}  local-agent_0.52.0_darwin_arm64.tar.gz`,
  `${sum(2)}  local-agent_0.52.0_darwin_amd64.tar.gz`,
  `${sum(3)} *local-agent_0.52.0_linux_arm64.tar.gz`,
  `${sum(4)}  local-agent_0.52.0_linux_amd64.tar.gz`,
  `${sum(5)}  local-agent-v0.52.0-src.tar.gz`,
].join("\n");

test("the cask names each platform's archive and its sum", () => {
  const text = cask("v0.52.0", parseSums(SUMS));
  expect(text).toContain('version "0.52.0"');
  for (const [n, target] of [
    [1, "darwin_arm64"],
    [2, "darwin_amd64"],
    [3, "linux_arm64"],
    [4, "linux_amd64"],
  ] as const) {
    const lines = text.split("\n");
    const url = lines.findIndex((line) =>
      line.includes(`local-agent_#{version}_${target}.tar.gz`),
    );
    expect(url).toBeGreaterThan(0);
    // The sum right above the url is that archive's.
    expect(lines[url - 1]?.trim()).toBe(`sha256 "${sum(n)}"`);
  }
  // Not deprecated any more, and the archive is flat.
  expect(text).not.toContain("deprecate!");
  expect(text).toContain('binary "local-agent"');
});

test("a missing archive sum or a bad tag is refused", () => {
  const partial = parseSums(SUMS.split("\n").slice(1).join("\n"));
  expect(() => cask("v0.52.0", partial)).toThrow(
    /no checksum for local-agent_0.52.0_darwin_arm64/,
  );
  expect(() => cask("latest", parseSums(SUMS))).toThrow(/not a version/);
});

test("the archive's platform words and its ui/ files", () => {
  expect(platform("darwin", "arm64")).toBe("darwin_arm64");
  expect(platform("linux", "x64")).toBe("linux_amd64");
  expect(() => platform("win32", "x64")).toThrow(/no release build/);
  expect(shipped("/app.tui")).toBe(true);
  expect(shipped("/themes/nord.tcss")).toBe(true);
  expect(shipped("/golden/sample-80.txt")).toBe(false);
  expect(shipped("/samples")).toBe(false);
  expect(shipped("/.themed-1-nord-app.tui")).toBe(false);
});

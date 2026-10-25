import { LIMITS } from "@local-agent/runtime";
import { expect, test } from "vitest";
import {
  bytes,
  duration,
  features,
  limitRows,
} from "#desktop/renderer/reference";

test("a byte count reads in the unit it is set in", () => {
  expect(bytes(8 * 1024 * 1024)).toBe("8 MiB");
  expect(bytes(512 * 1024)).toBe("512 KiB");
  expect(bytes(1_000_000)).toBe("1 MB");
  expect(bytes(3_000_000)).toBe("3 MB");
  expect(bytes(1_500)).toBe("1,500 bytes");
  expect(bytes(512)).toBe("512 bytes");
  expect(bytes(0)).toBe("0 bytes");
});

test("a duration reads in minutes, seconds or milliseconds", () => {
  expect(duration(300_000)).toBe("5 min");
  expect(duration(90_000)).toBe("90 s");
  expect(duration(1_500)).toBe("1,500 ms");
  expect(duration(0)).toBe("0 ms");
});

test("the bounds table shows every bound the runtime has", () => {
  const shown = JSON.stringify(limitRows(LIMITS));
  for (const key of Object.keys(LIMITS) as (keyof typeof LIMITS)[])
    expect(
      JSON.stringify(limitRows({ ...LIMITS, [key]: LIMITS[key] * 3 })),
      key,
    ).not.toBe(shown);
});

test("no feature links to the Reference page it is listed on", () => {
  // A feature the workbench has no view for has no route, not this one.
  expect(features.filter((f) => (f.route as string) === "reference")).toEqual(
    [],
  );
  expect(features.filter((f) => !f.route).map((f) => f.name)).toEqual([
    "Fullscreen TUI",
    "Inline TUI",
  ]);
  expect(new Set(features.map((f) => f.name)).size).toBe(features.length);
});

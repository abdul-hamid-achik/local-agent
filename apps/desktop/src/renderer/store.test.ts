import { expect, test } from "vitest";
import { changeWorkspace } from "#desktop/renderer/store";

test("a different workspace drops the session handle of the old one", () => {
  const target = {
    workspace: "/repo-a",
    repoRoot: "/repo-a",
    resumeSession: "s-1",
  };
  changeWorkspace(target, "/repo-b");
  expect(target).toEqual({
    workspace: "/repo-b",
    repoRoot: "/repo-a",
    resumeSession: "",
  });
});

test("picking the current workspace again keeps the session handle", () => {
  const target = {
    workspace: "/repo-a",
    repoRoot: "/repo-a",
    resumeSession: "s-1",
  };
  changeWorkspace(target, "/repo-a");
  expect(target.resumeSession).toBe("s-1");
});

test("the repo root counts as the workspace while none is chosen", () => {
  const target = { workspace: "", repoRoot: "/repo-a", resumeSession: "s-1" };
  changeWorkspace(target, "/repo-a");
  expect(target).toEqual({
    workspace: "",
    repoRoot: "/repo-a",
    resumeSession: "s-1",
  });
  changeWorkspace(target, "/repo-b");
  expect(target.workspace).toBe("/repo-b");
  expect(target.resumeSession).toBe("");
});

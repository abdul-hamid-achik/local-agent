import { expect, test } from "vitest";
import { projectEnvironment } from "#desktop/sidecar/project-env";

test("only the toolchain's variables pass, whatever a secret is named", () => {
  expect(
    projectEnvironment({
      PATH: "/opt/bin:/usr/bin",
      HOME: "/home/a",
      LC_ALL: "C",
      XDG_CONFIG_HOME: "/cfg",
      ASDF_DATA_DIR: "/asdf",
      OPENAI_API_KEY: "sk-1",
      OLLAMA_HOST: "http://127.0.0.1:11434",
      LOCAL_AGENT_PROVIDER_CONFIG: "{}",
      AWS_SECRET_ACCESS_KEY: "s",
      LCX_SECRET: "not a locale variable",
    }),
  ).toEqual({
    PATH: "/opt/bin:/usr/bin",
    HOME: "/home/a",
    LC_ALL: "C",
    XDG_CONFIG_HOME: "/cfg",
    ASDF_DATA_DIR: "/asdf",
  });
});

test("a missing PATH falls back to the system's", () => {
  expect(projectEnvironment({ HOME: "/home/a" })).toEqual({
    HOME: "/home/a",
    PATH: "/usr/bin:/bin",
  });
});

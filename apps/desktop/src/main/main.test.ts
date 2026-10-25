import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import {
  bunNotFoundMessage,
  isExecutableFile,
  resolveBun,
} from "#desktop/main/bun-path";
import {
  grantedMcpParams,
  TextFileGrants,
  userMcpConfigPath,
} from "#desktop/main/file-access";
import {
  type ConfinedContents,
  confine,
  externalUrl,
  staysInApp,
} from "#desktop/main/navigation";
import { LineDecoder } from "#desktop/main/ndjson";
import { writePrivateFile } from "#desktop/main/private-file";
import {
  persistentProviderEnv,
  providerEnvProblem,
} from "#desktop/main/provider-env";
import { loadSettings, saveSettings } from "#desktop/main/settings";
import { SidecarClient } from "#desktop/main/sidecar-client";

let dir = "";
beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "la-desktop-main-"));
});
afterEach(() => {
  rmSync(dir, { recursive: true, force: true });
});

describe("resolveBun", () => {
  const home = "/Users/someone";

  test("prefers BUN_INSTALL, then the usual install locations, then PATH", () => {
    const present = new Set([
      "/opt/homebrew/bin/bun",
      "/custom/bin/bun",
      "/pathdir/bun",
    ]);
    const lookup = (env: Record<string, string>) =>
      resolveBun({ env, home, isExecutable: (p) => present.has(p) });
    expect(lookup({ BUN_INSTALL: "/custom", PATH: "/pathdir" }).path).toBe(
      "/custom/bin/bun",
    );
    expect(lookup({ PATH: "/pathdir" }).path).toBe("/opt/homebrew/bin/bun");
    present.delete("/opt/homebrew/bin/bun");
    expect(lookup({ PATH: "/other:/pathdir" }).path).toBe("/pathdir/bun");
  });

  test("a Finder-minimal PATH still finds ~/.bun/bin", () => {
    const resolved = resolveBun({
      env: { PATH: "/usr/bin:/bin:/usr/sbin:/sbin" },
      home,
      isExecutable: (p) => p === "/Users/someone/.bun/bin/bun",
    });
    expect(resolved.path).toBe("/Users/someone/.bun/bin/bun");
  });

  test("a relative PATH entry is never probed", () => {
    const resolved = resolveBun({
      env: { PATH: ".:bin:/usr/bin" },
      home,
      isExecutable: () => false,
    });
    expect(resolved.tried).toContain("/usr/bin/bun");
    expect(resolved.tried).not.toContain("bun");
    expect(resolved.tried).not.toContain("bin/bun");
  });

  test("reports every place it looked when nothing is executable", () => {
    const resolved = resolveBun({
      env: { PATH: "/a:/b" },
      home,
      isExecutable: () => false,
    });
    expect(resolved.path).toBeUndefined();
    expect(resolved.tried).toEqual([
      "/Users/someone/.bun/bin/bun",
      "/opt/homebrew/bin/bun",
      "/usr/local/bin/bun",
      "/a/bun",
      "/b/bun",
    ]);
    const message = bunNotFoundMessage(resolved.tried);
    expect(message).toContain("/opt/homebrew/bin/bun");
    expect(message).toContain("https://bun.sh");
  });

  test("the default check requires an executable regular file", () => {
    const file = join(dir, "bun");
    writeFileSync(file, "#!/bin/sh\n");
    chmodSync(file, 0o644);
    expect(isExecutableFile(file)).toBe(false);
    chmodSync(file, 0o755);
    expect(isExecutableFile(file)).toBe(true);
    expect(isExecutableFile(join(dir, "missing"))).toBe(false);
    // A directory named bun is not an executable.
    mkdirSync(join(dir, "nested", "bun"), { recursive: true });
    expect(isExecutableFile(join(dir, "nested", "bun"))).toBe(false);
  });
});

describe("LineDecoder", () => {
  test("keeps a multi-byte character split across chunks intact", () => {
    const bytes = new TextEncoder().encode('{"text":"héllo ✓ 🙂"}\n');
    for (let split = 1; split < bytes.length; split += 1) {
      const decoder = new LineDecoder();
      const lines = [
        ...decoder.push(bytes.slice(0, split)),
        ...decoder.push(bytes.slice(split)),
      ];
      expect(lines).toEqual(['{"text":"héllo ✓ 🙂"}']);
    }
  });

  test("buffers a partial line and emits several lines from one chunk", () => {
    const decoder = new LineDecoder();
    const encode = (text: string) => new TextEncoder().encode(text);
    expect(decoder.push(encode("one\ntw"))).toEqual(["one"]);
    expect(decoder.push(encode("o\nthree\n"))).toEqual(["two", "three"]);
    expect(decoder.push(encode("tail"))).toEqual([]);
    expect(decoder.push(encode("\n"))).toEqual(["tail"]);
  });
});

describe("TextFileGrants", () => {
  test("only a file the user picked can be read or written", () => {
    const config = join(dir, "mcp.json");
    const other = join(dir, "other.json");
    writeFileSync(config, "[]");
    writeFileSync(other, "[1]");
    const grants = new TextFileGrants();
    expect(() => grants.read(config)).toThrow("Browse");
    grants.grant(config);
    expect(grants.read(config)).toBe("[]");
    grants.write(config, "[2]");
    expect(readFileSync(config, "utf8")).toBe("[2]");
    expect(() => grants.read(other)).toThrow("Browse");
    expect(() => grants.write(other, "x")).toThrow("Browse");
    expect(readFileSync(other, "utf8")).toBe("[1]");
  });

  test("rejects non-JSON files even when picked, and unknown or odd paths", () => {
    const secret = join(dir, "id_rsa");
    writeFileSync(secret, "key");
    const grants = new TextFileGrants();
    grants.grant(secret);
    expect(() => grants.read(secret)).toThrow("Browse");
    expect(() => grants.read(join(dir, "missing.json"))).toThrow("Browse");
    expect(() => grants.read(undefined)).toThrow("path");
    expect(() => grants.read("")).toThrow("path");
  });

  test("a symlink to an ungranted file does not widen the grant", () => {
    const target = join(dir, "target.json");
    const link = join(dir, "link.json");
    writeFileSync(target, "{}");
    symlinkSync(target, link);
    const grants = new TextFileGrants();
    grants.grant(link);
    // The grant is the real file, so the link and the target are the same file.
    expect(grants.read(target)).toBe("{}");
    const stranger = join(dir, "stranger.json");
    writeFileSync(stranger, "{}");
    const sneaky = join(dir, "sneaky.json");
    symlinkSync(stranger, sneaky);
    expect(() => grants.read(sneaky)).toThrow("Browse");
  });

  test("a picked .json that links to a file of another kind is not granted", () => {
    const key = join(dir, "id_rsa");
    writeFileSync(key, "key");
    const link = join(dir, "config.json");
    symlinkSync(key, link);
    const grants = new TextFileGrants();
    grants.grant(link);
    expect(() => grants.read(link)).toThrow("Browse");
    expect(() => grants.write(link, "{}")).toThrow("Browse");
    expect(readFileSync(key, "utf8")).toBe("key");
  });

  test("a file granted by name is usable once it exists, and only as JSON", () => {
    const config = join(dir, "local-agent", "mcp.json");
    const grants = new TextFileGrants();
    grants.grantByName(config);
    expect(() => grants.read(config)).toThrow("Browse");
    mkdirSync(join(dir, "local-agent"));
    writeFileSync(config, "[]");
    expect(grants.read(config)).toBe("[]");
    grants.write(config, "[1]");
    expect(readFileSync(config, "utf8")).toBe("[1]");
    // Replaced by a link to a key file: the name no longer grants it.
    const key = join(dir, "id_rsa");
    writeFileSync(key, "key");
    rmSync(config);
    symlinkSync(key, config);
    expect(() => grants.read(config)).toThrow("Browse");
    expect(() => grants.read(key)).toThrow("Browse");
  });

  test("an MCP config reaches the sidecar only once granted, as its real path", () => {
    const picked = join(dir, "picked.json");
    const typed = join(dir, "typed.json");
    writeFileSync(picked, "[]");
    writeFileSync(typed, "[]");
    const link = join(dir, "picked-link.json");
    symlinkSync(picked, link);
    const grants = new TextFileGrants();
    grants.grant(picked);
    expect(
      grantedMcpParams("mcp.status", { configPath: link }, grants),
    ).toEqual({ configPath: realpathSync(picked) });
    expect(() =>
      grantedMcpParams("mcp.status", { configPath: typed }, grants),
    ).toThrow("Browse");
    expect(() => grantedMcpParams("mcp.status", {}, grants)).toThrow("path");
    const start = { runId: "r", mcpConfig: typed };
    expect(() => grantedMcpParams("agent.start", start, grants)).toThrow(
      "Browse",
    );
    expect(
      grantedMcpParams(
        "agent.start",
        { runId: "r", mcpConfig: picked },
        grants,
      ),
    ).toEqual({ runId: "r", mcpConfig: realpathSync(picked) });
    // A run without MCP, and any other method, pass as they are.
    const plain = { runId: "r" };
    expect(grantedMcpParams("agent.start", plain, grants)).toBe(plain);
    const read = { path: typed };
    expect(grantedMcpParams("workspace.read", read, grants)).toBe(read);
  });

  test("enforces the 1 MB limit on read and write", () => {
    const big = join(dir, "big.json");
    writeFileSync(big, "x".repeat(1_000_001));
    const grants = new TextFileGrants();
    grants.grant(big);
    expect(() => grants.read(big)).toThrow("1 MB");
    expect(() => grants.write(big, "x".repeat(1_000_001))).toThrow("1 MB");
    expect(() => grants.write(big, 42)).toThrow("Text is required");
  });
});

describe("userMcpConfigPath", () => {
  test("uses XDG_CONFIG_HOME only when it is absolute once trimmed", () => {
    const home = "/Users/someone";
    expect(userMcpConfigPath({ XDG_CONFIG_HOME: " /x " }, home)).toBe(
      "/x/local-agent/mcp.json",
    );
    expect(userMcpConfigPath({ XDG_CONFIG_HOME: "rel" }, home)).toBe(
      "/Users/someone/.config/local-agent/mcp.json",
    );
    expect(userMcpConfigPath({}, home)).toBe(
      "/Users/someone/.config/local-agent/mcp.json",
    );
  });
});

describe("providerEnvProblem", () => {
  test("a provider name the runtime would not match is refused", () => {
    expect(providerEnvProblem({ LOCAL_AGENT_PROVIDER: "chalupa" })).toBe(
      undefined,
    );
    expect(providerEnvProblem({})).toBe(undefined);
    expect(providerEnvProblem({ LOCAL_AGENT_PROVIDER: "Chalupa" })).toBe(
      'LOCAL_AGENT_PROVIDER must be exactly "chalupa"',
    );
    expect(providerEnvProblem({ LOCAL_AGENT_PROVIDER: " openai_local" })).toBe(
      'LOCAL_AGENT_PROVIDER must be exactly "openai_local"',
    );
  });
});

describe("settings", () => {
  test("every save leaves a Chalupa profile out, whoever saves", () => {
    const path = join(dir, "settings.json");
    const settings = {
      repoRoot: "/repo",
      providerEnv: {
        LOCAL_AGENT_PROVIDER: "chalupa",
        LOCAL_AGENT_PROVIDER_CONFIG: '{"type":"ollama_remote"}',
        LOCAL_AGENT_LOCAL_ONLY: "false",
        LOCAL_AGENT_THINK: "0",
      },
    };
    // As repoRoot() saves: the whole in-memory object.
    saveSettings(path, settings);
    expect(JSON.parse(readFileSync(path, "utf8"))).toEqual({
      repoRoot: "/repo",
      providerEnv: { LOCAL_AGENT_THINK: "0" },
    });
    // The running app keeps the profile for its sidecar.
    expect(settings.providerEnv.LOCAL_AGENT_PROVIDER_CONFIG).toBeDefined();
    expect(loadSettings(path)).toEqual({
      repoRoot: "/repo",
      providerEnv: { LOCAL_AGENT_THINK: "0" },
    });
    expect(statSync(path).mode & 0o777).toBe(0o600);
    expect(loadSettings(join(dir, "missing.json"))).toEqual({});
  });
});

describe("persistentProviderEnv", () => {
  test("a Chalupa profile is never saved; other settings are", () => {
    const chalupa = {
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: '{"type":"ollama_remote"}',
      LOCAL_AGENT_THINK: "0",
    };
    expect(persistentProviderEnv(chalupa)).toEqual({ LOCAL_AGENT_THINK: "0" });
    // The run itself still gets all of it.
    expect(chalupa.LOCAL_AGENT_PROVIDER_CONFIG).toBeDefined();
    const local = {
      LOCAL_AGENT_PROVIDER: "openai_local",
      LOCAL_AGENT_PROVIDER_CONFIG: '{"type":"openai_local"}',
    };
    expect(persistentProviderEnv(local)).toEqual(local);
  });
});

describe("writePrivateFile", () => {
  test("creates the file 0600 and tightens an existing looser one", () => {
    const fresh = join(dir, "settings.json");
    writePrivateFile(fresh, "{}");
    expect(statSync(fresh).mode & 0o777).toBe(0o600);

    const old = join(dir, "old.json");
    writeFileSync(old, "{}");
    chmodSync(old, 0o644);
    writePrivateFile(old, '{"providerEnv":{}}');
    expect(statSync(old).mode & 0o777).toBe(0o600);
    expect(readFileSync(old, "utf8")).toBe('{"providerEnv":{}}');
    // The text went into a new 0600 file, not into the 0644 one.
    expect(readdirSync(dir).sort()).toEqual(["old.json", "settings.json"]);
  });

  test("a failed write leaves the old file as it was", () => {
    const old = join(dir, "old.json");
    writeFileSync(old, "kept");
    chmodSync(dir, 0o500);
    try {
      expect(() => writePrivateFile(old, "new")).toThrow();
    } finally {
      chmodSync(dir, 0o700);
    }
    expect(readFileSync(old, "utf8")).toBe("kept");
    expect(readdirSync(dir)).toEqual(["old.json"]);
  });
});

describe("SidecarClient lifecycle", () => {
  test("a missing Bun rejects invoke with what was tried", async () => {
    const exits: (string | undefined)[] = [];
    const client = new SidecarClient(
      dir,
      {},
      {
        resolveBun: () => ({ tried: ["/nowhere/bun"] }),
      },
    );
    client.onExit = (_code, message) => exits.push(message);
    client.start();
    expect(client.running).toBe(false);
    await expect(client.invoke("hello")).rejects.toThrow("/nowhere/bun");
    expect(exits).toHaveLength(1);
    expect(exits[0]).toContain("https://bun.sh");
  });

  test("a spawn error rejects pending and future invokes instead of hanging", async () => {
    const missing = join(dir, "not-bun");
    const client = new SidecarClient(
      dir,
      {},
      {
        resolveBun: () => ({ path: missing, tried: [missing] }),
      },
    );
    const exits: (string | undefined)[] = [];
    client.onExit = (_code, message) => exits.push(message);
    client.start();
    // Written before the error event arrives, so it is genuinely pending.
    const pending = client.invoke("hello");
    await expect(pending).rejects.toThrow(missing);
    await expect(client.invoke("again")).rejects.toThrow("Could not start");
    expect(client.running).toBe(false);
    expect(exits).toHaveLength(1);
  });

  test("an early exit rejects pending invokes with the exit code and stderr", async () => {
    const script = join(dir, "dies.mjs");
    writeFileSync(
      script,
      'console.error("boom from sidecar"); process.exit(3);',
    );
    const client = new SidecarClient(
      dir,
      {},
      {
        resolveBun: () => ({
          path: process.execPath,
          tried: [process.execPath],
        }),
        script,
      },
    );
    const exited = new Promise<string | undefined>((resolve) => {
      client.onExit = (_code, message) => resolve(message);
    });
    client.start();
    const pending = client.invoke("hello").catch((error: Error) => error);
    const message = await exited;
    expect(message).toContain("code 3");
    expect(message).toContain("boom from sidecar");
    expect(((await pending) as Error).message).toContain("boom from sidecar");
    await expect(client.invoke("later")).rejects.toThrow("code 3");
    expect(client.running).toBe(false);
  });

  test("a stopped client rejects pending invokes and reports no exit", async () => {
    const script = join(dir, "idle.mjs");
    writeFileSync(script, "setInterval(() => undefined, 1000);");
    const client = new SidecarClient(
      dir,
      {},
      {
        resolveBun: () => ({
          path: process.execPath,
          tried: [process.execPath],
        }),
        script,
      },
    );
    let exits = 0;
    client.onExit = () => {
      exits += 1;
    };
    client.start();
    const pending = client.invoke("hello");
    client.stop();
    await expect(pending).rejects.toThrow("stopped");
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(exits).toBe(0);
  });

  test("a stopped child's last output never reaches the stream", async () => {
    const script = join(dir, "late.mjs");
    writeFileSync(
      script,
      [
        'process.on("SIGTERM", () => {',
        '  process.stdout.write(JSON.stringify({ stream: "r1", event: { type: "text", text: "late" } }) + "\\n");',
        "  setTimeout(() => process.exit(0), 50);",
        "});",
        "setInterval(() => undefined, 1000);",
      ].join("\n"),
    );
    const client = new SidecarClient(
      dir,
      {},
      {
        resolveBun: () => ({
          path: process.execPath,
          tried: [process.execPath],
        }),
        script,
      },
    );
    const streamed: unknown[] = [];
    client.onStream = (envelope) => streamed.push(envelope);
    client.start();
    await new Promise((resolve) => setTimeout(resolve, 300));
    client.stop();
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(streamed).toEqual([]);
  });

  /** A client on a child script made of `lines`. */
  function scripted(name: string, lines: string[], stopGraceMs?: number) {
    const script = join(dir, name);
    writeFileSync(script, lines.join("\n"));
    return new SidecarClient(
      dir,
      {},
      {
        resolveBun: () => ({
          path: process.execPath,
          tried: [process.execPath],
        }),
        script,
        stopGraceMs,
      },
    );
  }

  const alive = (pid: number) => {
    try {
      process.kill(pid, 0);
      return true;
    } catch {
      return false;
    }
  };

  async function gone(pid: number) {
    for (let tries = 0; tries < 100 && alive(pid); tries += 1)
      await new Promise((resolve) => setTimeout(resolve, 20));
    return !alive(pid);
  }

  test("a line that is not a frame stops the sidecar and rejects what waits", async () => {
    const pidFile = join(dir, "child.pid");
    const client = scripted("chatty.mjs", [
      'import { writeFileSync } from "node:fs";',
      `writeFileSync(${JSON.stringify(pidFile)}, String(process.pid));`,
      'process.stdin.on("data", () => process.stdout.write("\\u001b[31mloaded 3 plugins\\n"));',
      "setInterval(() => undefined, 1000);",
    ]);
    const exits: (string | undefined)[] = [];
    client.onExit = (_code, message) => exits.push(message);
    client.start();
    await expect(client.invoke("hello")).rejects.toThrow(
      'The sidecar wrote "loaded 3 plugins", not a frame',
    );
    expect(exits).toHaveLength(1);
    expect(client.running).toBe(false);
    await expect(client.invoke("again")).rejects.toThrow("not a frame");
    expect(await gone(Number(readFileSync(pidFile, "utf8")))).toBe(true);
  });

  test("an answer in the wrong shape fails only its own invoke", async () => {
    const client = scripted("odd.mjs", [
      'process.stdin.setEncoding("utf8");',
      'process.stdin.on("data", (data) => {',
      '  for (const line of data.split("\\n").filter(Boolean)) {',
      "    const { id, method } = JSON.parse(line);",
      "    const frame =",
      '      method === "odd" ? { id, ok: "yes" }',
      '      : method === "bare" ? { id, ok: false }',
      "      : { id, ok: true, result: 7 };",
      '    process.stdout.write(JSON.stringify(frame) + "\\n");',
      "  }",
      "});",
    ]);
    let exits = 0;
    client.onExit = () => {
      exits += 1;
    };
    client.start();
    try {
      await expect(client.invoke("odd")).rejects.toThrow("malformed frame");
      await expect(client.invoke("bare")).rejects.toThrow(
        "Sidecar request failed",
      );
      await expect(client.invoke("fine")).resolves.toBe(7);
      expect(client.running).toBe(true);
      expect(exits).toBe(0);
    } finally {
      await client.stop();
    }
  });

  test("a line too long to be a frame stops the sidecar", async () => {
    const client = scripted("endless.mjs", [
      'process.stdin.on("data", () => process.stdout.write("x".repeat(33 * 1024 * 1024)));',
      "setInterval(() => undefined, 1000);",
    ]);
    client.start();
    await expect(client.invoke("hello")).rejects.toThrow(
      "a line over 33554432 characters",
    );
    expect(client.running).toBe(false);
  });

  test("stop kills a child that ignores SIGTERM once the grace is over", async () => {
    const pidFile = join(dir, "stubborn.pid");
    const client = scripted(
      "stubborn.mjs",
      [
        'import { writeFileSync } from "node:fs";',
        `writeFileSync(${JSON.stringify(pidFile)}, String(process.pid));`,
        'process.on("SIGTERM", () => undefined);',
        "setInterval(() => undefined, 1000);",
      ],
      200,
    );
    client.start();
    for (let tries = 0; tries < 100 && !existsSync(pidFile); tries += 1)
      await new Promise((resolve) => setTimeout(resolve, 20));
    const pid = Number(readFileSync(pidFile, "utf8"));
    const started = Date.now();
    await client.stop();
    expect(Date.now() - started).toBeGreaterThanOrEqual(150);
    expect(alive(pid)).toBe(false);
  });

  test("decodes multi-byte replies and stream frames from the child", async () => {
    const script = join(dir, "echo.mjs");
    writeFileSync(
      script,
      [
        'process.stdin.setEncoding("utf8");',
        'process.stdin.on("data", (data) => {',
        "  const { id } = JSON.parse(data.trim());",
        '  const frame = JSON.stringify({ id, ok: true, result: { text: "héllo ✓ 🙂" } }) + "\\n";',
        "  const bytes = Buffer.from(frame);",
        "  process.stdout.write(bytes.subarray(0, 21));",
        "  setTimeout(() => process.stdout.write(bytes.subarray(21)), 30);",
        "});",
      ].join("\n"),
    );
    const client = new SidecarClient(
      dir,
      {},
      {
        resolveBun: () => ({
          path: process.execPath,
          tried: [process.execPath],
        }),
        script,
      },
    );
    client.start();
    try {
      await expect(client.invoke("hello")).resolves.toEqual({
        text: "héllo ✓ 🙂",
      });
    } finally {
      client.stop();
    }
  });
});

describe("navigation", () => {
  test("only a web link opens outside", () => {
    expect(externalUrl("https://local-agent.dev/safety")).toBe(
      "https://local-agent.dev/safety",
    );
    expect(externalUrl("http://example.com")).toBe("http://example.com/");
    for (const url of [
      "file:///etc/passwd",
      "javascript:alert(1)",
      "data:text/html,<p>x</p>",
      "local-agent://x",
      "not a url",
    ])
      expect(externalUrl(url)).toBeUndefined();
  });

  test("a navigation stays only on the app's own page", () => {
    const dev = "http://localhost:5173/#/chat";
    expect(staysInApp("http://localhost:5173/#/sessions", dev)).toBe(true);
    expect(staysInApp("http://localhost:5174/", dev)).toBe(false);
    expect(staysInApp("https://example.com/", dev)).toBe(false);
    const packaged = "file:///Applications/X.app/dist-renderer/index.html";
    expect(staysInApp(`${packaged}#/chat`, packaged)).toBe(true);
    expect(staysInApp("file:///tmp/evil.html", packaged)).toBe(false);
    expect(staysInApp("https://example.com/", packaged)).toBe(false);
    expect(staysInApp("not a url", packaged)).toBe(false);
  });

  test("the window refuses any frame's navigation or redirect away from home", () => {
    const listeners = new Map<string, (details: never) => void>();
    let openHandler = (_details: { url: string }) => ({ action: "deny" });
    const contents: ConfinedContents = {
      setWindowOpenHandler(handler) {
        openHandler = handler;
      },
      on(event: string, listener: (details: never) => void) {
        listeners.set(event, listener);
      },
    };
    const opened: string[] = [];
    confine(contents, "http://localhost:5173/", (url) => opened.push(url));
    const navigate = (
      event: string,
      url: string,
      isMainFrame: boolean,
    ): boolean => {
      let prevented = false;
      const listener = listeners.get(event) as (details: object) => void;
      listener({ url, isMainFrame, preventDefault: () => (prevented = true) });
      return prevented;
    };

    expect(openHandler({ url: "https://example.com/" })).toEqual({
      action: "deny",
    });
    for (const event of ["will-frame-navigate", "will-redirect"]) {
      expect(navigate(event, "http://localhost:5173/#/sessions", true)).toBe(
        false,
      );
      expect(navigate(event, "https://example.com/main", true)).toBe(true);
      expect(navigate(event, "https://example.com/frame", false)).toBe(true);
      expect(navigate(event, "file:///etc/hosts", true)).toBe(true);
    }
    // A new window and a main frame go to openOutside, which opens only a
    // web link; a subframe goes nowhere.
    expect(opened).toEqual([
      "https://example.com/",
      "https://example.com/main",
      "file:///etc/hosts",
      "https://example.com/main",
      "file:///etc/hosts",
    ]);
  });
});

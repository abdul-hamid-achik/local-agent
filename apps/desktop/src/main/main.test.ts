import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
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
import { TextFileGrants } from "#desktop/main/file-access";
import { LineDecoder } from "#desktop/main/ndjson";
import { writePrivateFile } from "#desktop/main/private-file";
import { persistentProviderEnv } from "#desktop/main/provider-env";
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

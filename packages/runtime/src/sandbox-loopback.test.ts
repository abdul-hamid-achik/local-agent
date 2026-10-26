import { createSocket } from "node:dgram";
import { mkdir, mkdtemp, realpath, rm } from "node:fs/promises";
import { connect, createServer, type Server } from "node:net";
import { networkInterfaces, tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import {
  type CommandResult,
  createCommandProposal,
  executeApprovedCommand,
} from "#runtime/command";
import {
  type CommandSandbox,
  sandboxedCommand,
  sandboxUnavailable,
} from "#runtime/sandbox";

/** Every profile rule the loopback mode adds. Absent without it. */
const LOOPBACK_RULES = [
  '(allow network-inbound (local tcp "localhost:*"))',
  '(allow network-outbound (remote tcp "localhost:*"))',
];

describe("the loopback profile", () => {
  let home: string;
  let workspace: string;
  beforeAll(async () => {
    home = await realpath(await mkdtemp(join(tmpdir(), "la-sbx-lo-")));
    workspace = join(home, "projects", "app");
    await mkdir(workspace, { recursive: true });
  });
  afterAll(() => rm(home, { recursive: true, force: true }));

  const profileOf = (sandbox: CommandSandbox) => {
    const { args } = sandboxedCommand(
      ["/usr/bin/true"],
      workspace,
      { PATH: "/usr/bin:/bin" },
      { home, ...sandbox },
    );
    return args[args.indexOf("-p") + 1] ?? "";
  };

  test("adds the loopback rules only when loopback is on", () => {
    const on = profileOf({ loopback: true });
    for (const rule of LOOPBACK_RULES) expect(on).toContain(rule);
    // Nothing about any other address, protocol or direction.
    expect(on).not.toMatch(/\(remote (ip|udp)/);
    expect(on).not.toMatch(/\(local (ip|udp)/);
    expect(on).not.toMatch(/system-socket/);
  });

  test("has no network rule at all when loopback is off", () => {
    for (const sandbox of [{}, { loopback: false }]) {
      const off = profileOf(sandbox);
      for (const rule of LOOPBACK_RULES) expect(off).not.toContain(rule);
      expect(off).not.toMatch(/network-/);
    }
  });
});

// The live tests need the real Seatbelt. A non-darwin host skips them, and
// so does a host where sandbox-exec cannot apply a profile.
const available = sandboxUnavailable(tmpdir()) === undefined;

describe.runIf(available)("a loopback sandboxed command", () => {
  let base: string;
  let home: string;
  let workspace: string;
  const servers: Server[] = [];
  /** Listening outside the sandbox (this test process), on loopback. */
  let outside: number;
  let outside6: number | undefined;
  /** This machine's own non-loopback address: a LAN IPv4 address, if any. */
  let lanAddress: string | undefined;
  /** Whether this machine can reach the internet (a control for test (d)). */
  let internetProbe: string;

  const listen = (host: string) =>
    new Promise<number>((resolve, reject) => {
      const server = createServer((socket) => socket.end("ok"));
      servers.push(server);
      server.on("error", reject);
      server.listen(0, host, () =>
        resolve((server.address() as { port: number }).port),
      );
    });

  /** Connects from outside the sandbox: the control for each "reached" claim. */
  const connectOutside = (host: string, port: number, timeout = 3_000) =>
    new Promise<string>((resolve) => {
      const socket = connect({ host, port });
      socket.setTimeout(timeout, () => {
        socket.destroy();
        resolve("TIMEOUT");
      });
      socket.on("connect", () => {
        socket.destroy();
        resolve("ok");
      });
      socket.on("error", (e: NodeJS.ErrnoException) =>
        resolve(e.code ?? "error"),
      );
    });

  beforeAll(async () => {
    base = await realpath(await mkdtemp("/private/var/tmp/la-sbx-lo-"));
    home = join(base, "home");
    workspace = join(home, "projects", "app");
    await mkdir(workspace, { recursive: true });
    outside = await listen("127.0.0.1");
    outside6 = await listen("::1").catch(() => undefined);
    const lan = Object.values(networkInterfaces())
      .flat()
      .find((i) => i?.family === "IPv4" && !i.internal);
    lanAddress = lan?.address;
    internetProbe = await connectOutside("1.1.1.1", 443, 3_000);
  });
  afterAll(async () => {
    for (const server of servers)
      await new Promise((resolve) => server.close(resolve));
    await rm(base, { recursive: true, force: true });
  });

  /**
   * Runs a Bun script in the workspace, confined with `loopback` as given, or
   * unconfined when `loopback` is undefined. Returns what it printed.
   */
  async function run(
    script: string,
    loopback: boolean | undefined,
  ): Promise<CommandResult> {
    const proposal = await createCommandProposal(
      [process.execPath, "-e", script],
      workspace,
      20_000,
    );
    return executeApprovedCommand(
      proposal,
      AbortSignal.timeout(20_000),
      loopback === undefined ? undefined : { home, loopback },
    );
  }

  /**
   * Prints "ok" when `body` completes, or the error code it failed with, which
   * is what a sandbox denial looks like from Bun (ECONNREFUSED for a connect).
   */
  async function attempt(body: string, loopback: boolean | undefined) {
    const result = await run(
      `const net = require("node:net");
       (async () => { ${body} })().then(() => console.log("ok"), (e) => console.log(e.code ?? e.cause?.code ?? e.message));`,
      loopback,
    );
    expect(result.sandboxed).toBe(loopback === undefined ? undefined : true);
    return result.output.trim();
  }

  /** Starts a server in the sandbox on `host` and connects to it there. */
  const serveAndConnect = (host: string, port: number | 0 = 0) =>
    `await new Promise((resolve, reject) => {
       const srv = net.createServer((c) => c.end("ok"));
       srv.on("error", reject);
       srv.listen(${port}, ${JSON.stringify(host)}, () => {
         const c = net.connect(srv.address().port, ${JSON.stringify(host)});
         c.on("connect", () => { c.destroy(); srv.close(); resolve(); });
         c.on("error", (e) => { srv.close(); reject(e); });
       });
     })`;

  /** Connects from the sandbox to `host:port`. */
  const connectInside = (host: string, port: number) =>
    `await new Promise((resolve, reject) => {
       const s = net.connect({ host: ${JSON.stringify(host)}, port: ${port}, timeout: 3000 });
       s.on("connect", () => { s.destroy(); resolve(); });
       s.on("timeout", () => { s.destroy(); const e = new Error("timed out"); e.code = "ETIMEDOUT"; reject(e); });
       s.on("error", reject);
     })`;

  test("(a) starts a server on 127.0.0.1 and connects to it", async () => {
    expect(await attempt(serveAndConnect("127.0.0.1"), true)).toBe("ok");
  });

  test("(b) reaches a listener the test opened outside the sandbox", async () => {
    expect(await connectOutside("127.0.0.1", outside)).toBe("ok");
    expect(await attempt(connectInside("127.0.0.1", outside), true)).toBe("ok");
  });

  test("(c) without loopback, neither (a) nor (b) works", async () => {
    expect(await connectOutside("127.0.0.1", outside)).toBe("ok");
    expect(await attempt(serveAndConnect("127.0.0.1"), false)).toMatch(
      /EPERM|ECONNREFUSED/,
    );
    expect(await attempt(connectInside("127.0.0.1", outside), false)).toBe(
      "ECONNREFUSED",
    );
  });

  test("(d) a connect to a non-loopback address still fails, with a control", async () => {
    // Control: the internet is reachable outside the sandbox. Without that
    // the "fails" below would prove nothing.
    expect(
      internetProbe,
      "1.1.1.1:443 is unreachable outside the sandbox",
    ).toBe("ok");
    expect(await attempt(connectInside("1.1.1.1", 443), true)).not.toBe("ok");
    expect(await attempt(connectInside("1.1.1.1", 443), false)).not.toBe("ok");
  });

  test("(d) this machine's own LAN address: NOT refused (Seatbelt limit)", async () => {
    // Known deviation from the owner's spec. Seatbelt matches `localhost`
    // against this machine's own addresses too, so a connect to the LAN
    // address of this machine is allowed with loopback. This asserts that
    // actual behavior so it is visible; it must change if the rule changes.
    if (lanAddress === undefined) return;
    const lanPort = await new Promise<number>((resolve, reject) => {
      const server = createServer((socket) => socket.end("ok"));
      servers.push(server);
      server.on("error", reject);
      server.listen(0, "0.0.0.0", () =>
        resolve((server.address() as { port: number }).port),
      );
    });
    expect(await connectOutside(lanAddress, lanPort)).toBe("ok");
    expect(await attempt(connectInside(lanAddress, lanPort), true)).toBe("ok");
    expect(await attempt(connectInside(lanAddress, lanPort), false)).toBe(
      "ECONNREFUSED",
    );
  });

  test("(e) IPv6 loopback works with loopback, and not without", async () => {
    if (outside6 === undefined) return;
    expect(await connectOutside("::1", outside6)).toBe("ok");
    expect(await attempt(connectInside("::1", outside6), true)).toBe("ok");
    expect(await attempt(serveAndConnect("::1"), true)).toBe("ok");
    expect(await attempt(connectInside("::1", outside6), false)).toBe(
      "ECONNREFUSED",
    );
  });

  test("UDP stays closed with loopback", async () => {
    // Control: the same datagram leaves outside the sandbox.
    const udp = `await new Promise((resolve, reject) => { const s = require("node:dgram").createSocket("udp4"); s.on("error", reject); s.send("x", ${outside}, "127.0.0.1", (e) => (e ? reject(e) : resolve())); })`;
    expect(await attempt(udp, true)).toBe("EPERM");
    expect(await attempt(udp, false)).toBe("EPERM");
    const socket = createSocket("udp4");
    const sent = await new Promise<string>((resolve) => {
      socket.send("x", outside, "127.0.0.1", (e) =>
        resolve(e ? ((e as NodeJS.ErrnoException).code ?? "error") : "ok"),
      );
    });
    socket.close();
    expect(sent).toBe("ok");
    expect(await connectOutside("127.0.0.1", outside)).toBe("ok");
  });

  test("40 listeners outside the sandbox: all reached with loopback, none without", async () => {
    // Allow-only rules have no list to misread: every port answers alike.
    const ports: number[] = [];
    for (let i = 0; i < 40; i++) ports.push(await listen("127.0.0.1"));
    const list = JSON.stringify(ports);
    const probe = `const ports = ${list};
       const one = (port) => new Promise((resolve) => { const s = net.connect(port, "127.0.0.1"); s.on("connect", () => { s.destroy(); resolve("ok"); }); s.on("error", (e) => resolve(e.code)); });
       (async () => { const out = []; for (const p of ports) out.push(await one(p)); console.log(JSON.stringify(out)); })();`;
    const withLoopback = JSON.parse(
      (await run(`const net = require("node:net");\n${probe}`, true)).output,
    ) as string[];
    const without = JSON.parse(
      (await run(`const net = require("node:net");\n${probe}`, false)).output,
    ) as string[];
    expect(withLoopback.filter((r) => r === "ok")).toHaveLength(40);
    expect(without.filter((r) => r === "ECONNREFUSED")).toHaveLength(40);
  }, 120_000);
});

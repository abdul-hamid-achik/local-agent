// macOS Seatbelt confinement for the commands a host runs without a card.

import { spawnSync } from "node:child_process";
import {
  accessSync,
  constants,
  existsSync,
  lstatSync,
  mkdirSync,
  realpathSync,
  symlinkSync,
} from "node:fs";
import { homedir, tmpdir } from "node:os";
import { basename, dirname, isAbsolute, join, sep } from "node:path";

const SANDBOX_EXEC = "/usr/bin/sandbox-exec";
/** The sandbox's own folder, under the home folder: its TMPDIR and GOCACHE. */
const OWN_REL = "/Library/Caches/local-agent/sandbox";

/**
 * What a command may read in the home folder besides the workspace: the
 * toolchains and their caches. Every other path there (keys, tokens, mail,
 * browser profiles, other projects) is unreadable. `~/.gitconfig` is here
 * because git needs the user's name and email.
 */
const HOME_READS = [
  "/.bun",
  "/.cargo",
  "/.rustup",
  "/go",
  "/.asdf",
  "/.nvm",
  "/.volta",
  "/.pyenv",
  "/.rbenv",
  "/.deno",
  "/.local/bin",
  "/.local/share/uv",
  "/.local/share/mise",
  "/.local/share/opentui",
  "/.config/git",
  "/.cache/node",
  "/.cache/uv",
  "/.cache/pip",
  "/.cache/pnpm",
  "/.cache/deno",
  "/.cache/typescript",
  "/.cache/go-build",
  "/.cache/ms-playwright",
  "/.cache/puppeteer",
  "/Library/Caches/bun",
  "/Library/Caches/deno",
  "/Library/Caches/dev.biomejs.biome",
  "/Library/Caches/electron",
  "/Library/Caches/go-build",
  "/Library/Caches/golangci-lint",
  "/Library/Caches/Homebrew",
  "/Library/Caches/ms-playwright",
  "/Library/Caches/node-gyp",
  "/Library/Caches/pip",
  "/Library/Caches/pnpm",
  "/Library/Caches/tuimark",
  "/Library/Caches/typescript",
  "/Library/Caches/uv",
  "/Library/Caches/Yarn",
] as const;
const HOME_READ_FILES = ["/.gitconfig", "/.tool-versions"] as const;
/** Where those toolchains keep tokens: unreadable even inside them. */
const HOME_SECRETS = [
  "/.config/git/credentials",
  "/.cargo/credentials",
  "/.cargo/credentials.toml",
  "/.local/share/uv/credentials",
] as const;

/** The folders above `path` (absolute, or relative to a root as `/a/b`). */
function ancestors(path: string): string[] {
  const parts = path.split("/").filter(Boolean);
  return parts
    .slice(0, -1)
    .map((_, i) => `/${parts.slice(0, i + 1).join("/")}`);
}

/**
 * Folders above a readable path whose metadata a command may read: resolving
 * a real path (as Node does for every module) stats each of them.
 */
const HOME_ANCESTORS = [
  ...new Set([...HOME_READS, ...HOME_READ_FILES, OWN_REL].flatMap(ancestors)),
];

const quote = (text: string) => JSON.stringify(text);

/**
 * Paths in the workspace, at any depth, whose writes run later as the user: a
 * git folder (creating one, or its hooks, the config that names them, a
 * commondir that points git at another config, submodule and worktree git
 * data, and the attributes that pick a diff, filter or merge driver from the
 * user's own config, as `.gitattributes` does) and local-agent's own rules.
 * Creating `.git` is refused too, or a new one, a renamed folder or a
 * `gitdir:` file would carry hooks past these rules. Seatbelt matches these
 * paths in any case, as the default macOS filesystem does (`.GIT` is `.git`).
 */
const RUNS_LATER = String.raw`/(.*/)?(\.git(/(hooks|modules|worktrees|info)(/|$)|/(config(\.worktree)?|commondir)$|$)|\.gitattributes$|\.local-agent(/|$))`;

/**
 * The profile. Paths arrive as `-D` parameters, never spliced into the text:
 * a workspace path with quotes or parentheses cannot change the policy.
 * Later rules win, so each narrowing follows the grant it narrows.
 */
function profile({
  workspaceAncestors,
  bunNode,
  loopback,
}: {
  workspaceAncestors: number;
  bunNode: boolean;
  loopback: boolean;
}): string {
  const home = (rel: string) => `(string-append (param "HOME") ${quote(rel)})`;
  return [
    "(version 1)",
    "(deny default)",
    "(allow process-exec process-fork)",
    "(allow signal (target same-sandbox))",
    "(allow process-info* (target same-sandbox))",
    "(allow sysctl-read)",
    // User and group lookups, power assertions, notifications and logging;
    // nothing that opens apps, sends Apple events or reaches the keychain.
    "(allow mach-lookup",
    '  (global-name "com.apple.system.opendirectoryd.libinfo")',
    '  (global-name "com.apple.PowerManagement.control")',
    '  (global-name "com.apple.system.notification_center")',
    '  (global-name "com.apple.logd")',
    '  (global-name "com.apple.diagnosticd"))',
    "(allow ipc-posix-sem ipc-posix-shm)",
    "(allow file-ioctl)",
    // Reads: the system, but not the home folder, the shared temporary
    // folder, the user's own folder under /var/folders (its temporary files
    // and caches), other volumes or devices beyond the usual few.
    "(allow file-read*)",
    '(deny file-read* (subpath (param "HOME")) (subpath "/private/tmp")',
    '  (subpath (param "USER_DIR")) (subpath "/Volumes") (subpath "/dev"))',
    '(allow file-read* (subpath (param "WS"))',
    '  (subpath (param "OWN"))',
    ...HOME_READS.map((rel) => `  (subpath ${home(rel)})`),
    ...HOME_READ_FILES.map((rel) => `  (literal ${home(rel)})`),
    '  (literal "/dev") (literal "/dev/null") (literal "/dev/zero")',
    '  (literal "/dev/random") (literal "/dev/urandom")',
    '  (literal "/dev/dtracehelper") (regex #"^/dev/fd(/|$)"))',
    `(deny file-read*${HOME_SECRETS.map((rel) => ` (subpath ${home(rel)})`).join("")})`,
    '(allow file-read-metadata (literal (param "HOME")) (literal "/private/tmp")',
    ...HOME_ANCESTORS.map((rel) => `  (literal ${home(rel)})`),
    ...Array.from(
      { length: workspaceAncestors },
      (_, i) => `  (literal (param "WS_UP${i}"))`,
    ),
    "  )",
    // Writes: the workspace and the sandbox's own folder only, and never
    // what runs later as the user.
    '(allow file-write* (subpath (param "WS")) (subpath (param "OWN"))',
    '  (literal "/dev/null") (literal "/dev/zero") (regex #"^/dev/fd/"))',
    `(deny file-write* (regex (string-append "^" (param "WS_RE") ${quote(RUNS_LATER)})))`,
    // Network: none, not even this machine, unless `loopback` opens TCP to
    // this machine (below). Seatbelt's per-port rules are unreliable (a deny
    // for one port in a list can let it through), and every port open on
    // this machine (Ollama, an editor's or another agent's server, a
    // database) would be reachable without them, so there is no per-port
    // deny: loopback is all of it or nothing.
    ...(loopback
      ? [
          // Opt-in, `loopback` only. This opens every TCP port listening on
          // this machine to a confined command: Ollama, other agents' and
          // editors' servers (some run commands, e.g. opencode), databases.
          // Only TCP, both ways. Seatbelt names no address but `localhost`
          // or `*`, and `localhost` matches the machine's own addresses too,
          // so a command can also connect to this machine's LAN and IPv6
          // addresses and listen on 0.0.0.0 or ::, which other hosts on the
          // network may be able to reach (not tested: this machine has no
          // second host). It cannot reach any other host: a LAN gateway and
          // the internet are refused. No `network-bind` rule: binds succeeded
          // with the inbound rule alone, and an extra one changed nothing.
          // UDP stays closed (`ip` instead of `tcp` would open it).
          '(allow network-inbound (local tcp "localhost:*"))',
          '(allow network-outbound (remote tcp "localhost:*"))',
        ]
      : []),
    ...(bunNode
      ? [
          // Bun's links (see bunNodeFolder): readable, and creatable only
          // in the sense that creating one fails with "exists", which is
          // what Bun waits for. Replacing or removing one stays denied.
          '(allow file-read* (literal (param "BUN_NODE"))',
          '  (literal (string-append (param "BUN_NODE") "/node"))',
          '  (literal (string-append (param "BUN_NODE") "/bun")))',
          "(allow file-write-create",
          '  (literal (string-append (param "BUN_NODE") "/node"))',
          '  (literal (string-append (param "BUN_NODE") "/bun")))',
        ]
      : []),
  ].join("\n");
}

/** `text` as an extended regular expression that matches only itself. */
function literalRegex(text: string): string {
  return text.replace(/[.^$*+?()[\]{}|\\]/g, "\\$&");
}

export type CommandSandbox = Readonly<{
  /** The home folder whose reads are kept from a command (the user's). */
  home?: string;
  /**
   * Open TCP to this machine (every local port, in and out) for the command.
   * Off by default: the command then has no network at all. See `profile`.
   */
  loopback?: boolean;
}>;

type Host = Readonly<{
  platform: NodeJS.Platform;
  exists: (path: string) => boolean;
  probe: () => string | undefined;
}>;

let probed: { result: string | undefined } | undefined;

/** Applies an empty profile once: a host already inside a sandbox cannot nest one. */
function probeSandboxExec(): string | undefined {
  probed ??= {
    result: (() => {
      const run = spawnSync(
        SANDBOX_EXEC,
        ["-p", "(version 1)(allow default)", "--", "/usr/bin/true"],
        { encoding: "utf8", timeout: 5_000 },
      );
      if (run.status === 0) return undefined;
      const said = (run.stderr || run.error?.message || "").trim();
      return `sandbox-exec cannot apply a profile here${said ? `: ${said}` : ""}`;
    })(),
  };
  return probed.result;
}

const realHost: Host = {
  platform: process.platform,
  exists: existsSync,
  probe: probeSandboxExec,
};

/**
 * Why commands in `workspaceRoot` cannot run in the sandbox, or undefined
 * when they can. A host that gets a reason asks instead (or, unattended,
 * refuses): a command meant for the sandbox never runs outside it.
 */
export function sandboxUnavailable(
  workspaceRoot: string,
  homeFolder: string = homedir(),
  host: Host = realHost,
): string | undefined {
  if (host.platform !== "darwin")
    return "the command sandbox needs macOS (Seatbelt)";
  if (!host.exists(SANDBOX_EXEC)) return `${SANDBOX_EXEC} is missing`;
  let root: string;
  let home: string;
  try {
    root = realpathSync(workspaceRoot);
    home = realpathSync(homeFolder);
  } catch {
    return "the workspace or the home folder cannot be resolved";
  }
  // Every read the profile keeps from a command lives in the home folder.
  if (
    root === home ||
    `${home}${sep}`.startsWith(`${root}${sep}`) ||
    root === sep
  )
    return "the workspace contains your home folder";
  return host.probe();
}

const bunRevisions = new Map<string, string | undefined>();

/** The first executable `name` on `path`, as found there. */
function onPath(name: string, path: string | undefined): string | undefined {
  for (const folder of (path ?? "").split(":")) {
    if (!isAbsolute(folder)) continue;
    const file = join(folder, name);
    try {
      accessSync(file, constants.X_OK);
      return file;
    } catch {
      // Not here.
    }
  }
  return undefined;
}

/**
 * `bun --bun` puts a `node` that is Bun first on PATH: links in
 * /private/tmp/bun-node-<revision> (whatever TMPDIR says), which it uses only
 * when creating them fails with "exists". In the sandbox that create is
 * refused outright, so `node` would quietly stay Node. Made here, outside,
 * the links exist; the profile lets a command attempt the create (which then
 * fails with "exists") and nothing else, so it can neither make these links
 * point elsewhere nor make them for a `bun --bun` run outside to trust. A
 * link that leads anywhere but this Bun is left alone, and not offered.
 * `root` is where Bun looks; tests pass a folder of their own.
 */
export function bunNodeFolder(
  path: string | undefined,
  root = "/private/tmp",
): string | undefined {
  const bun = onPath("bun", path);
  if (!bun) return undefined;
  if (!bunRevisions.has(bun)) {
    const run = spawnSync(bun, ["--revision"], {
      encoding: "utf8",
      timeout: 5_000,
      env: { PATH: path },
    });
    bunRevisions.set(bun, /\+([0-9a-f]{7,40})\s*$/.exec(run.stdout ?? "")?.[1]);
  }
  const revision = bunRevisions.get(bun);
  if (!revision) return undefined;
  const folder = join(root, `bun-node-${revision}`);
  try {
    mkdirSync(folder, { recursive: true, mode: 0o700 });
    // Another user's folder could hold any link.
    const info = lstatSync(folder);
    if (!info.isDirectory() || info.uid !== process.getuid?.())
      return undefined;
    for (const name of ["node", "bun"]) {
      try {
        symlinkSync(bun, join(folder, name));
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
      }
      if (realpathSync(join(folder, name)) !== realpathSync(bun))
        return undefined;
    }
  } catch {
    return undefined;
  }
  return folder;
}

/**
 * The argv and environment that run `argv` in `cwd` (the workspace root)
 * under the profile. The environment gains HOME, a TMPDIR and a GOCACHE of
 * the sandbox's own: the real ones are not writable here, and a cache
 * written in the sandbox must not feed builds that run outside it.
 */
export function sandboxedCommand(
  argv: readonly string[],
  cwd: string,
  environment: NodeJS.ProcessEnv,
  sandbox: CommandSandbox,
): { file: string; args: string[]; env: NodeJS.ProcessEnv } {
  const home = realpathSync(sandbox.home ?? homedir());
  const own = `${home}${OWN_REL}`;
  const ownTmp = join(own, "tmp");
  const goCache = join(own, "go-build");
  mkdirSync(ownTmp, { recursive: true, mode: 0o700 });
  mkdirSync(goCache, { recursive: true, mode: 0o700 });
  let userTmp: string;
  try {
    userTmp = realpathSync(tmpdir());
  } catch {
    userTmp = ownTmp;
  }
  // macOS keeps a user's temporary files (T) beside their caches (C) and
  // data (0) in one folder under /var/folders: all of it stays unreadable.
  const userDir =
    basename(userTmp) === "T" && userTmp.startsWith("/private/var/folders/")
      ? dirname(userTmp)
      : userTmp;
  const up = ancestors(cwd);
  const bunNode = bunNodeFolder(environment.PATH);
  const params: Record<string, string> = {
    WS: cwd,
    WS_RE: literalRegex(cwd),
    HOME: home,
    OWN: own,
    // The user's temporary folder stays unreadable unless it is the
    // sandbox's own (an unresolvable TMPDIR falls back to that).
    USER_DIR: userTmp === ownTmp ? join(own, "none") : userDir,
    ...Object.fromEntries(up.map((path, i) => [`WS_UP${i}`, path])),
    ...(bunNode ? { BUN_NODE: bunNode } : {}),
  };
  return {
    file: SANDBOX_EXEC,
    args: [
      "-p",
      profile({
        workspaceAncestors: up.length,
        bunNode: bunNode !== undefined,
        loopback: sandbox.loopback === true,
      }),
      ...Object.entries(params).flatMap(([key, value]) => [
        "-D",
        `${key}=${value}`,
      ]),
      "--",
      ...argv,
    ],
    env: Object.freeze({
      ...environment,
      HOME: home,
      TMPDIR: `${ownTmp}/`,
      GOCACHE: goCache,
    }),
  };
}

/** Dev launcher: builds main/preload, starts Vite, opens Electron. */
import { join } from "node:path";

const root = Bun.fileURLToPath(new URL("../", import.meta.url));
const RENDERER_URL = "http://127.0.0.1:5174";

const build = Bun.spawnSync(["bun", "run", "build:main"], {
  cwd: root,
  stdout: "inherit",
  stderr: "inherit",
});
if (build.exitCode !== 0) process.exit(build.exitCode ?? 1);

const vite = Bun.spawn(
  ["bun", "--bun", "vite", "--port", "5174", "--strictPort"],
  {
    cwd: root,
    stdout: "inherit",
    stderr: "inherit",
  },
);

async function waitForRenderer(): Promise<void> {
  const deadline = Date.now() + 60_000;
  for (;;) {
    try {
      const response = await fetch(RENDERER_URL, {
        signal: AbortSignal.timeout(2000),
      });
      if (response.ok) return;
    } catch {
      // Retry until the dev server accepts connections.
    }
    if (Date.now() > deadline) throw new Error("Vite dev server did not start");
    await Bun.sleep(250);
  }
}

let electron: ReturnType<typeof Bun.spawn> | undefined;
const shutdown = () => {
  electron?.kill();
  vite.kill();
};

try {
  await waitForRenderer();
  electron = Bun.spawn(
    [
      join(root, "node_modules", ".bin", "electron"),
      join(root, "dist-main", "main.mjs"),
    ],
    {
      cwd: root,
      env: { ...process.env, ELECTRON_RENDERER_URL: RENDERER_URL },
      stdout: "inherit",
      stderr: "inherit",
    },
  );
  process.once("SIGINT", shutdown);
  process.once("SIGTERM", shutdown);
  await electron.exited;
} finally {
  vite.kill();
  electron?.kill();
}

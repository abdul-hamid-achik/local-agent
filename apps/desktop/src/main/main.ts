import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
import { app, BrowserWindow, dialog, ipcMain, shell } from "electron";
import {
  grantedMcpParams,
  TextFileGrants,
  userMcpConfigPath,
} from "#desktop/main/file-access";
import { confine, externalUrl } from "#desktop/main/navigation";
import { providerEnvProblem } from "#desktop/main/provider-env";
import { holdQuit } from "#desktop/main/quit";
import {
  loadSettings,
  type Settings,
  saveSettings,
} from "#desktop/main/settings";
import { SidecarClient } from "#desktop/main/sidecar-client";

const textFiles = new TextFileGrants();
// The user's own MCP config, which the CLI loads by default, is editable
// without Browse; it is per user, never per workspace.
textFiles.grantByName(userMcpConfigPath(process.env, homedir()));

function settingsPath(): string {
  return join(app.getPath("userData"), "settings.json");
}

function looksLikeRepoRoot(path: string): boolean {
  return (
    existsSync(join(path, "package.json")) &&
    existsSync(join(path, "apps", "desktop", "src", "sidecar", "sidecar.ts"))
  );
}

function defaultRepoRoot(): string | undefined {
  const override = process.env.DESKTOP_REPO_ROOT;
  if (override && looksLikeRepoRoot(override)) return override;
  // dist-main/main.cjs -> apps/desktop/dist-main -> repo root is ../../..
  const sibling = join(dirname(app.getAppPath()), "..", "..", "..");
  // When running from source checkout via `electron dist-main/main.cjs`.
  const dev = join(process.cwd(), "..", "..");
  for (const candidate of [sibling, process.cwd(), dev]) {
    const normalized = join(candidate);
    if (looksLikeRepoRoot(normalized)) return normalized;
  }
  return undefined;
}

let mainWindow: BrowserWindow | undefined;
let sidecar: SidecarClient | undefined;
let settings: Settings = loadSettings(settingsPath());

function repoRoot(): string {
  if (settings.repoRoot && looksLikeRepoRoot(settings.repoRoot))
    return settings.repoRoot;
  const detected = defaultRepoRoot();
  if (detected) {
    settings = { ...settings, repoRoot: detected };
    saveSettings(settingsPath(), settings);
    return detected;
  }
  throw new Error("Repository checkout not found");
}

// Quitting waits for the sidecar to cancel its runs and exit (SIGKILL after
// the grace), so no run or command it started outlives the app.
const quitGuard = holdQuit(
  () => (sidecar?.running ? sidecar.stop() : undefined),
  () => app.quit(),
);

function startSidecar(): void {
  // A sidecar started while the app quits would be left to itself.
  if (quitGuard.quitting) throw new Error("The app is quitting");
  // stop() reports no exit, so a run on the replaced sidecar would wait for
  // a run_done that never comes: say it ended.
  const replaced = sidecar?.running ?? false;
  // The old one cancels its runs and exits on its own time.
  void sidecar?.stop();
  if (replaced) mainWindow?.webContents.send("desktop/sidecar-restarted");
  sidecar = new SidecarClient(repoRoot(), settings.providerEnv ?? {});
  sidecar.onStream = (envelope) => {
    mainWindow?.webContents.send("desktop/stream", envelope);
  };
  sidecar.onExit = (code, message) => {
    mainWindow?.webContents.send("desktop/sidecar-exit", code, message);
  };
  sidecar.start();
}

async function ensureRepoRoot(): Promise<string> {
  try {
    return repoRoot();
  } catch {
    const picked = await dialog.showOpenDialog({
      title: "Select the local-agent checkout",
      defaultPath: homedir(),
      properties: ["openDirectory"],
    });
    const candidate = picked.filePaths[0];
    if (!candidate || !looksLikeRepoRoot(candidate))
      throw new Error("A local-agent checkout is required");
    settings = { ...settings, repoRoot: candidate };
    saveSettings(settingsPath(), settings);
    return candidate;
  }
}

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1320,
    height: 880,
    minWidth: 960,
    minHeight: 620,
    title: "Local Agent Workbench",
    backgroundColor: "#0d1117",
    icon: join(repoRoot(), "apps", "desktop", "build", "icon.png"),
    webPreferences: {
      preload: join(import.meta.dirname, "preload.cjs"),
      contextIsolation: true,
      sandbox: true,
    },
  });
  // No other page loads in this window: its bridge drives the sidecar.
  const home =
    process.env.ELECTRON_RENDERER_URL ||
    pathToFileURL(
      join(import.meta.dirname, "..", "dist-renderer", "index.html"),
    ).href;
  confine(mainWindow.webContents, home, openOutside);
  void mainWindow.loadURL(home);
  mainWindow.on("closed", () => {
    mainWindow = undefined;
  });
}

function openOutside(url: string): void {
  const external = externalUrl(url);
  if (external) void shell.openExternal(external);
}

ipcMain.handle("desktop/rpc", (_event, method: string, params?: unknown) => {
  // The client says why it is down (Bun missing, early exit, ...).
  if (!sidecar) throw new Error("Sidecar is not running");
  return sidecar.invoke(method, grantedMcpParams(method, params, textFiles));
});

ipcMain.handle("desktop/repo-root", () => repoRoot());

ipcMain.handle("desktop/restart-sidecar", () => {
  startSidecar();
  return true;
});

ipcMain.handle(
  "desktop/set-provider-env",
  (_event, env: Record<string, string>) => {
    const problem = providerEnvProblem(env);
    if (problem) throw new Error(problem);
    settings = { ...settings, providerEnv: env };
    saveSettings(settingsPath(), settings);
    startSidecar();
    return true;
  },
);

ipcMain.handle("desktop/get-provider-env", () => settings.providerEnv ?? {});

ipcMain.handle(
  "desktop/pick",
  async (
    event,
    options: {
      kind: "directory" | "file";
      filters?: { name: string; extensions: string[] }[];
    },
  ) => {
    const request: Electron.OpenDialogOptions = {
      properties:
        options.kind === "directory" ? ["openDirectory"] : ["openFile"],
      filters: options.filters,
    };
    // The dialog belongs to the window that asked; with none, it stands
    // alone (a window made for it would stay open, empty).
    const owner = BrowserWindow.fromWebContents(event.sender) ?? mainWindow;
    const picked = owner
      ? await dialog.showOpenDialog(owner, request)
      : await dialog.showOpenDialog(request);
    const path = picked.filePaths[0];
    if (path && options.kind === "file") textFiles.grant(path);
    return path;
  },
);

// Only files the user picked in the dialog (the MCP config) are readable or
// writable from the renderer.
ipcMain.handle("desktop/read-text-file", (_event, path: string) =>
  textFiles.read(path),
);

ipcMain.handle(
  "desktop/write-text-file",
  (_event, path: string, text: string) => {
    textFiles.write(path, text);
    return true;
  },
);

ipcMain.handle("desktop/open-external", (_event, url: string) => {
  const external = externalUrl(url);
  if (!external) throw new Error("Only http(s) URLs may open");
  return shell.openExternal(external);
});

app.whenReady().then(async () => {
  try {
    await ensureRepoRoot();
  } catch (error) {
    await dialog.showErrorBox(
      "Local Agent Workbench",
      (error as Error).message,
    );
    app.quit();
    return;
  }
  startSidecar();
  createWindow();
  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

app.on("before-quit", (event) => quitGuard.handle(event));

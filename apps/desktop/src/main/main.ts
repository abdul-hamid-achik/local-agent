import { chmodSync, existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { app, BrowserWindow, dialog, ipcMain, shell } from "electron";
import { TextFileGrants } from "#desktop/main/file-access";
import { writePrivateFile } from "#desktop/main/private-file";
import { SidecarClient } from "#desktop/main/sidecar-client";

const textFiles = new TextFileGrants();
// The user's own MCP config, which the CLI loads by default, is editable
// without Browse; it is per user, never per workspace.
const xdgConfig = process.env.XDG_CONFIG_HOME;
textFiles.grant(
  join(
    xdgConfig?.startsWith("/") ? xdgConfig : join(homedir(), ".config"),
    "local-agent",
    "mcp.json",
  ),
);

type Settings = {
  repoRoot?: string;
  providerEnv?: Record<string, string>;
};

function settingsPath(): string {
  return join(app.getPath("userData"), "settings.json");
}

function loadSettings(): Settings {
  try {
    const settings = JSON.parse(
      readFileSync(settingsPath(), "utf8"),
    ) as Settings;
    // providerEnv holds API keys; an older file may have been written 0644.
    // A file that cannot be tightened still loads.
    try {
      chmodSync(settingsPath(), 0o600);
    } catch (error) {
      console.error(
        `[main] cannot tighten settings: ${(error as Error).message}`,
      );
    }
    return settings;
  } catch {
    return {};
  }
}

function saveSettings(settings: Settings): void {
  try {
    writePrivateFile(settingsPath(), JSON.stringify(settings, null, 2));
  } catch (error) {
    console.error(`[main] cannot save settings: ${(error as Error).message}`);
  }
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
let settings = loadSettings();

function repoRoot(): string {
  if (settings.repoRoot && looksLikeRepoRoot(settings.repoRoot))
    return settings.repoRoot;
  const detected = defaultRepoRoot();
  if (detected) {
    settings = { ...settings, repoRoot: detected };
    saveSettings(settings);
    return detected;
  }
  throw new Error("Repository checkout not found");
}

function startSidecar(): void {
  sidecar?.stop();
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
    saveSettings(settings);
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
  const devUrl = process.env.ELECTRON_RENDERER_URL;
  if (devUrl) {
    void mainWindow.loadURL(devUrl);
  } else {
    void mainWindow.loadFile(
      join(import.meta.dirname, "..", "dist-renderer", "index.html"),
    );
  }
  mainWindow.on("closed", () => {
    mainWindow = undefined;
  });
}

ipcMain.handle("desktop/rpc", (_event, method: string, params?: unknown) => {
  // The client says why it is down (Bun missing, early exit, ...).
  if (!sidecar) throw new Error("Sidecar is not running");
  return sidecar.invoke(method, params);
});

ipcMain.handle("desktop/repo-root", () => repoRoot());

ipcMain.handle("desktop/restart-sidecar", () => {
  startSidecar();
  return true;
});

ipcMain.handle(
  "desktop/set-provider-env",
  (_event, env: Record<string, string>) => {
    settings = { ...settings, providerEnv: env };
    saveSettings(settings);
    startSidecar();
    return true;
  },
);

ipcMain.handle("desktop/get-provider-env", () => settings.providerEnv ?? {});

ipcMain.handle(
  "desktop/pick",
  async (
    _event,
    options: {
      kind: "directory" | "file";
      filters?: { name: string; extensions: string[] }[];
    },
  ) => {
    const picked = await dialog.showOpenDialog(
      mainWindow ?? new BrowserWindow(),
      {
        properties:
          options.kind === "directory" ? ["openDirectory"] : ["openFile"],
        filters: options.filters,
      },
    );
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
  if (!/^https?:\/\//.test(url)) throw new Error("Only http(s) URLs may open");
  return shell.openExternal(url);
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

app.on("before-quit", () => {
  sidecar?.stop();
});

import { contextBridge, ipcRenderer } from "electron";
import type { StreamEnvelope } from "#desktop/shared/protocol";

export type DesktopBridge = {
  invoke: (method: string, params?: unknown) => Promise<unknown>;
  repoRoot: () => Promise<string>;
  restartSidecar: () => Promise<boolean>;
  setProviderEnv: (env: Record<string, string>) => Promise<boolean>;
  getProviderEnv: () => Promise<Record<string, string>>;
  pick: (options: {
    kind: "directory" | "file";
    filters?: { name: string; extensions: string[] }[];
  }) => Promise<string | undefined>;
  readTextFile: (path: string) => Promise<string>;
  writeTextFile: (path: string, text: string) => Promise<boolean>;
  openExternal: (url: string) => Promise<void>;
  onStream: (callback: (envelope: StreamEnvelope) => void) => () => void;
  onSidecarExit: (
    callback: (code: number | null, message?: string) => void,
  ) => () => void;
  /** Main replaced a running sidecar (a restart or a provider change). */
  onSidecarRestart: (callback: () => void) => () => void;
};

const bridge: DesktopBridge = {
  invoke: (method, params) => ipcRenderer.invoke("desktop/rpc", method, params),
  repoRoot: () => ipcRenderer.invoke("desktop/repo-root"),
  restartSidecar: () => ipcRenderer.invoke("desktop/restart-sidecar"),
  setProviderEnv: (env) => ipcRenderer.invoke("desktop/set-provider-env", env),
  getProviderEnv: () => ipcRenderer.invoke("desktop/get-provider-env"),
  pick: (options) => ipcRenderer.invoke("desktop/pick", options),
  readTextFile: (path) => ipcRenderer.invoke("desktop/read-text-file", path),
  writeTextFile: (path, text) =>
    ipcRenderer.invoke("desktop/write-text-file", path, text),
  openExternal: (url) => ipcRenderer.invoke("desktop/open-external", url),
  onStream: (callback) => {
    const listener = (_event: unknown, envelope: StreamEnvelope) =>
      callback(envelope);
    ipcRenderer.on("desktop/stream", listener);
    return () => ipcRenderer.removeListener("desktop/stream", listener);
  },
  onSidecarExit: (callback) => {
    const listener = (_event: unknown, code: number | null, message?: string) =>
      callback(code, message);
    ipcRenderer.on("desktop/sidecar-exit", listener);
    return () => ipcRenderer.removeListener("desktop/sidecar-exit", listener);
  },
  onSidecarRestart: (callback) => {
    const listener = () => callback();
    ipcRenderer.on("desktop/sidecar-restarted", listener);
    return () =>
      ipcRenderer.removeListener("desktop/sidecar-restarted", listener);
  },
};

contextBridge.exposeInMainWorld("desktop", bridge);

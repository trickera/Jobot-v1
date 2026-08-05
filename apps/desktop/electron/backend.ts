import { execFileSync, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { app } from "electron";

const BACKEND_HOST = "127.0.0.1";
const BACKEND_PORT = 48730;
const MAX_BOOT_LOG_LINES = 500;

let backendProcess: ChildProcessWithoutNullStreams | null = null;
const bootLogs: string[] = [];

export function pushBootLog(line: string): void {
  bootLogs.push(line);
  if (bootLogs.length > MAX_BOOT_LOG_LINES) {
    bootLogs.splice(0, bootLogs.length - MAX_BOOT_LOG_LINES);
  }
}

function createLineForwarder(onLine: (line: string) => void): (chunk: string) => void {
  let buffer = "";
  return (chunk: string) => {
    buffer += chunk;
    const lines = buffer.split(/\r?\n/);
    buffer = lines.pop() ?? "";
    for (const line of lines) {
      if (line.length > 0) onLine(line);
    }
  };
}

export function getBootLogs(): string[] {
  return [...bootLogs];
}

export function resolveBackendExe(): string {
  if (app.isPackaged) {
    return path.join(process.resourcesPath, "backend", "sencia-job-backend.exe");
  }
  return path.join(app.getAppPath(), "..", "backend-go", "bin", "sencia-job-backend.exe");
}

// resolveAppDataDir mirrors the exact join the Go backend already computes
// on its own via os.UserConfigDir() + "Sencia Job" (store.go, ocr_bundle.go)
// - used both for the packaged-mode env contract below and for the "open
// data folder" IPC handler (main.ts), so the renderer never needs to know
// or display the real filesystem path (which would contain the Windows
// username).
export function resolveAppDataDir(): string | undefined {
  return process.env.APPDATA ? path.join(process.env.APPDATA, "Sencia Job") : undefined;
}

export function prodEnv(): NodeJS.ProcessEnv {
  if (!app.isPackaged) {
    const workerPy = path.join(app.getAppPath(), "..", "browser-worker", "worker.py");
    return fs.existsSync(workerPy) ? { SENCIA_BROWSER_WORKER: workerPy } : {};
  }

  const resourcesBackend = path.join(process.resourcesPath, "backend");
  const pythonExe = path.join(resourcesBackend, "python", "python.exe");
  const workerPy = path.join(resourcesBackend, "backend-browser", "worker.py");
  const camoufoxBundle = path.join(resourcesBackend, "camoufox");

  // Packaged-mode contract (installer hardening, Phase 3): explicit,
  // deterministic paths instead of dev-machine assumptions. SENCIA_PACKAGED
  // tells the Go backend it must not silently fall back to a global Python
  // interpreter. appData/logDir mirror the exact join the backend already
  // computes on its own via os.UserConfigDir() + "Sencia Job" (store.go,
  // ocr_bundle.go) so existing users' local data stays in the same place -
  // this is informational/health-check plumbing, not a path override.
  const appData = resolveAppDataDir();
  const localAppData = process.env.LOCALAPPDATA ? path.join(process.env.LOCALAPPDATA, "Sencia Job") : undefined;

  const env: NodeJS.ProcessEnv = { SENCIA_PACKAGED: "1" };
  if (fs.existsSync(pythonExe)) env.SENCIA_PYTHON = pythonExe;
  if (fs.existsSync(workerPy)) env.SENCIA_BROWSER_WORKER = workerPy;
  if (fs.existsSync(path.join(camoufoxBundle, "camoufox.exe"))) env.SENCIA_CAMOUFOX_BUNDLE = camoufoxBundle;
  if (appData) {
    env.SENCIA_APP_DATA = appData;
    env.SENCIA_LOG_DIR = path.join(appData, "logs");
  }
  if (localAppData) env.SENCIA_CAMOUFOX_CACHE = path.join(localAppData, "browser");
  return env;
}

export function startBackend(token: string): ChildProcessWithoutNullStreams {
  if (backendProcess) {
    throw new Error("Backend process already started");
  }

  const exe = resolveBackendExe();
  // Packaged mode: pin cwd to the backend's own resource dir instead of
  // inheriting whatever cwd Windows happened to launch Electron with (a
  // desktop shortcut's "Start in" field, or Explorer's cwd, are not
  // guaranteed) - installer hardening Phase 3, matches the plan's "no
  // dev-only path assumptions in packaged mode".
  const cwd = app.isPackaged ? path.dirname(exe) : undefined;
  const child = spawn(exe, [], {
    env: { ...process.env, SENCIA_API_TOKEN: token, ...prodEnv() },
    cwd,
    windowsHide: true,
  });

  const forwardStdout = createLineForwarder(pushBootLog);
  const forwardStderr = createLineForwarder((line) => pushBootLog(`[stderr] ${line}`));
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdout.on("data", forwardStdout);
  child.stderr.on("data", forwardStderr);
  // Without this listener, a spawn failure (missing/corrupt backend exe,
  // AV quarantine, not executable) emits an unhandled 'error' event and
  // crashes the whole main process instead of surfacing in the boot logs
  // the ErrorBoundary already knows how to show.
  child.on("error", (err) => {
    pushBootLog(`[backend] failed to start: ${err.message} (exe: ${exe})`);
    backendProcess = null;
  });
  child.on("exit", (code, signal) => {
    pushBootLog(`[backend] exited (code=${code ?? "null"}, signal=${signal ?? "null"})`);
    backendProcess = null;
  });

  backendProcess = child;
  return child;
}

export function stopBackend(): void {
  const child = backendProcess;
  backendProcess = null;
  if (!child || child.pid === undefined) return;

  // /T kills the whole process tree (backend -> python worker -> Camoufox),
  // not just the direct child - the Go backend never gets a chance to run
  // its own cleanup under /F, so this tree-kill is the actual mechanism that
  // prevents an orphaned Python/Camoufox process (installer hardening Phase
  // 4). A bounded timeout keeps a hung taskkill from blocking app shutdown.
  pushBootLog("[ SHUTDOWN ] closing browser worker and backend");
  if (process.platform === "win32") {
    try {
      execFileSync("taskkill", ["/PID", String(child.pid), "/T", "/F"], { timeout: 5000 });
    } catch {
      // Process tree already gone, or taskkill itself timed out/failed —
      // stopBackend is best-effort and idempotent either way.
    }
  } else {
    child.kill();
  }
  pushBootLog("[ SHUTDOWN ] backend stopped");
}

export async function waitForHealth(timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`http://${BACKEND_HOST}:${BACKEND_PORT}/health`);
      if (response.ok) return true;
    } catch {
      // Backend not accepting connections yet — retry until the deadline.
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  return false;
}

import { app, BrowserWindow, dialog, ipcMain, Notification, session, shell } from "electron";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { getBootLogs, pushBootLog, resolveAppDataDir, startBackend, stopBackend, waitForHealth } from "./backend";
import {
  assertTrustedSender,
  createSecurityPolicy,
  isAllowedNavigation,
  SecurityPolicyError,
} from "./security-policy";

const isDev = !app.isPackaged;
const DEV_SERVER_URL = "http://127.0.0.1:1420";
const BACKEND_ORIGIN = "http://127.0.0.1:48730";
const PACKAGED_INDEX_PATH = path.join(__dirname, "../dist/index.html");
const securityPolicy = createSecurityPolicy({
  mode: isDev ? "dev" : "packaged",
  devOrigin: DEV_SERVER_URL,
  packagedIndexPath: PACKAGED_INDEX_PATH,
});

// Windows will not display a toast for a process it cannot identify. Without an
// AppUserModelID matching the Start Menu shortcut the installer creates,
// Notification.isSupported() still answers true and show() still succeeds --
// and the toast is silently dropped on the floor. The radar spent its whole
// existence "sending" notifications nobody could receive. Must match
// electron-builder.yml's appId, and must be set before any window exists.
app.setAppUserModelId("com.sencia.job");

let apiToken = "";
let mainWindow: BrowserWindow | null = null;

// Only ordinary web/mail links may leave the app. Anything else handed to
// shell.openExternal (file:, smb:, ms-msdt:, ...) is an OS-level execution
// vector controlled by a renderer string - deny by default.
const EXTERNAL_URL_SCHEMES = new Set(["http:", "https:", "mailto:"]);

function openExternalSafely(url: string): void {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    pushBootLog(`[ SECURITY ] blocked openExternal for unparseable url`);
    return;
  }
  if (parsed.username || parsed.password || !EXTERNAL_URL_SCHEMES.has(parsed.protocol)) {
    pushBootLog(`[ SECURITY ] blocked openExternal for unsafe URL`);
    return;
  }
  void shell.openExternal(url);
}

// shell.openPath executes whatever the path points at (.exe/.lnk/UNC), so the
// renderer is never allowed to pick the target. Only paths this main process
// itself produced (save-dialog results, the app data dir) may be opened or
// revealed - everything else is denied and logged.
const openablePaths = new Set<string>();

function normalizeOpenablePath(p: string): string {
  return path.resolve(p).toLowerCase();
}

function allowOpenablePath(p: string): void {
  if (p) openablePaths.add(normalizeOpenablePath(p));
}

function isOpenablePath(p: string): boolean {
  return typeof p === "string" && p.length > 0 && openablePaths.has(normalizeOpenablePath(p));
}

type PrivilegedIpcEvent = { senderFrame: { url: string } | null };

function requireTrustedSender(event: PrivilegedIpcEvent): void {
  try {
    assertTrustedSender(securityPolicy, event.senderFrame?.url ?? null);
  } catch (error) {
    pushBootLog("[ SECURITY ] blocked privileged IPC from untrusted frame");
    if (error instanceof SecurityPolicyError) throw error;
    throw new SecurityPolicyError("UNTRUSTED_SENDER");
  }
}

function hasTrustedSender(event: PrivilegedIpcEvent): boolean {
  try {
    requireTrustedSender(event);
    return true;
  } catch {
    return false;
  }
}

function buildCsp(): string {
  const connectSrc = isDev
    ? `'self' ${BACKEND_ORIGIN} ws://127.0.0.1:1420`
    : `'self' ${BACKEND_ORIGIN}`;
  const scriptSrc = isDev ? "'self' 'unsafe-inline' 'unsafe-eval'" : "'self'";
  return [
    "default-src 'self'",
    `script-src ${scriptSrc}`,
    "style-src 'self' 'unsafe-inline'",
    "font-src 'self'",
    "img-src 'self' data:",
    `connect-src ${connectSrc}`,
    "object-src 'none'",
    "base-uri 'self'",
    "frame-ancestors 'none'",
  ].join("; ");
}

function applyCsp(): void {
  session.defaultSession.webRequest.onHeadersReceived((details, callback) => {
    callback({
      responseHeaders: {
        ...details.responseHeaders,
        "Content-Security-Policy": [buildCsp()],
      },
    });
  });
}

function createWindow(): BrowserWindow {
  const win = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 980,
    minHeight: 640,
    frame: false,
    backgroundColor: "#0a0b0d",
    webPreferences: {
      preload: path.join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  win.webContents.setWindowOpenHandler(({ url }) => {
    openExternalSafely(url);
    return { action: "deny" };
  });

  win.webContents.on("will-navigate", (event, url) => {
    if (!isAllowedNavigation(securityPolicy, url)) event.preventDefault();
  });

  // A crashed/killed renderer must not leave the backend (and, if a search
  // was mid-flight, the Python/Camoufox child processes it spawned) running
  // with no visible window - installer hardening Phase 4 (process
  // lifecycle). Closing the window reuses the exact same window-all-closed
  // -> shutdown() path a normal user close already takes.
  win.webContents.on("render-process-gone", (_event, details) => {
    pushBootLog(`[ SHUTDOWN ] renderer process gone (reason=${details.reason}); closing window`);
    if (!win.isDestroyed()) win.close();
  });

  if (isDev) {
    void win.loadURL(DEV_SERVER_URL);
  } else {
    void win.loadFile(PACKAGED_INDEX_PATH);
  }

  win.on("closed", () => {
    if (mainWindow === win) mainWindow = null;
  });

  return win;
}

function registerIpcHandlers(): void {
  ipcMain.handle("sencia:getApiToken", (event) => {
    requireTrustedSender(event);
    return apiToken;
  });
  ipcMain.handle("sencia:getBootLogs", (event) => {
    requireTrustedSender(event);
    return getBootLogs();
  });

  // The radar exists to find work while the user is doing something else, which
  // means the only place its results can land is outside the window. Clicking
  // the notification opens the posting and raises the app.
  ipcMain.handle(
    "sencia:notifyJob",
    (event, options: { title: string; body: string; url: string }) => {
      requireTrustedSender(event);
      if (!Notification.isSupported()) return false;
      const notification = new Notification({ title: options.title, body: options.body });
      notification.on("click", () => {
        if (mainWindow && !mainWindow.isDestroyed()) {
          if (mainWindow.isMinimized()) mainWindow.restore();
          mainWindow.focus();
        }
        openExternalSafely(options.url);
      });
      notification.show();
      return true;
    },
  );

  ipcMain.handle(
    "sencia:saveBinaryFile",
    async (
      event,
      options: { fileName: string; base64: string; filter: { name: string; extensions: string[] } },
    ) => {
      requireTrustedSender(event);
      if (!mainWindow) return { canceled: true };
      const { canceled, filePath } = await dialog.showSaveDialog(mainWindow, {
        defaultPath: options.fileName,
        filters: [options.filter],
      });
      if (canceled || !filePath) return { canceled: true };
      await fs.writeFile(filePath, Buffer.from(options.base64, "base64"));
      allowOpenablePath(filePath);
      allowOpenablePath(path.dirname(filePath));
      return { path: filePath, canceled: false };
    },
  );

  ipcMain.handle(
    "sencia:saveTextFile",
    async (
      event,
      options: { fileName: string; content: string; filter: { name: string; extensions: string[] } },
    ) => {
      requireTrustedSender(event);
      if (!mainWindow) return { canceled: true };
      const { canceled, filePath } = await dialog.showSaveDialog(mainWindow, {
        defaultPath: options.fileName,
        filters: [options.filter],
      });
      if (canceled || !filePath) return { canceled: true };
      await fs.writeFile(filePath, options.content, "utf8");
      allowOpenablePath(filePath);
      allowOpenablePath(path.dirname(filePath));
      return { path: filePath, canceled: false };
    },
  );

  ipcMain.handle("sencia:openPath", async (event, targetPath: string) => {
    requireTrustedSender(event);
    if (!isOpenablePath(targetPath)) {
      pushBootLog("[ SECURITY ] blocked openPath for a path this app did not produce");
      return;
    }
    await shell.openPath(targetPath);
  });

  ipcMain.handle("sencia:showItemInFolder", (event, targetPath: string) => {
    requireTrustedSender(event);
    if (!isOpenablePath(targetPath)) {
      pushBootLog("[ SECURITY ] blocked showItemInFolder for a path this app did not produce");
      return;
    }
    shell.showItemInFolder(targetPath);
  });

  // Resolves the app data folder path (which contains the Windows username)
  // entirely in the main process, so the renderer never receives or has to
  // display the raw path - installer hardening install-health panel's
  // "Abrir pasta de dados" action.
  ipcMain.handle("sencia:openAppDataFolder", async (event) => {
    requireTrustedSender(event);
    const dir = resolveAppDataDir();
    if (!dir) return;
    await shell.openPath(dir);
  });

  ipcMain.on("sencia:windowControl", (event, action: "minimize" | "maximize" | "close") => {
    if (!hasTrustedSender(event)) return;
    if (!mainWindow) return;
    if (action === "minimize") {
      mainWindow.minimize();
    } else if (action === "maximize") {
      if (mainWindow.isMaximized()) mainWindow.unmaximize();
      else mainWindow.maximize();
    } else if (action === "close") {
      mainWindow.close();
    }
  });
}

function shutdown(): void {
  stopBackend();
}

// A second launched instance would spawn a second Go backend that can never
// bind the fixed 48730 port - one dead window and an orphan-prone process
// tree. Hand the activation to the existing instance instead.
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.focus();
  });

  app.whenReady().then(() => {
    apiToken = crypto.randomBytes(24).toString("hex");
    startBackend(apiToken);
    void waitForHealth(15000);

    applyCsp();
    registerIpcHandlers();
    mainWindow = createWindow();

    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) {
        mainWindow = createWindow();
      }
    });
  });
}

app.on("window-all-closed", () => {
  shutdown();
  app.quit();
});

app.on("before-quit", () => {
  shutdown();
});

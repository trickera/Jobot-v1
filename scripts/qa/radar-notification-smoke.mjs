// Nobody had ever seen a radar notification fire. This run explains why: until
// this branch, it could not.
//
// `notificationThreshold` has been in the Go config, in types.ts, and rendered as
// a number input in Settings since the beginning -- and read by nothing, ever.
// There was no call to Electron's Notification API anywhere in the app. The user
// could set "tell me about anything over 85" and be told about nothing, forever.
// It is not that the notification never fired. It is that there was nothing to
// fire.
//
// So this suite drives the thing that now exists, in the packaged .exe, and does
// not settle for "the queue was drained". It captures the Windows desktop while
// the toast is on screen, because a notification that the code believes it sent
// and the user never sees is the same bug wearing a different coat.
//
// The sweep is a real search against the real boards. Scoring is the offline
// heuristic, so no AI key is needed -- notification does not depend on the AI.
//
// POINT THIS AT THE INSTALLED APP, NOT release/electron/win-unpacked.
//
// Windows only draws a toast for a process whose AppUserModelID matches a Start
// Menu shortcut, and only the NSIS installer creates that shortcut. Run
// win-unpacked straight off the build output and Notification.isSupported()
// returns true, show() returns cleanly, and the toast is silently discarded --
// every assertion below passes and the user sees nothing. That is precisely how
// this was verified the first time, and the screenshot is what caught it. Which
// also means clean-install-smoke.mjs, which drives win-unpacked, can never
// validate a notification.
//
//   %LOCALAPPDATA%\Programs\@senciadesktop\Sencia Job.exe
//
// Prerequisites:
//   npm run release:electron
//   install it:  "release\electron\Sencia Job Setup 0.1.0.exe" /S
//
// Env:
//   SENCIA_APP_EXE   the INSTALLED exe (see above)
//   SENCIA_OUT_DIR   screenshots
//
// Run:
//   SENCIA_APP_EXE="...\Sencia Job.exe" node scripts/qa/radar-notification-smoke.mjs

import { _electron as electron } from "playwright-core";
import { execFileSync } from "node:child_process";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const appExe =
  process.env.SENCIA_APP_EXE ?? path.join(repoRoot, "release/electron/win-unpacked/Sencia Job.exe");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-radar-"));
// A caller-supplied SENCIA_OUT_DIR need not exist yet, and the screen capture
// writes straight into it.
fs.mkdirSync(outDir, { recursive: true });
const API = "http://127.0.0.1:48730";
const SWEEP_TIMEOUT = 6 * 60 * 1000;

if (!fs.existsSync(appExe)) {
  console.error(`No packaged app at ${appExe}. Build it: npm run release:electron`);
  process.exit(1);
}

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
}

// Playwright can see inside the window. It cannot see a Windows toast, which
// lives outside it -- and the toast is the entire deliverable. So: photograph
// the screen.
function captureDesktop(file) {
  const script = `
    Add-Type -AssemblyName System.Windows.Forms, System.Drawing
    $screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
    $bitmap = New-Object System.Drawing.Bitmap $screen.Width, $screen.Height
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $graphics.CopyFromScreen($screen.Location, [System.Drawing.Point]::Empty, $screen.Size)
    $bitmap.Save('${file.replace(/\\/g, "\\\\")}', [System.Drawing.Imaging.ImageFormat]::Png)
    $graphics.Dispose(); $bitmap.Dispose()
  `;
  try {
    execFileSync("powershell", ["-NoProfile", "-STA", "-Command", script], { stdio: "ignore" });
    return fs.existsSync(file);
  } catch {
    return false;
  }
}

const portInUse = await fetch(`${API}/health`)
  .then(() => true)
  .catch(() => false);
if (portInUse) {
  console.error(`Something is already serving ${API}. Close the running Sencia Job first.`);
  process.exit(1);
}

const profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), "sencia-radar-profile-"));
for (const d of ["AppData/Roaming", "AppData/Local", "Temp"]) {
  fs.mkdirSync(path.join(profileRoot, d), { recursive: true });
}

const app = await electron.launch({
  executablePath: appExe,
  env: {
    ...process.env,
    APPDATA: path.join(profileRoot, "AppData/Roaming"),
    LOCALAPPDATA: path.join(profileRoot, "AppData/Local"),
    TEMP: path.join(profileRoot, "Temp"),
    TMP: path.join(profileRoot, "Temp"),
    USERPROFILE: profileRoot,
    SENCIA_DB_PATH: path.join(profileRoot, "sencia.db"),
    // The loop wakes every 15s by default; this run has no patience for that.
    SENCIA_RADAR_TICK_SECONDS: "3",
  },
  timeout: 90_000,
});

let page;
try {
  const packaged = await app.evaluate(({ app }) => app.isPackaged);
  if (!packaged) throw new Error("app.isPackaged is false - this is not the packaged artifact");

  page = await app.firstWindow({ timeout: 90_000 });
  await page.waitForLoadState("domcontentloaded");
  await page.waitForSelector("button", { timeout: 60_000 });
  await page.waitForFunction(
    async () => {
      try {
        return (await fetch("http://127.0.0.1:48730/health")).ok;
      } catch {
        return false;
      }
    },
    { timeout: 90_000 },
  );

  // Whether Windows will even show us a toast. If this is false, everything
  // below is theatre and the run should say so rather than pass.
  const supported = await app.evaluate(({ Notification }) => Notification.isSupported());
  record("Windows will display notifications for this app", supported, `Notification.isSupported() = ${supported}`);

  // Radar on, threshold at 1 so that whatever the boards return crosses it. The
  // threshold logic is unit-tested; what has never been exercised is the path
  // from a crossed threshold to a toast on the screen.
  const configured = await page.evaluate(async () => {
    const token = await window.senciaElectron.getApiToken();
    const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
    const config = await (await fetch("http://127.0.0.1:48730/api/v1/config", { headers })).json();
    const next = {
      ...config,
      form: {
        ...config.form,
        role: "Backend",
        roles: "Backend",
        seniority: "Pleno",
        levels: "Pleno",
        location: "Remoto",
        workMode: "remote",
        remoteCountry: "Brazil",
        keywords: "Go, PostgreSQL, REST, Docker, Linux",
        recentHours: 168,
        maxJobs: 1,
        linkedinPages: 1,
        scoreCut: 0,
        maxDelaySeconds: 1,
        notificationThreshold: 1,
        radarIntervalMinutes: 1,
      },
      toggles: {
        ...config.toggles,
        radarMode: true,
        useLinkedin: true,
        useIndeed: false,
        useGupy: false,
        score: false,
        compatibility: false,
        headless: true,
        remoteOnly: true,
        saveHistory: true,
      },
    };
    const put = await fetch("http://127.0.0.1:48730/api/v1/config", {
      method: "PUT",
      headers,
      body: JSON.stringify(next),
    });
    return put.ok;
  });
  record("radar armed with a threshold of 1", configured, "radarMode=true, notificationThreshold=1");

  // Tap Notification.show() and call straight through. Nothing else is touched:
  // the renderer's own drain loop, the preload bridge, and the real ipcMain
  // handler in main.ts all run exactly as they ship, and the toast really
  // appears. Replacing the handler with a copy of itself would have proved only
  // that the copy works -- which is the mistake this whole session is about.
  await app.evaluate(({ Notification }) => {
    globalThis.__toasts = [];
    const show = Notification.prototype.show;
    Notification.prototype.show = function patchedShow() {
      globalThis.__toasts.push({ title: this.title, body: this.body });
      return show.call(this);
    };
  });

  console.log("\n  waiting for a radar sweep against the real boards (this is a live search)...");
  const sweepStarted = Date.now();
  let toasts = [];
  while (Date.now() - sweepStarted < SWEEP_TIMEOUT) {
    toasts = await app.evaluate(() => globalThis.__toasts ?? []);
    if (toasts.length > 0) break;
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }

  // Photograph the desktop while the toast is still up. Several frames, because
  // a single frame that happens to miss it is indistinguishable from a toast
  // Windows silently refused to draw -- and those are very different bugs.
  const shots = [];
  for (let frame = 0; frame < 4; frame += 1) {
    const file = path.join(outDir, `desktop-${frame}.png`);
    if (captureDesktop(file)) shots.push(file);
    await new Promise((resolve) => setTimeout(resolve, 1200));
  }
  const captured = shots.length > 0;

  const logs = await page.evaluate(async () => {
    const token = await window.senciaElectron.getApiToken();
    const response = await fetch("http://127.0.0.1:48730/api/v1/logs", {
      headers: { Authorization: `Bearer ${token}` },
    });
    return (await response.json()).logs ?? [];
  });
  const radarLines = logs.map((entry) => entry.message).filter((line) => /\[ RADAR \]/.test(line));

  record(
    "a radar sweep ran on its own, with nobody clicking anything",
    radarLines.some((line) => /varredura iniciada/.test(line)),
    radarLines.slice(0, 2).join(" | ") || "no [ RADAR ] line at all",
  );
  record(
    "the sweep queued a job over the threshold",
    radarLines.some((line) => /aguardando notificacao/.test(line)),
    radarLines.find((line) => /aguardando notificacao/.test(line)) ?? "nothing was queued",
  );
  record(
    "the desktop was asked to show a notification",
    toasts.length > 0,
    toasts.length > 0
      ? `${toasts.length} toast(s): ${toasts.map((t) => t.title).join(" / ")}`
      : "sencia:notifyJob was never invoked",
  );
  record(
    "the screen was photographed while the toast was up",
    captured,
    captured ? `${shots.length} frames: ${outDir}` : "capture failed",
  );

  // Drained means drained: the next poll must not announce the same job again.
  const second = await page.evaluate(async () => {
    const token = await window.senciaElectron.getApiToken();
    const response = await fetch("http://127.0.0.1:48730/api/v1/notifications/drain", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    });
    return (await response.json()).jobs ?? [];
  });
  record(
    "a delivered job is not announced a second time",
    second.length === 0,
    `re-drain returned ${second.length} job(s)`,
  );
} catch (error) {
  record("run completed", false, error.message.split("\n")[0]);
} finally {
  await app.close().catch(() => {});
  const failed = steps.filter((step) => !step.ok);
  console.log(`\nScreenshots: ${outDir}`);
  console.log(`=== ${steps.length - failed.length}/${steps.length} passed ===`);
  if (failed.length > 0) process.exit(1);
}

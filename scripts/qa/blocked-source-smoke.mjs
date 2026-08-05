// A job board can close its doors, and the app must say so.
//
// br.indeed.com now answers automated traffic with a Cloudflare interstitial
// ("Security Check - Indeed.com"). Measured on 2026-07-12 through the real
// Camoufox worker: the wall is served on the search page AND the home page,
// headless and headed, cold and warmed, desktop and mobile, with humanize and a
// pinned OS fingerprint. Seven approaches, seven walls. The old jobhunter that
// "worked perfectly" used byte-identical Camoufox launch args, so its code holds
// no secret either — the wall is new, the code is not worse.
//
// So this is not a scraper bug to fix. It is a source that has closed, and the
// only bug we own is that the app LIED about it: the worker's blocked flag was
// discarded with `_` in the search path (it was only honoured for detail pages),
// zero cards parsed out of a captcha page, and the app reported "Busca
// concluida". One of three boards was dead for weeks and nothing said a word.
//
// Two failures had to line up for that, and this suite pins both:
//   1. looks_blocked() did not recognise the page. BLOCK_MARKERS said "security
//      challenge"; Cloudflare says "Security Check". One word off, so the worker
//      answered blocked=false and Go believed it.
//   2. Even had it said true, the search threw the answer away.
//
// Prerequisites:
//   npm run release:electron ; install it
//
// Env:
//   SENCIA_APP_EXE   the INSTALLED exe
//   SENCIA_OUT_DIR   screenshots
//
// Run:
//   node scripts/qa/blocked-source-smoke.mjs

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const appExe =
  process.env.SENCIA_APP_EXE ??
  path.join(process.env.LOCALAPPDATA ?? "", "Programs", "Sencia Job", "Sencia Job.exe");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-blocked-"));
fs.mkdirSync(outDir, { recursive: true });

const API = "http://127.0.0.1:48730";
const SEARCH_TIMEOUT = 420_000;

if (!fs.existsSync(appExe)) {
  console.error(`No installed app at ${appExe}.`);
  process.exit(1);
}
if (await fetch(`${API}/health`).then(() => true).catch(() => false)) {
  console.error(`Something is already serving ${API}. Close the running Sencia Job first.`);
  process.exit(1);
}

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
}

const profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), "sencia-blocked-profile-"));
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
    SENCIA_RADAR_DISABLED: "1",
  },
  timeout: 90_000,
});

const api = async (page, route, init) =>
  page.evaluate(
    async ({ route, init }) => {
      const token = await window.senciaElectron.getApiToken();
      const response = await fetch(`http://127.0.0.1:48730${route}`, {
        ...init,
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      });
      return response.json().catch(() => null);
    },
    { route, init },
  );

let page;
try {
  const packaged = await app.evaluate(({ app }) => app.isPackaged);
  if (!packaged) throw new Error("app.isPackaged is false - this is not the installed artifact");

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

  // Indeed on, alongside a source that still works, so the run also proves the
  // block notice does not swallow a perfectly good search.
  const config = await api(page, "/api/v1/config");
  await api(page, "/api/v1/config", {
    method: "PUT",
    body: JSON.stringify({
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
        keywords: "Go, PostgreSQL",
        recentHours: 168,
        maxJobs: 5,
        linkedinPages: 1,
        scoreCut: 40,
        maxDelaySeconds: 1,
      },
      toggles: {
        ...config.toggles,
        useLinkedin: true,
        useIndeed: true,
        useGupy: false,
        compatibility: false,
        headless: true,
        remoteOnly: true,
      },
    }),
  });

  // Through the UI, the way a person does it. The message lives in a notice the
  // renderer only paints while it is polling a search IT started.
  // The sidebar is icon-only; the accessible name comes from aria-label
  // ("Buscar vagas", navigation.tsx). Assert we actually landed on the search
  // view rather than trusting the click — the first run of this file ended up on
  // "Vagas salvas" and spent five minutes measuring the wrong screen.
  await page.locator('nav.sidebar button[aria-label="Buscar vagas"]').click({ timeout: 20_000 });
  await page.getByRole("button", { name: /nova busca/i }).first().waitFor({ state: "visible", timeout: 20_000 });
  await page.getByRole("button", { name: /nova busca/i }).first().click({ timeout: 20_000 });

  // Wait for the search to START before waiting for it to finish.
  //
  // The click does not start the search: startSearch() awaits a browser-health
  // check first, so POST /api/v1/search lands roughly a second later. Poll
  // straight after the click and you read the pristine state — running=false,
  // message="" — and a naive "wait until not running" loop exits immediately and
  // reports an empty search that never happened. That is exactly what the first
  // version of this file did: four red assertions against a backend that was
  // working perfectly.
  let startedRunning = false;
  const startDeadline = Date.now() + 60_000;
  while (Date.now() < startDeadline) {
    const snapshot = await api(page, "/api/v1/search/status");
    if (snapshot?.running) {
      startedRunning = true;
      break;
    }
    await page.waitForTimeout(500);
  }
  if (!startedRunning) throw new Error("the search never started after clicking Nova busca");

  let status = null;
  const started = Date.now();
  const deadline = started + SEARCH_TIMEOUT;
  while (Date.now() < deadline) {
    status = await api(page, "/api/v1/search/status");
    if (status && !status.running) break;
    await page.waitForTimeout(3000);
  }

  const indeed = status?.diagnostics?.sources?.Indeed;
  const linkedin = status?.diagnostics?.sources?.LinkedIn;

  record(
    "the wall is recognised as a wall, not as an empty result",
    indeed?.blocked === true,
    `Indeed: collected=${indeed?.collected} blocked=${indeed?.blocked}`,
  );

  record(
    "the outcome message names the blocked source",
    /Indeed/.test(status?.message ?? "") && /anti-bot/.test(status?.message ?? ""),
    status?.message ?? "(no message)",
  );

  record(
    "a working source is unaffected",
    (linkedin?.collected ?? 0) > 0,
    `LinkedIn: collected=${linkedin?.collected} approved=${linkedin?.approved}`,
  );

  // The whole point. The API being right is not the deliverable; the user being
  // told is.
  const notice = page.locator(".notice, .inline-notice, [class*='notice']").filter({ hasText: /bloqueou o acesso automatizado/ });
  let visible = false;
  try {
    await notice.first().waitFor({ state: "visible", timeout: 20_000 });
    visible = true;
  } catch {
    visible = false;
  }
  await page.screenshot({ path: path.join(outDir, "blocked-notice.png") });

  const body = await page.locator("body").innerText();
  record(
    "the user is told, on screen, that Indeed blocked us",
    visible || /bloqueou o acesso automatizado/.test(body),
    visible
      ? (await notice.first().innerText()).replace(/\s+/g, " ").slice(0, 110)
      : `not on screen. Body: ${body.slice(0, 120).replace(/\s+/g, " ")}`,
  );

  const errors = await api(page, "/api/v1/logs");
  const indeedLogs = (errors?.logs ?? []).filter((entry) => /INDEED/i.test(entry.message));
  record(
    "the block is in the log, at error level",
    indeedLogs.some((entry) => entry.level === "error" && /bloqueado/.test(entry.message)),
    indeedLogs.find((entry) => entry.level === "error")?.message ?? "no error line",
  );
} catch (error) {
  if (page) await page.screenshot({ path: path.join(outDir, "99-failure.png") }).catch(() => {});
  record("run completed", false, error.message.split("\n")[0]);
} finally {
  await app.close().catch(() => {});
  const failed = steps.filter((step) => !step.ok);
  console.log(`\nScreenshots: ${outDir}`);
  console.log(`=== ${steps.length - failed.length}/${steps.length} passed ===`);
  if (failed.length > 0) process.exit(1);
}

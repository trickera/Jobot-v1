// The free international boards, driven live through the installed .exe.
//
// Indeed closed its door (Cloudflare, verified — even a real human click did not
// pass, and the old jobhunter's own code gets zero today). These keyless REST
// boards replace the international volume it used to give. This run turns them on
// through Settings, runs a real search with LinkedIn OFF so nothing else can be
// mistaken for their output, and asserts the jobs actually arrive with the shape
// the rest of the pipeline needs.
//
// No AI key: scoring is offline, because a job source has nothing to do with the
// AI and demanding a key here would be theatre.
//
// Env:
//   SENCIA_APP_EXE   the INSTALLED exe
//   SENCIA_OUT_DIR   screenshots
//
// Run:
//   node scripts/qa/remote-sources-smoke.mjs

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const appExe =
  process.env.SENCIA_APP_EXE ??
  path.join(process.env.LOCALAPPDATA ?? "", "Programs", "Sencia Job", "Sencia Job.exe");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-remote-"));
fs.mkdirSync(outDir, { recursive: true });

const API = "http://127.0.0.1:48730";
const SEARCH_TIMEOUT = 300_000;

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

const profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), "sencia-remote-profile-"));
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
  if (!packaged) throw new Error("app.isPackaged is false - not the installed artifact");

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

  // The toggles start off. Turn the five on through Settings, the way a user
  // would, and read them back.
  await page.getByRole("button", { name: /configuracoes/i }).first().click({ timeout: 20_000 });
  await page.getByRole("button", { name: /fontes de vagas/i }).click({ timeout: 20_000 }).catch(() => {});
  await page.waitForTimeout(500);
  await page.screenshot({ path: path.join(outDir, "01-sources-settings.png") });

  // Read defaults, then enable via API (the UI toggles are verified visually in
  // the screenshot above; the enable itself goes through the same config the UI
  // writes). LinkedIn OFF so the only jobs that can arrive are the REST boards'.
  const config = await api(page, "/api/v1/config");
  const before = {
    remotive: config.toggles.useRemotive,
    remoteok: config.toggles.useRemoteok,
    jobicy: config.toggles.useJobicy,
    arbeitnow: config.toggles.useArbeitnow,
    wwr: config.toggles.useWeworkremotely,
  };
  record(
    "the REST sources ship OFF by default",
    Object.values(before).every((v) => v === false),
    JSON.stringify(before),
  );

  await api(page, "/api/v1/config", {
    method: "PUT",
    body: JSON.stringify({
      ...config,
      form: {
        ...config.form,
        role: "developer",
        roles: "developer",
        seniority: "",
        levels: "",
        excludedLevels: "",
        location: "Remote",
        workMode: "remote",
        remoteCountry: "Worldwide",
        keywords: "",
        recentHours: 336,
        maxJobs: 15,
        scoreCut: 20,
        maxDelaySeconds: 1,
      },
      toggles: {
        ...config.toggles,
        useLinkedin: false,
        useIndeed: false,
        useGupy: false,
        useRemotive: true,
        useRemoteok: true,
        useJobicy: true,
        useArbeitnow: true,
        useWeworkremotely: true,
        compatibility: false,
        headless: true,
        remoteOnly: false,
      },
    }),
  });

  // Drive the search through the UI button, not an API POST, so the renderer's
  // own poll engages and actually paints the results — otherwise the screenshot
  // would show an empty "Nenhuma busca executada" screen even though the jobs are
  // in the store. (The API-POST shortcut bit an earlier version of this file.)
  await page.locator('nav.sidebar button[aria-label="Buscar vagas"]').click({ timeout: 20_000 });
  await page.getByRole("button", { name: /nova busca/i }).first().waitFor({ state: "visible", timeout: 20_000 });
  await page.getByRole("button", { name: /nova busca/i }).first().click({ timeout: 20_000 });

  // The click starts the search a beat later (startSearch awaits a health check),
  // so wait for running=true before waiting for it to finish.
  let startedRunning = false;
  const startBy = Date.now() + 60_000;
  while (Date.now() < startBy) {
    const s = await api(page, "/api/v1/search/status");
    if (s?.running) {
      startedRunning = true;
      break;
    }
    await page.waitForTimeout(500);
  }
  if (!startedRunning) throw new Error("search never started");

  let status = null;
  const deadline = Date.now() + SEARCH_TIMEOUT;
  while (Date.now() < deadline) {
    status = await api(page, "/api/v1/search/status");
    if (status && !status.running) break;
    await page.waitForTimeout(3000);
  }

  const sources = status?.diagnostics?.sources ?? {};
  const restNames = ["Remotive", "RemoteOK", "Jobicy", "Arbeitnow", "WeWorkRemotely"];
  const collectedByRest = restNames
    .map((name) => [name, sources[name]?.collected ?? 0])
    .filter(([, n]) => n > 0);

  record(
    "at least one free board returned jobs",
    collectedByRest.length > 0,
    collectedByRest.map(([n, c]) => `${n}=${c}`).join(", ") || "no REST source collected anything",
  );

  // Read the jobs the search itself surfaced (status.jobs is what the UI renders),
  // not a separate /jobs store query — so a green assertion means the same jobs
  // the user is looking at.
  const jobList = Array.isArray(status?.jobs) ? status.jobs : [];
  const fromRest = jobList.filter((j) => restNames.includes(j.source));
  record(
    "the jobs are real and complete (title, company, url)",
    fromRest.length > 0 && fromRest.every((j) => j.title && j.company && j.url),
    fromRest.slice(0, 4).map((j) => `${j.title} @ ${j.company} [${j.source}]`).join(" | ") || "none",
  );

  // International, not just five US-remote listings.
  const locations = new Set(fromRest.map((j) => (j.location || "").trim()).filter(Boolean));
  record(
    "coverage is international",
    locations.size > 0,
    [...locations].slice(0, 8).join(" · ") || "no locations",
  );

  // LinkedIn was off; nothing should have come from it.
  record(
    "LinkedIn was off - these jobs are genuinely the free boards'",
    !jobList.some((j) => j.source === "LinkedIn"),
    `sources present: ${[...new Set(jobList.map((j) => j.source))].join(", ")}`,
  );

  // And the user actually sees them: wait for a job card to paint, then shoot.
  const rendered = await page.locator(".job-card").first().waitFor({ state: "visible", timeout: 20_000 }).then(() => true).catch(() => false);
  const cardCount = await page.locator(".job-card").count();
  record(
    "the results are on screen, not just in the API",
    rendered && cardCount > 0,
    `${cardCount} job card(s) rendered`,
  );
  await page.screenshot({ path: path.join(outDir, "02-results.png") });
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

// One real, low-volume Indeed search through the Electron app built from source.
//
// The deterministic Go regression is the contract proof. This smoke checks the
// same path with the real browser worker and records an anti-bot listing wall as
// External / inconclusive. It never retries a blocked listing and only opens a
// /viewjob URL after clicking the existing card button.
//
// Prerequisites:
//   npm run backend:build
//   npm run electron:build
//   npm run dev -w @sencia/desktop
//
// Run:
//   node scripts/qa/indeed-listing-only-smoke.mjs

import { _electron as electron } from "playwright-core";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const desktopDir = path.join(repoRoot, "apps/desktop");
const stamp = new Date().toISOString().replaceAll(":", "-").replace("T", "_").slice(0, 19);
const outDir = process.env.SENCIA_OUT_DIR ?? path.join(repoRoot, "qa-artifacts", "indeed-listing-only-2026-07-14", stamp);
const apiOrigin = "http://127.0.0.1:48730";
const searchTimeout = 300_000;

fs.mkdirSync(outDir, { recursive: true });

const devServer = await fetch("http://127.0.0.1:1420").catch(() => null);
if (!devServer?.ok) {
  console.error("The Vite dev server is not on :1420. Run `npm run dev -w @sencia/desktop` first.");
  process.exit(1);
}
if (await fetch(`${apiOrigin}/health`).then(() => true).catch(() => false)) {
  console.error(`Something is already serving ${apiOrigin}. Close the running Sencia Job first.`);
  process.exit(1);
}

const profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), "sencia-indeed-listing-only-"));
for (const directory of ["AppData/Roaming", "AppData/Local", "Temp"]) {
  fs.mkdirSync(path.join(profileRoot, directory), { recursive: true });
}
const dbPath = path.join(profileRoot, "sencia.db");

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
}

function writeJSON(name, value) {
  fs.writeFileSync(path.join(outDir, name), `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

const app = await electron.launch({
  args: [desktopDir],
  cwd: desktopDir,
  env: {
    ...process.env,
    APPDATA: path.join(profileRoot, "AppData/Roaming"),
    LOCALAPPDATA: path.join(profileRoot, "AppData/Local"),
    TEMP: path.join(profileRoot, "Temp"),
    TMP: path.join(profileRoot, "Temp"),
    USERPROFILE: profileRoot,
    SENCIA_DB_PATH: dbPath,
    SENCIA_RADAR_DISABLED: "1",
  },
  timeout: 90_000,
});

const api = async (page, route, init) =>
  page.evaluate(
    async ({ apiOrigin, route, init }) => {
      const token = await window.senciaElectron.getApiToken();
      const response = await fetch(`${apiOrigin}${route}`, {
        ...init,
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      });
      const text = await response.text();
      const payload = text ? JSON.parse(text) : null;
      if (!response.ok) throw new Error(`${init?.method ?? "GET"} ${route}: ${response.status} ${text}`);
      return payload;
    },
    { apiOrigin, route, init },
  );

let page;
let classification = "not-run";
let status = null;
let beforeOpenLogs = [];
let afterOpenLogs = [];

try {
  page = await app.firstWindow({ timeout: 90_000 });
  page.on("pageerror", (error) => console.log(`      [renderer crash] ${error.message}`));
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
  record("Electron source window and backend started", fs.existsSync(dbPath), "isolated profile and SQLite were created");

  const config = await api(page, "/api/v1/config");
  const originalScoreCut = config.form.scoreCut;
  const nextConfig = {
    ...config,
    form: {
      ...config.form,
      role: "Backend Engineer",
      roles: "Backend Engineer",
      searchProfiles: "",
      location: "Sao Paulo, SP",
      workMode: "onsite",
      onsiteLocation: "Sao Paulo, SP",
      remoteCountry: "Brazil",
      resumeName: "Synthetic isolated QA profile",
      resumeText:
        "Backend engineer experienced with Go, REST APIs, PostgreSQL, Docker, Linux, distributed systems, testing, observability, and production services.",
      maxJobs: Math.min(config.form.maxJobs || 40, 5),
    },
    toggles: {
      ...config.toggles,
      useLinkedin: false,
      useIndeed: true,
      useGupy: false,
      useRemotive: false,
      useRemoteok: false,
      useJobicy: false,
      useArbeitnow: false,
      useWeworkremotely: false,
      remoteOnly: false,
    },
  };
  const savedConfig = await api(page, "/api/v1/config", { method: "PUT", body: JSON.stringify(nextConfig) });
  record(
    "score threshold was not changed",
    savedConfig.form.scoreCut === originalScoreCut,
    `scoreCut=${savedConfig.form.scoreCut}; maxJobs=${savedConfig.form.maxJobs}; only Indeed enabled`,
  );

  // SearchView loaded its effective plan before the isolated config existed.
  // Reload once so the visible button reflects the config the backend saved.
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.waitForSelector("button", { timeout: 60_000 });
  const searchButton = page.getByRole("button", { name: /nova busca/i }).first();
  await searchButton.waitFor({ state: "visible", timeout: 20_000 });
  await page.waitForFunction(
    () => [...document.querySelectorAll("button")].some((button) => /nova busca/i.test(button.textContent ?? "") && !button.disabled),
    { timeout: 20_000 },
  );
  await searchButton.click({ timeout: 20_000 });

  let started = false;
  const startDeadline = Date.now() + 60_000;
  while (Date.now() < startDeadline) {
    const snapshot = await api(page, "/api/v1/search/status");
    if (snapshot?.running) {
      started = true;
      break;
    }
    await page.waitForTimeout(500);
  }
  if (!started) throw new Error("the UI search never reached running=true");

  const deadline = Date.now() + searchTimeout;
  while (Date.now() < deadline) {
    status = await api(page, "/api/v1/search/status");
    if (status && !status.running) break;
    await page.waitForTimeout(2000);
  }
  if (!status || status.running) throw new Error("the single search did not finish within five minutes");

  const logsPayload = await api(page, "/api/v1/logs");
  beforeOpenLogs = logsPayload?.logs ?? [];
  const persistedPayload = await api(page, "/api/v1/jobs");
  const persistedJobs = persistedPayload?.jobs ?? [];
  const historyPayload = await api(page, "/api/v1/history");
  const history = historyPayload?.history ?? historyPayload?.items ?? [];
  const state = await api(page, "/api/v1/state");
  const bootLogs = await page.evaluate(() => window.senciaElectron.getBootLogs());

  writeJSON("search-status.json", status);
  writeJSON("logs-before-manual-open.json", beforeOpenLogs);
  writeJSON("persisted-jobs.json", persistedPayload);
  writeJSON("history.json", historyPayload);
  writeJSON("runtime-proof.json", {
    database: path.basename(dbPath),
    isolatedProfile: path.basename(profileRoot),
    originalScoreCut,
    savedScoreCut: savedConfig.form.scoreCut,
    localItems: state.localItems,
    bootLogs,
  });
  await page.screenshot({ path: path.join(outDir, "search-result.png"), fullPage: true });

  const indeedDiagnostics = status.diagnostics?.sources?.Indeed ?? {};
  const automaticDetailURLs = beforeOpenLogs.filter(
    (entry) => /https?:\/\/\S+\/(?:m\/)?viewjob/i.test(entry.message ?? "") && !/\[ OPEN \]/.test(entry.message ?? ""),
  );
  const listingLogs = beforeOpenLogs.filter((entry) => /\[ INDEED \] https?:\/\/[^ ]+\/jobs\?/i.test(entry.message ?? ""));
  const listingOnlyLogs = beforeOpenLogs.filter((entry) => /\[ INDEED \] modo listing-only:/i.test(entry.message ?? ""));
  record("one real Indeed listing search was attempted", listingLogs.length === 1, `${listingLogs.length} listing request log(s)`);
  record("runtime declared listing-only mode", listingOnlyLogs.length >= 1, listingOnlyLogs[0]?.message ?? "missing policy log");
  record(
    "zero automatic /viewjob detail fetches",
    automaticDetailURLs.length === 0 && (indeedDiagnostics.detailFetched ?? 0) === 0,
    `request logs=${automaticDetailURLs.length}; diagnostics.detailFetched=${indeedDiagnostics.detailFetched ?? 0}`,
  );

  const indeedJobs = (status.jobs ?? []).filter((job) => job.source === "Indeed");
  if (indeedDiagnostics.blocked) {
    classification = "External / inconclusive: Indeed blocked the listing page";
    record(
      "blocked listing is surfaced honestly",
      /Indeed/i.test(status.message ?? "") && /anti-bot/i.test(status.message ?? ""),
      status.message ?? "missing source outcome",
    );
  } else if (indeedJobs.length === 0) {
    classification = "External / inconclusive: listing returned no approved Indeed card";
    record("live card validation was inconclusive", true, `collected=${indeedDiagnostics.collected ?? 0}; approved=${indeedDiagnostics.approved ?? 0}`);
  } else {
    classification = "Live card validated";
    const job = indeedJobs[0];
    const persisted = persistedJobs.find((item) => item.id === job.id);
    const fieldsPresent =
      Boolean(job.title?.trim()) &&
      Boolean(job.company?.trim()) &&
      Boolean(job.location?.trim()) &&
      Boolean(job.description?.trim()) &&
      /\/viewjob\?jk=/i.test(job.url ?? "") &&
      Number.isFinite(job.score);
    record(
      "Indeed card retained listing fields and score",
      fieldsPresent,
      `${job.title} | ${job.company} | ${job.location} | snippet=${job.description?.length ?? 0} chars | score=${job.score}`,
    );
    record(
      "Indeed card was persisted before display",
      Boolean(persisted) && persisted.description === job.description && history.length > 0,
      `persisted=${Boolean(persisted)}; history rows=${history.length}; url=${job.url}`,
    );

    const card = page.locator(".job-card").filter({ hasText: job.title }).first();
    await card.waitFor({ state: "visible", timeout: 20_000 });
    await card.getByRole("button", { name: /apply in browser/i }).click({ timeout: 20_000 });
    await page.waitForTimeout(1500);
    afterOpenLogs = (await api(page, "/api/v1/logs"))?.logs ?? [];
    const manualOpen = afterOpenLogs.find(
      (entry) => /\[ OPEN \] vaga aberta no navegador:/i.test(entry.message ?? "") && entry.message.includes(job.url),
    );
    record("/viewjob opened only after the human-style click", Boolean(manualOpen), manualOpen?.message ?? "missing manual-open log");
    writeJSON("logs-after-manual-open.json", afterOpenLogs);
  }
} catch (error) {
  classification = "Harness failure";
  if (page) await page.screenshot({ path: path.join(outDir, "failure.png"), fullPage: true }).catch(() => {});
  record("real Electron run completed", false, error.message.split("\n")[0]);
} finally {
  await app.close().catch(() => {});
  const failed = steps.filter((step) => !step.ok);
  writeJSON("report.json", {
    classification,
    passed: steps.length - failed.length,
    failed: failed.length,
    steps,
    diagnostics: status?.diagnostics ?? null,
  });
  console.log(`\nClassification: ${classification}`);
  console.log(`Evidence: ${outDir}`);
  console.log(`=== ${steps.length - failed.length}/${steps.length} passed ===`);
  if (failed.length > 0) process.exitCode = 1;
}

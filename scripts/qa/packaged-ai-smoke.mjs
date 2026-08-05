// The run that closes the hole yesterday's bug came through.
//
// There were two suites, and between them they covered everything except the
// one thing that matters:
//
//   clean-install-smoke.mjs   the packaged .exe, isolated profile   NO AI key
//   fresh-user-smoke.mjs      a real AI key, real provider calls    RUN FROM SOURCE
//
// Neither crosses the columns. So the artifact a user actually installs had
// never, on any machine, called an AI provider. That is not a gap in coverage,
// it is the precise shape of the bug that shipped: the default model answered
// for an existing account and returned 404 to every key created after Google retired
// it, and no test failed, because no test ever asked the shipped binary to ask
// the provider anything.
//
// main.ts branches on `isDev = !app.isPackaged`. The packaged build loads the
// renderer from app.asar over file://, under a stricter CSP (script-src 'self',
// no unsafe-eval), and resolves the Go backend out of resourcesPath instead of
// the repo. Every one of those is a different code path, and until this file
// none of them had ever been run with the AI turned on.
//
// So this is fresh-user-smoke's flow, driven against the .exe. Empty database,
// factory defaults, no model override, key typed into Settings by hand. It
// asserts app.isPackaged, because if this ever silently falls back to the dev
// build it stops proving the only thing it exists to prove.
//
// Prerequisites:
//   npm run release:electron        (or: backend:build; build; electron:build; desktop:package)
//
// Env:
//   SENCIA_FRESH_API_KEY   required -- a REAL provider key, typed into Settings.
//   SENCIA_APP_EXE         optional -- defaults to release/electron/win-unpacked
//   SENCIA_OUT_DIR         screenshots
//
// Run:
//   node --env-file=.env scripts/qa/packaged-ai-smoke.mjs

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const appExe =
  process.env.SENCIA_APP_EXE ?? path.join(repoRoot, "release/electron/win-unpacked/Sencia Job.exe");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-packaged-ai-"));

const AI_TIMEOUT = 240_000;
const API = "http://127.0.0.1:48730";

const apiKey = process.env.SENCIA_FRESH_API_KEY;
if (!apiKey) {
  console.error("SENCIA_FRESH_API_KEY is required: this run types a real key into the packaged app.");
  process.exit(1);
}
if (!fs.existsSync(appExe)) {
  console.error(`No packaged app at ${appExe}. Build it: npm run release:electron`);
  process.exit(1);
}

const resumeFile = path.join(repoRoot, "scripts/qa/fixtures/personas/1-software-backend.pdf");
if (!fs.existsSync(resumeFile)) {
  console.error(`Missing fixture: ${resumeFile}`);
  process.exit(1);
}

// The backend binds a fixed port. If another copy of the app is open, this run
// would quietly talk to that backend and pass while proving nothing.
const portInUse = await fetch(`${API}/health`)
  .then(() => true)
  .catch(() => false);
if (portInUse) {
  console.error(`Something is already serving ${API}. Close the running Sencia Job before this run:`);
  console.error("it would otherwise answer for the packaged app and this suite would test the wrong process.");
  process.exit(1);
}

const JOB_DESCRIPTION = `Senior Backend Engineer - Acme Payments (Remote)
We need someone to build reliable payment and account APIs.
Requirements: Go, PostgreSQL, REST, distributed systems, Docker, and observability.
Experience improving latency and operating production services is preferred.`;

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
}

async function clickWhenReady(page, name) {
  const button = page.getByRole("button", { name }).first();
  await button.waitFor({ state: "visible", timeout: AI_TIMEOUT });
  await button.click({ timeout: AI_TIMEOUT });
}

async function waitUntilIdle(page, name) {
  await page.getByRole("button", { name }).first().waitFor({ state: "visible", timeout: AI_TIMEOUT });
  await page
    .getByRole("button", { name })
    .first()
    .and(page.locator("button:not([disabled])"))
    .waitFor({ timeout: AI_TIMEOUT });
}

async function apiGet(page, route) {
  return page.evaluate(async (route) => {
    const token = await window.senciaElectron.getApiToken();
    const response = await fetch(`http://127.0.0.1:48730${route}`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    return response.json();
  }, route);
}

// A Windows profile this app has never seen: its own APPDATA, its own TEMP, and
// a database path that does not exist yet.
const profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), "sencia-packaged-profile-"));
const appData = path.join(profileRoot, "AppData", "Roaming");
const localAppData = path.join(profileRoot, "AppData", "Local");
const tempDir = path.join(profileRoot, "Temp");
for (const dir of [appData, localAppData, tempDir]) fs.mkdirSync(dir, { recursive: true });
const dbPath = path.join(profileRoot, "sencia.db");

const app = await electron.launch({
  executablePath: appExe,
  env: {
    ...process.env,
    APPDATA: appData,
    LOCALAPPDATA: localAppData,
    TEMP: tempDir,
    TMP: tempDir,
    USERPROFILE: profileRoot,
    SENCIA_DB_PATH: dbPath,
  },
  timeout: 90_000,
});

let page;
try {
  // Before anything else. If this is false we are driving the dev build, and
  // every PASS below would be a lie about the artifact users install.
  const packaged = await app.evaluate(({ app }) => app.isPackaged);
  if (!packaged) throw new Error("app.isPackaged is false - this is not the packaged artifact");

  page = await app.firstWindow({ timeout: 90_000 });
  page.on("pageerror", (error) => console.log(`      [renderer crash] ${error.message}`));

  const shot = async (name) => {
    const file = path.join(outDir, `${name}.png`);
    await page.screenshot({ path: file });
    return file;
  };

  await page.waitForLoadState("domcontentloaded");
  await page.waitForSelector("button", { timeout: 60_000 });

  // Loaded from inside the asar over file://, under the packaged CSP -- not from
  // a Vite dev server. This is the assertion that the renderer under test is the
  // shipped one.
  const url = page.url();
  record(
    "the shipped renderer loaded out of the asar, not a dev server",
    url.includes("app.asar") && url.startsWith("file://"),
    url,
  );

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
  record(
    "the packaged backend started from resourcesPath and migrated a new database",
    fs.existsSync(dbPath),
    dbPath,
  );

  // Read the default back out of the app rather than from a constant in the
  // source. This is the exact assertion that would have caught the 404.
  const config = await apiGet(page, "/api/v1/config");
  const model = config.form?.model ?? "";
  const provider = config.form?.provider ?? "";
  record(
    "the model the .exe ships with is not one Google has retired",
    /flash/.test(model) && !/2\.5-flash/.test(model),
    `${provider} / ${model} - apiKeySet=${config.apiKeySet}`,
  );

  // --- onboarding, clicked ---------------------------------------------------

  await page.getByRole("button", { name: /configuracoes/i }).first().click({ timeout: 20_000 });
  await page.getByRole("button", { name: /^salvar$/i }).waitFor({ timeout: 20_000 });

  const keyField = page.locator('input[placeholder*="Cole a chave"]').first();
  await keyField.waitFor({ timeout: 20_000 });
  await keyField.fill(apiKey);
  await page.getByRole("button", { name: /permitir processamento seguro por ia/i }).click({ timeout: 20_000 });

  await page.getByRole("button", { name: /perfil e curriculo/i }).click({ timeout: 20_000 });
  const roleField = page.getByLabel(/cargo alvo/i).first();
  await roleField.waitFor({ timeout: 20_000 });
  await roleField.fill("Backend Engineer");

  await page.getByRole("button", { name: /^salvar$/i }).click({ timeout: 20_000 });
  await page.waitForTimeout(2500);

  const saved = await apiGet(page, "/api/v1/config");
  record(
    "a key typed into the packaged app's Settings was accepted and stored",
    Boolean(saved.apiKeySet) && Boolean(saved.form?.role) && saved.form?.aiDataConsent === true,
    `apiKeySet=${saved.apiKeySet} consent=${saved.form?.aiDataConsent} role=${saved.form?.role} model=${saved.form?.model}`,
  );
  await shot("01-configured");

  // --- the call that had never been made -------------------------------------

  await page.getByRole("button", { name: /^curr|resume/i }).first().click({ timeout: 30_000 });
  await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });

  await page.locator("input.resume-file-input").setInputFiles(resumeFile);
  await page.getByRole("heading", { name: /ats diagnosis/i }).waitFor({ timeout: 60_000 });

  await clickWhenReady(page, /^parse/i);
  await page.getByRole("heading", { name: /target job/i }).waitFor({ timeout: AI_TIMEOUT });
  await waitUntilIdle(page, /analyze job/i);
  const parsedName = await page.locator(".resume-parsed-name").first().innerText().catch(() => "");
  record(
    "the installed .exe reached a real AI provider and got a resume back",
    Boolean(parsedName),
    parsedName || "(no name rendered)",
  );
  await shot("02-parsed");

  await page.getByRole("textbox", { name: /job description/i }).fill(JOB_DESCRIPTION);
  await clickWhenReady(page, /analyze job/i);
  await waitUntilIdle(page, /find gaps/i);
  await clickWhenReady(page, /find gaps/i);
  await waitUntilIdle(page, /^optimize resume/i);
  await clickWhenReady(page, /^optimize resume/i);
  await clickWhenReady(page, /generate tailored resume/i);
  await waitUntilIdle(page, /compare scores/i);
  await clickWhenReady(page, /compare scores/i);
  await page.waitForTimeout(3000);
  record("analyze, gap, optimize and score all ran on the shipped default", true, await shot("03-tailored"));

  // The app is designed to keep working when the AI is dead -- it falls back to
  // the offline heuristic. That design is why "it returned something" proves
  // nothing here. Only the log proves the provider actually answered.
  const logs = await apiGet(page, "/api/v1/logs");
  const llm = (logs.logs ?? []).map((entry) => entry.message).filter((line) => /\[ LLM \]/.test(line));
  const answered = llm.some((line) => /-> (ok|cached)/i.test(line));
  const degraded = llm.some(
    (line) => /heur[ií]stica offline|quota_exhausted|model_not_found|invalid_response|404/i.test(line),
  );
  record(
    "the provider answered - no silent fallback to the offline heuristic",
    answered && !degraded,
    llm.slice(-3).join(" | ") || "no [ LLM ] line at all",
  );

  // --- and the other half of the app ------------------------------------------
  //
  // The Resume Studio is not the only thing that calls a provider. Job scoring is
  // a separate purpose (job_score) down a separate path, and the first version of
  // this file never ran a search at all -- which meant AI job scoring on the
  // installed .exe was still, quietly, the untested square in the matrix this
  // file exists to fill in. Note the toggle: AI scoring is gated on
  // `compatibility` ("Analise por IA"), not on `score`, which the Go side never
  // reads at all.
  const searchStarted = await page.evaluate(async () => {
    const token = await window.senciaElectron.getApiToken();
    const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
    const config = await (await fetch("http://127.0.0.1:48730/api/v1/config", { headers })).json();
    await fetch("http://127.0.0.1:48730/api/v1/config", {
      method: "PUT",
      headers,
      body: JSON.stringify({
        ...config,
        form: {
          ...config.form,
          roles: "Backend",
          levels: "Pleno",
          location: "Remoto",
          workMode: "remote",
          remoteCountry: "Brazil",
          keywords: "Go, PostgreSQL, REST, Docker, Linux",
          recentHours: 168,
          maxJobs: 5,
          linkedinPages: 1,
          scoreCut: 40,
          maxDelaySeconds: 1,
        },
        toggles: {
          ...config.toggles,
          useLinkedin: true,
          useIndeed: false,
          useGupy: false,
          compatibility: true,
          headless: true,
          remoteOnly: true,
        },
      }),
    });
    return (await fetch("http://127.0.0.1:48730/api/v1/search", { method: "POST", headers, body: "{}" })).status;
  });

  let search = null;
  const searchDeadline = Date.now() + 420_000;
  while (Date.now() < searchDeadline) {
    search = await apiGet(page, "/api/v1/search/status");
    if (search && !search.running) break;
    await page.waitForTimeout(4000);
  }
  const diagnostics = search?.diagnostics ?? {};
  record(
    "the installed .exe searched the real boards",
    (diagnostics.collected ?? 0) > 0,
    `start=${searchStarted} collected=${diagnostics.collected} evaluated=${diagnostics.evaluated} approved=${diagnostics.approved}`,
  );

  const searchLogs = await apiGet(page, "/api/v1/logs");
  const lines = (searchLogs.logs ?? []).map((entry) => entry.message);
  const aiScored = lines.some((line) => /\[ LLM \] job_score .*-> (ok|cached)/i.test(line));
  const fellBack = lines.some((line) => /heur[ií]stica offline|quota_exhausted|model_not_found/i.test(line));
  record(
    "the AI scored the jobs - not the offline heuristic quietly catching a fall",
    aiScored && !fellBack && (diagnostics.scoredOffline ?? 0) === 0,
    `${lines.find((line) => /job_score/.test(line)) ?? "no job_score line at all"} - scoredOffline=${diagnostics.scoredOffline}`,
  );
  await shot("04-search");

  const body = await page.locator("body").innerText();
  const errored = /failed|nao foi possivel|não foi possível|unable to|error:/i.test(body);
  record("no error surfaced in the packaged UI", !errored, errored ? "an error string is visible" : "clean");
} catch (error) {
  const file = page
    ? await page
        .screenshot({ path: path.join(outDir, "99-failure.png") })
        .then(() => path.join(outDir, "99-failure.png"))
        .catch(() => "no screenshot")
    : "no window";
  record("run completed", false, `${error.message.split("\n")[0]} - ${file}`);
  if (page) {
    const logs = await apiGet(page, "/api/v1/logs").catch(() => ({ logs: [] }));
    const tail = (logs.logs ?? []).slice(-12).map((entry) => `${entry.level}: ${entry.message}`);
    if (tail.length) console.log(`\n--- backend log (tail) ---\n${tail.join("\n")}`);
  }
} finally {
  await app.close().catch(() => {});
  const failed = steps.filter((step) => !step.ok);
  console.log(`\nPackaged app: ${appExe}`);
  console.log(`Fresh profile: ${profileRoot}`);
  console.log(`Screenshots: ${outDir}`);
  console.log(`=== ${steps.length - failed.length}/${steps.length} passed ===`);
  if (failed.length > 0) process.exit(1);
}

// The run nobody ever does: a brand new user, with a brand new API key.
//
// Every other smoke here starts from a COPY of a developer database — its
// resume, roles, key, and stored model — and then overrides the model and
// the key by environment variable. That is testing the app on the one machine where
// it is guaranteed to work.
//
// clean-install-smoke.mjs does drive a genuinely isolated profile, but deliberately
// with NO AI key: it proves the app installs and degrades gracefully offline. So the
// only suite that ever saw a fresh install was the only one that never called a
// provider — which is precisely how this shipped:
//
//   404 "This model models/gemini-2.5-flash is no longer available to new users."
//
// A retired model keeps answering for the project that already used it. The default
// model worked perfectly for an existing account and returned 404 to every key created after
// the cutoff. No test failed. The dev machine lies, and the only way to catch it
// lying is to throw the dev machine away AND bring a real key.
//
// So: an empty database, factory defaults, nothing seeded. The key is typed into
// Settings the way a person types it. The resume arrives as a file, because a fresh
// install has no profile resume to fall back on. The search runs on a role configured
// through the UI. If this passes, the app works for someone who is not us.
//
// Prerequisites:
//   npm run backend:build ; npm run electron:build
//   npm run dev -w @sencia/desktop     # Vite on :1420, another shell
//
// Env:
//   SENCIA_FRESH_API_KEY   required — a REAL provider key, typed into Settings.
//   SENCIA_OUT_DIR         screenshots
//
// It deliberately accepts no model override. The model under test is whatever the app
// ships as its default; choosing one here would put back the exact blindfold this run
// exists to remove.

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const desktopDir = path.join(repoRoot, "apps/desktop");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-fresh-user-"));

const AI_TIMEOUT = 240_000;
const SEARCH_TIMEOUT = 420_000;

const apiKey = process.env.SENCIA_FRESH_API_KEY;
if (!apiKey) {
  console.error("SENCIA_FRESH_API_KEY is required: this run types a real key into Settings, as a new user would.");
  process.exit(1);
}

// A path that does not exist yet. The backend migrates a database into being here,
// which is what a real first launch does.
const dbPath = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "sencia-fresh-db-")), "sencia.db");

// A deterministic, synthetic resume in the form a real person has one: a file.
// It is versioned so this flow also works from a clean checkout.
const resumeFile = path.join(repoRoot, "scripts/qa/fixtures/personas/1-software-backend.pdf");

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
    const response = await fetch(`http://127.0.0.1:48730${route}`, { headers: { Authorization: `Bearer ${token}` } });
    return response.json();
  }, route);
}

if (!fs.existsSync(resumeFile)) {
  console.error(`Missing fixture: ${resumeFile}`);
  process.exit(1);
}

const devServer = await fetch("http://127.0.0.1:1420").catch(() => null);
if (!devServer?.ok) {
  console.error("The Vite dev server is not on :1420. Run `npm run dev -w @sencia/desktop` first.");
  process.exit(1);
}

const app = await electron.launch({
  args: [desktopDir],
  cwd: desktopDir,
  env: { ...process.env, SENCIA_DB_PATH: dbPath },
});

let page;
try {
  page = await app.firstWindow();
  page.on("pageerror", (error) => console.log(`      [renderer crash] ${error.message}`));

  const shot = async (name) => {
    const file = path.join(outDir, `${name}.png`);
    await page.screenshot({ path: file });
    return file;
  };

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
  record("first launch on a database that did not exist", fs.existsSync(dbPath), `the backend migrated one into being`);

  // What the app actually ships with, read back from the config it just wrote — not
  // from a constant in the source, and not from anything this script chose.
  const config = await apiGet(page, "/api/v1/config");
  const model = config.form?.model ?? "";
  const provider = config.form?.provider ?? "";
  record(
    "the factory default model is not one Google has retired",
    /flash/.test(model) && !/2\.5-flash/.test(model),
    `${provider} / ${model} · apiKeySet=${config.apiKeySet}`,
  );

  // --- Onboarding, the way a person does it --------------------------------------

  await page.getByRole("button", { name: /configuracoes/i }).first().click({ timeout: 20_000 });
  await page.getByRole("button", { name: /^salvar$/i }).waitFor({ timeout: 20_000 });
  record("opened Settings", true, await shot("01-settings"));

  // Settings opens on "Provedores de IA", and only the active section is in the DOM —
  // so each field has to be navigated to, exactly as a person navigates to it.

  // The key, typed rather than injected. It is the one field standing between a new
  // install and an app that can do nothing at all.
  const keyField = page.locator('input[placeholder*="Cole a chave"]').first();
  await keyField.waitFor({ timeout: 20_000 });
  await keyField.fill(apiKey);
  await page.getByRole("button", { name: /permitir processamento seguro por ia/i }).click({ timeout: 20_000 });

  // And a role, because the factory config has none and a search with nothing to
  // search for is not a search. It lives under a different section.
  await page.getByRole("button", { name: /perfil e curriculo/i }).click({ timeout: 20_000 });
  const roleField = page.getByLabel(/cargo alvo/i).first();
  await roleField.waitFor({ timeout: 20_000 });
  await roleField.fill("Backend Engineer");

  await page.getByRole("button", { name: /^salvar$/i }).click({ timeout: 20_000 });
  await page.waitForTimeout(2500);

  const saved = await apiGet(page, "/api/v1/config");
  record(
    "key and role saved through the UI",
    Boolean(saved.apiKeySet) && Boolean(saved.form?.role) && saved.form?.aiDataConsent === true,
    `apiKeySet=${saved.apiKeySet} consent=${saved.form?.aiDataConsent} role=${saved.form?.role} model=${saved.form?.model}`,
  );
  await shot("02-configured");

  // --- Resume Studio, from a file, because there is nothing else ------------------

  await page.getByRole("button", { name: /^curr|resume/i }).first().click({ timeout: 30_000 });
  await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });

  await page.locator("input.resume-file-input").setInputFiles(resumeFile);
  await page.getByRole("heading", { name: /ats diagnosis/i }).waitFor({ timeout: 60_000 });
  record("uploaded a PDF into an app that had never seen a resume", true, path.basename(resumeFile));

  await clickWhenReady(page, /^parse/i);
  await page.getByRole("heading", { name: /target job/i }).waitFor({ timeout: AI_TIMEOUT });
  await waitUntilIdle(page, /analyze job/i);
  const parsedName = await page.locator(".resume-parsed-name").first().innerText().catch(() => "");
  record("the shipped default model parsed it", true, parsedName || "parsed");
  await shot("03-parsed");

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
  record("tailored and scored it, all on the shipped default", true, await shot("04-tailored"));

  // --- Search --------------------------------------------------------------------

  await page.getByRole("button", { name: /buscar vagas/i }).first().click({ timeout: 20_000 });
  await page.getByRole("button", { name: /nova busca/i }).first().click({ timeout: 20_000 });
  await page.locator(".job-card").first().waitFor({ state: "visible", timeout: SEARCH_TIMEOUT });

  // The first card is not the finish line, and asserting on it is how the first
  // version of this check went wrong. Jobs the deterministic pre-filter rejects are
  // scored offline and emitted immediately, so a card can appear before the AI has
  // been asked anything at all. Wait for the search to actually end.
  const status = await page.waitForFunction(
    async () => {
      const token = await window.senciaElectron.getApiToken();
      const response = await fetch("http://127.0.0.1:48730/api/v1/search/status", {
        headers: { Authorization: `Bearer ${token}` },
      });
      const state = await response.json();
      return state.running ? null : state;
    },
    null,
    { timeout: SEARCH_TIMEOUT, polling: 3000 },
  );
  const state = await status.jsonValue();
  const jobs = await page.locator(".job-card").count();
  const diagnostics = state.diagnostics ?? {};
  record(
    "searched the boards on a role configured through the UI",
    jobs > 0,
    `${jobs} approved · collected=${diagnostics.collected} scored=${diagnostics.evaluated} prefiltered=${diagnostics.skippedByPrefilter} cache=${diagnostics.scoredFromCache}`,
  );
  await shot("05-search");

  // The scores have to have come from the AI, not from the offline heuristic quietly
  // catching a fall. An install whose AI is dead still returns jobs — that is the
  // whole design, and it means "jobs came back" is no evidence at all that the AI
  // works. This is.
  const logs = await apiGet(page, "/api/v1/logs");
  const llm = (logs.logs ?? []).map((entry) => entry.message).filter((line) => /\[ LLM \]/.test(line));
  const scoredByAI = llm.some((line) => /job_score .*-> (ok|cached)/i.test(line));
  const degraded = llm.some((line) => /heurística offline|quota_exhausted|model_not_found|invalid_response/i.test(line));
  record(
    "the AI actually scored the jobs — no silent fallback",
    scoredByAI && !degraded,
    llm.filter((line) => /job_score|offline|quota/i.test(line)).slice(-3).join(" | ") || "no job_score line at all",
  );

  const body = await page.locator("body").innerText();
  const errored = /failed|não foi possível|unable to|error:/i.test(body);
  record("no error surfaced anywhere in onboarding", !errored, errored ? "an error string is visible" : "clean");
} catch (error) {
  const file = page
    ? await page
        .screenshot({ path: path.join(outDir, "99-failure.png") })
        .then(() => path.join(outDir, "99-failure.png"))
        .catch(() => "no screenshot")
    : "no window";
  record("run completed", false, `${error.message.split("\n")[0]} — ${file}`);
  if (page) {
    const logs = await apiGet(page, "/api/v1/logs").catch(() => ({ logs: [] }));
    const tail = (logs.logs ?? []).slice(-12).map((entry) => `${entry.level}: ${entry.message}`);
    if (tail.length) console.log(`\n--- backend log (tail) ---\n${tail.join("\n")}`);
  }
} finally {
  await app.close().catch(() => {});
  const failed = steps.filter((step) => !step.ok);
  console.log(`\nFresh database: ${dbPath}`);
  console.log(`Screenshots: ${outDir}`);
  console.log(`=== ${steps.length - failed.length}/${steps.length} passed ===`);
  if (failed.length > 0) process.exit(1);
}

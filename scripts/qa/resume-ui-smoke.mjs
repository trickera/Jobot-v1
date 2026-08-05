// Drives the Electron app itself: the window, the clicks, the rendered result.
// This is the only check that sees what the user sees.
//
// The other two smokes each cover a layer and miss this one. resume-studio-smoke
// hits the backend routes; resume-client-smoke drives the real api.ts. Neither
// would notice a Resume Studio tab that renders a blank panel, a progress card
// that never resolves, or a button wired to nothing.
//
// Prerequisites (the launcher does not build or start anything for you):
//   npm run backend:build            # Electron's main process spawns this exe
//   npm run electron:build           # dist-electron/main.cjs
//   npm run dev -w @sencia/desktop   # Vite on :1420, in another shell
//
// The Vite server is not optional. An Electron run from source has
// app.isPackaged === false, and main.ts reads that as dev mode and loads the
// renderer from http://127.0.0.1:1420 — with no server there the window paints a
// blank chrome-error page, which is indistinguishable from a crashed app.
//
// Usage:
//   node scripts/qa/resume-ui-smoke.mjs
//
// Env:
//   SENCIA_DB_PATH   strongly recommended — point at a COPY of a real db, so the
//                    run uses a real resume and API key without touching the
//                    user's own data. Electron passes its env to the backend it
//                    spawns. The AI steps need a configured key.
//   SENCIA_OUT_DIR   where screenshots land; default a temp dir.

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const desktopDir = path.join(repoRoot, "apps/desktop");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-ui-smoke-"));

// AI steps are slow on a cold cache and can sit behind a provider rate limit.
const AI_TIMEOUT = 240_000;

// The view gates every action button on a single busyStep, so exactly one AI
// operation can be in flight. Waiting for a button to merely EXIST is therefore
// not enough — sections appear as soon as an offline canonical exists, while the
// AI call behind them is still running and every button is still disabled.
// Waiting for enabled is what actually says "the previous step finished".
async function clickWhenReady(page, name) {
  const button = page.getByRole("button", { name }).first();
  await button.waitFor({ state: "visible", timeout: AI_TIMEOUT });
  await button.click({ timeout: AI_TIMEOUT }); // Playwright waits for enabled.
}

async function waitUntilIdle(page, name) {
  await page.getByRole("button", { name }).first().waitFor({ state: "visible", timeout: AI_TIMEOUT });
  await page
    .getByRole("button", { name })
    .first()
    .and(page.locator("button:not([disabled])"))
    .waitFor({ timeout: AI_TIMEOUT });
}

const JOB_DESCRIPTION = `Senior Infrastructure Engineer - NovaTech (Remote)
We need someone to own our AWS and Kubernetes platform.
Requirements: 5+ years infrastructure/DevOps, AWS, Terraform, Kubernetes, Docker, Linux.
CI/CD with GitLab or Jenkins. Observability with Prometheus and Grafana. Python or Bash.
Nice to have: Helm, Argo CD, FinOps cost work, on-call experience.`;

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
}

// Points the run at a specific provider/model/key. API keys are encrypted at
// rest, so a test key cannot be seeded by editing the database copy — it has to
// go in through the same endpoint the Settings view writes to. Sending an empty
// apiKey with apiKeySet intact leaves whatever key is already there alone, so
// setting only SENCIA_TEST_MODEL is safe.
async function seedProvider(page) {
  const model = process.env.SENCIA_TEST_MODEL;
  const apiKey = process.env.SENCIA_TEST_API_KEY;
  if (!model && !apiKey) return null;

  return page.evaluate(
    async ({ model, apiKey }) => {
      const token = await window.senciaElectron.getApiToken();
      const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
      const base = "http://127.0.0.1:48730/api/v1/config";

      const current = await (await fetch(base, { headers })).json();
      const next = { ...current, form: { ...current.form } };
      if (model) next.form.model = model;
      if (apiKey) next.form.apiKey = apiKey;

      const response = await fetch(base, { method: "PUT", headers, body: JSON.stringify(next) });
      if (!response.ok) return `config PUT failed: ${response.status} ${await response.text()}`;
      return `${next.form.provider} / ${next.form.model}`;
    },
    { model, apiKey },
  );
}

// A key with nothing left for the day cannot verify the AI steps, and reporting
// that as a failed assertion would blame the app for the quota. Read the backend
// log and say which it was.
async function backendLog(page, count = 20) {
  return page
    .evaluate(async (n) => {
      const token = await window.senciaElectron.getApiToken();
      const response = await fetch("http://127.0.0.1:48730/api/v1/logs", {
        headers: { Authorization: `Bearer ${token}` },
      });
      const payload = await response.json();
      return (payload.logs ?? []).slice(-n).map((entry) => `${entry.level}: ${entry.message}`);
    }, count)
    .catch(() => []);
}

// Distinguishes "the AI refused us" from "the app is broken". It deliberately
// does not guess WHICH refusal — a spent daily quota, a per-minute rate limit and
// a token-per-minute cap all stop the run, and the backend log printed alongside
// this says which one it actually was.
async function aiUnavailable(page) {
  const lines = await backendLog(page, 40);
  return lines.some((line) => /quota_exhausted|rate_limited|cota diária|orçamento diário/i.test(line));
}

const devServer = await fetch("http://127.0.0.1:1420").catch(() => null);
if (!devServer?.ok) {
  console.error("The Vite dev server is not on :1420. Run `npm run dev -w @sencia/desktop` first.");
  process.exit(1);
}

const app = await electron.launch({ args: [desktopDir], cwd: desktopDir, env: { ...process.env } });

let page;
let quotaSpent = false;
try {
  page = await app.firstWindow();

  // A renderer that dies on load paints a blank window, which looks exactly like
  // success to anything that only checks the process is alive. Capture why.
  page.on("pageerror", (error) => console.log(`      [renderer crash] ${error.message}`));

  const shot = async (name) => {
    const file = path.join(outDir, `${name}.png`);
    await page.screenshot({ path: file });
    return file;
  };

  await page.waitForLoadState("domcontentloaded");
  await page.waitForSelector("button", { timeout: 60_000 });
  record("app window rendered", true, await shot("01-launch"));

  // The backend is spawned by the main process; the Resume tab is useless until
  // it answers.
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
  record("backend answered /health", true, "spawned by the Electron main process");

  const seeded = await seedProvider(page);
  if (seeded) record("seeded the provider under test", !seeded.startsWith("config PUT failed"), seeded);

  await page.getByRole("button", { name: /resume/i }).first().click({ timeout: 30_000 });
  await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });
  record("opened Resume Studio", true, await shot("02-resume-tab"));

  // "Use profile resume" fills the textarea from the resume already in config —
  // a real document, and no file picker to drive.
  await page.getByRole("button", { name: /use profile resume/i }).click({ timeout: 20_000 });
  const textarea = page.locator("textarea").first();
  await textarea.waitFor({ timeout: 20_000 });
  const pastedLength = (await textarea.inputValue()).length;
  record("loaded the profile resume", pastedLength > 100, `${pastedLength} chars in the textarea`);

  // Parse — the first async job, and the proof that runResumeJob works through
  // the real UI rather than through a script. "3. Target job" appears as soon as
  // an offline canonical exists, so the flow is only really parsed once the
  // AI-gated Analyze job button becomes clickable again.
  await clickWhenReady(page, /^parse/i);
  await page.getByRole("heading", { name: /target job/i }).waitFor({ timeout: AI_TIMEOUT });
  await waitUntilIdle(page, /analyze job/i);
  record("parse resolved and advanced the flow", true, await shot("03-parsed"));

  await page.getByRole("textbox", { name: /job description/i }).fill(JOB_DESCRIPTION);
  await clickWhenReady(page, /analyze job/i);
  await waitUntilIdle(page, /find gaps/i);
  record("analyze-job resolved", true, await shot("04-analyzed"));

  await clickWhenReady(page, /find gaps/i);
  // "Optimize resume" opens a review card first; the AI call is behind the
  // "Generate tailored resume" button inside it. Waiting for the review step to
  // become clickable is what says gap analysis actually finished.
  await waitUntilIdle(page, /^optimize resume/i);
  record("gap analysis resolved", true, await shot("05-gaps"));

  await clickWhenReady(page, /^optimize resume/i);
  record("opened the review-before-generating card", true, await shot("06-review"));

  // Tailoring — the heavy call, and the one that used to die at the old 25s
  // client timeout.
  await clickWhenReady(page, /generate tailored resume/i);
  await waitUntilIdle(page, /compare scores/i);
  record("tailoring resolved", true, await shot("07-tailored"));

  await clickWhenReady(page, /compare scores/i);
  await page.waitForTimeout(3000);
  record("scores compared", true, await shot("08-scores"));

  // Saving a version writes to SQLite — no native dialog — so unlike export it can
  // be driven. It is also the step that populates section 8, which is why it comes
  // before it.
  await clickWhenReady(page, /^save version/i);
  await page.getByText(/version saved/i).first().waitFor({ timeout: 30_000 });
  record("saved a resume version", true, await shot("09-version-saved"));

  // Section 8 lists a job's history, and with no saved job picked it correctly says
  // so instead of listing nothing. Either way it has to render rather than blow up:
  // this panel had never been on screen before.
  const versions = page.locator(".resume-versions-panel");
  await versions.waitFor({ state: "visible", timeout: 20_000 });
  record("versions panel rendered", true, (await versions.innerText()).split("\n")[0]);

  // The last AI call in the app, and the last one never driven by a click.
  await clickWhenReady(page, /generate cover letter/i);
  const letter = page.locator(".cover-letter-preview");
  await letter.waitFor({ state: "visible", timeout: AI_TIMEOUT });
  const letterText = await letter.innerText();
  record("cover letter generated", letterText.length > 200, `${letterText.length} chars`);
  await shot("10-cover-letter");

  // Export stays unclicked, here and in the cover letter's own Save buttons: both
  // end in a native save dialog, which no browser driver can dismiss, so the run
  // would hang on it. resume-client-smoke.mjs covers that path and writes a real
  // PDF to disk.

  // An error string anywhere on screen at the end means a step failed quietly.
  const body = await page.locator("body").innerText();
  const errored = /failed|não foi possível|unable to|error:/i.test(body);
  record("no error surfaced during the flow", !errored, errored ? "an error string is visible" : "clean");
} catch (error) {
  const file = page
    ? await page
        .screenshot({ path: path.join(outDir, "99-failure.png") })
        .then(() => path.join(outDir, "99-failure.png"))
        .catch(() => "no screenshot")
    : "no window";

  // An AI provider refusing us is not an app failure. Say so instead of blaming
  // the code, and let the log below name the actual limit that was hit.
  if (page && (await aiUnavailable(page))) {
    quotaSpent = true;
    console.log(
      `\nSTOPPED  every configured AI provider refused us, so the AI steps cannot be verified now.` +
        `\n         The backend log below names the limit that was hit (daily quota, requests/min, or` +
        `\n         tokens/min). Screenshot: ${file}`,
    );
  } else {
    record("run completed", false, `${error.message.split("\n")[0]} — ${file}`);
  }

  // The screenshot says WHAT the UI showed; the backend log says WHY.
  if (page) {
    const logs = await backendLog(page, 15);
    if (logs.length) console.log(`\n--- backend log (tail) ---\n${logs.join("\n")}`);
  }
} finally {
  await app.close().catch(() => {});
  const failed = steps.filter((step) => !step.ok);
  console.log(`\nScreenshots: ${outDir}`);
  console.log(`=== ${steps.length - failed.length}/${steps.length} passed${quotaSpent ? ", AI steps not reached (quota spent)" : ""} ===`);
  // A spent quota exits 2, so a CI run can tell "the app is broken" from "there
  // was no AI left to test with".
  if (failed.length > 0) process.exit(1);
  if (quotaSpent) process.exit(2);
}

// Nobody had ever watched the screen while the AI refused.
//
// The classification is well covered in Go (llm_provider_error_test.go,
// resume_errors_test.go): a 429 with a daily-quota body becomes
// ai_quota_exhausted, a 404 becomes ai_model_unavailable, and so on. What has
// never been looked at is the other end of that wire -- whether the message the
// user is supposed to read actually lands on the screen, in full, inside the
// window. A message that is right in Go and truncated, blank, or scrolled out of
// view in the renderer is a message the user never gets.
//
// Two ways in, because the app does not treat these failures alike:
//
// ai_budget_spent is forced FOR REAL, end to end. It is the app's own daily
// request budget, checked in spendDailyRequest before any provider call, so
// setting llmRequestsPerDay to 1 and making two AI calls refuses the second one
// with no provider involved and no quota spent.
//
// The other three are simulated at the backend's own response boundary, because
// with a real provider they are close to unreachable -- and that is worth saying
// plainly. runLLMWithFallback cascades across geminiModelFallbacks
// (gemini-flash-lite-latest, gemini-3-flash-preview, gemini-flash-latest), all of
// which answer. Point the app at a retired model and it does NOT show
// ai_model_unavailable: it quietly falls back to a sibling and succeeds. Free-tier
// quota on Gemini is per-model, so a 429 on one model does not stop the next.
// Reaching the user therefore takes the entire ladder failing at once, which is
// good design and also means these three screens are all but dead code -- and
// dead code that is never looked at is how the last six bugs got in.
//
// So the payloads below are the real ones, copied from classifyResumeError in
// resume_errors.go, injected on the response the renderer actually polls
// (/api/v1/resume/jobs/{id}, since runResumeJob is async). Everything downstream
// -- ApiError, fail(), setNotice, the toast -- is the shipped code.
//
// Env:
//   SENCIA_FRESH_API_KEY   required (the budget run makes one real AI call)
//   SENCIA_APP_EXE         the INSTALLED exe
//   SENCIA_OUT_DIR         screenshots
//
// Run:
//   node --env-file=.env scripts/qa/ai-refusal-ui-smoke.mjs

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const appExe =
  process.env.SENCIA_APP_EXE ?? path.join(repoRoot, "release/electron/win-unpacked/Sencia Job.exe");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-refusal-"));
fs.mkdirSync(outDir, { recursive: true });

const AI_TIMEOUT = 240_000;
const API = "http://127.0.0.1:48730";

const apiKey = process.env.SENCIA_FRESH_API_KEY;
if (!apiKey) {
  console.error("SENCIA_FRESH_API_KEY is required.");
  process.exit(1);
}
if (!fs.existsSync(appExe)) {
  console.error(`No packaged app at ${appExe}.`);
  process.exit(1);
}

const resumeFile = path.join(repoRoot, "scripts/qa/fixtures/resumes/nursing.pdf");

// Verbatim from classifyResumeError (resume_errors.go). If these drift, the UI is
// showing something this run never checked.
const REFUSALS = [
  {
    code: "ai_quota_exhausted",
    status: 502,
    message:
      "This API key's daily AI quota is spent. It resets tomorrow. To keep going now, add a different provider under Settings > AI.",
  },
  {
    code: "ai_model_unavailable",
    status: 502,
    message:
      "The AI model set in Settings no longer exists for your API key. Providers retire model names over time. Pick a current model in Settings > AI.",
  },
  {
    code: "ai_rate_limited",
    status: 502,
    message:
      "The AI provider is rate-limiting requests (quota). Wait a minute and try again, or switch provider/model in Settings.",
  },
];

const BUDGET_SPENT_MESSAGE =
  "This app's daily AI request budget is used up. Raise the daily budget in Settings > AI to keep going, or wait for it to reset tomorrow.";

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
}

// The whole point of this run. Not "an error object existed" -- the user can read
// it, all of it, without scrolling.
//
// fail() writes to two surfaces: an inline notice at the top of the Resume Studio
// page, and a toast. The toast is the one that is guaranteed to be seen -- it is
// fixed-position, so it is on screen wherever the user has scrolled to. The
// inline notice is NOT: click "Analyze job" in section 3 with the page scrolled
// down and the notice renders above the fold, measured here at y = -384 in an
// 800px window. It is a genuine rough edge (the toast auto-dismisses; the notice
// is the lasting record, and it is where the user is not looking), but the toast
// covers it, so the message does reach them. Both are measured below rather than
// asserted as one thing, because they are not one thing.
async function assertOnScreen(page, label, expectedMessage, shotName) {
  const toast = page.locator(".toast").filter({ hasText: expectedMessage.slice(0, 40) }).first();
  let visible = false;
  try {
    await toast.waitFor({ state: "visible", timeout: 20_000 });
    visible = true;
  } catch {
    visible = false;
  }

  await page.screenshot({ path: path.join(outDir, `${shotName}.png`) });

  if (!visible) {
    const body = await page.locator("body").innerText();
    record(
      `${label}: the message reaches the screen`,
      false,
      `no toast carries it. Body says: ${body.slice(0, 160).replace(/\s+/g, " ")}`,
    );
    return;
  }

  const shown = (await toast.innerText()).trim();
  const complete = shown.includes(expectedMessage);
  record(
    `${label}: the toast carries the message, in full`,
    complete,
    complete ? `"${shown.replace(/\s+/g, " ").slice(0, 90)}..."` : `TRUNCATED / ALTERED: "${shown}"`,
  );

  const viewport = page.viewportSize() ?? { width: 1280, height: 800 };
  const toastBox = await toast.boundingBox();
  const toastInside =
    toastBox !== null &&
    toastBox.y >= 0 &&
    toastBox.x >= 0 &&
    toastBox.y < viewport.height &&
    toastBox.x < viewport.width;
  record(
    `${label}: the toast is inside the window`,
    toastInside,
    toastBox
      ? `at (${Math.round(toastBox.x)}, ${Math.round(toastBox.y)}) in ${viewport.width}x${viewport.height}`
      : "no box",
  );

  // Informational: where the lasting inline record landed. Not a gate -- the
  // toast already carried the message -- but this is what would have to be fixed
  // for a user who dismissed the toast to still find out why nothing happened.
  const notice = page.locator(".inline-notice.error").filter({ hasText: expectedMessage.slice(0, 40) }).first();
  const noticeBox = await notice.boundingBox().catch(() => null);
  console.log(
    noticeBox && noticeBox.y >= 0
      ? `      (inline notice also in view, at y=${Math.round(noticeBox.y)})`
      : `      (inline notice is ABOVE THE FOLD at y=${noticeBox ? Math.round(noticeBox.y) : "n/a"} - only the toast is visible)`,
  );
}

const portInUse = await fetch(`${API}/health`)
  .then(() => true)
  .catch(() => false);
if (portInUse) {
  console.error(`Something is already serving ${API}. Close the running Sencia Job first.`);
  process.exit(1);
}

const profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), "sencia-refusal-profile-"));
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

  // Key in, and the app's own daily budget set to a single request.
  const armed = await page.evaluate(async (key) => {
    const token = await window.senciaElectron.getApiToken();
    const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
    const config = await (await fetch("http://127.0.0.1:48730/api/v1/config", { headers })).json();
    const put = await fetch("http://127.0.0.1:48730/api/v1/config", {
      method: "PUT",
      headers,
      body: JSON.stringify({
        // The key lives on form.apiKey (server.go's form struct), not at the top
        // level -- GET only ever echoes back the apiKeySet boolean, so putting it
        // at the top level is silently accepted and silently dropped.
        ...config,
        form: { ...config.form, apiKey: key, role: "DevOps Engineer", aiDataConsent: true, llmRequestsPerDay: 1 },
      }),
    });
    if (!put.ok) return null;
    const saved = await (await fetch("http://127.0.0.1:48730/api/v1/config", { headers })).json();
    return { budget: saved.form.llmRequestsPerDay, keySet: saved.apiKeySet };
  }, apiKey);
  record(
    "a key is stored and the app's daily AI budget is one request",
    armed?.budget === 1 && armed?.keySet === true,
    `llmRequestsPerDay=${armed?.budget} apiKeySet=${armed?.keySet}`,
  );

  await page.getByRole("button", { name: /^curr|resume/i }).first().click({ timeout: 30_000 });
  await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });
  await page.locator("input.resume-file-input").setInputFiles(resumeFile);
  await page.getByRole("heading", { name: /ats diagnosis/i }).waitFor({ timeout: 60_000 });

  // --- ai_budget_spent, for real ---------------------------------------------
  // The first Parse spends the only request in the budget. It succeeds.
  const parseButton = page.getByRole("button", { name: /^parse/i }).first();
  await parseButton.waitFor({ state: "visible", timeout: AI_TIMEOUT });
  await Promise.all([
    page.waitForResponse(
      async (response) => {
        if (!response.url().includes("/api/v1/resume/jobs/")) return false;
        try {
          return (await response.json()).state === "done";
        } catch {
          return false;
        }
      },
      { timeout: AI_TIMEOUT },
    ),
    parseButton.click({ timeout: AI_TIMEOUT }),
  ]);
  await page.getByRole("heading", { name: /target job/i }).waitFor({ timeout: AI_TIMEOUT });

  // The second one has nothing left to spend, and is refused before a single byte
  // goes to Google.
  await page.getByRole("textbox", { name: /job description/i }).fill(
    "Registered Nurse - ICU. Requirements: BSN, ACLS, three years of critical care.",
  );
  await page.getByRole("button", { name: /analyze job/i }).first().click({ timeout: AI_TIMEOUT });
  await assertOnScreen(page, "ai_budget_spent (real)", BUDGET_SPENT_MESSAGE, "01-ai_budget_spent");

  // --- the three the cascade hides -------------------------------------------
  for (const refusal of REFUSALS) {
    await page.reload();
    await page.waitForSelector("button", { timeout: 30_000 });
    await page.getByRole("button", { name: /^curr|resume/i }).first().click({ timeout: 30_000 });
    await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });
    await page.locator("input.resume-file-input").setInputFiles(resumeFile);
    await page.getByRole("heading", { name: /ats diagnosis/i }).waitFor({ timeout: 60_000 });

    // Answer the poll runResumeJob is already making, with what the backend would
    // have said. Everything after this line -- ApiError, fail(), setNotice, the
    // toast -- is the app's own code, untouched.
    await page.route("**/api/v1/resume/jobs/**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          state: "done",
          status: refusal.status,
          result: { message: refusal.message, code: refusal.code },
        }),
      });
    });

    await page.getByRole("button", { name: /^parse/i }).first().click({ timeout: AI_TIMEOUT });
    await assertOnScreen(page, refusal.code, refusal.message, `0${REFUSALS.indexOf(refusal) + 2}-${refusal.code}`);
    await page.unroute("**/api/v1/resume/jobs/**");
  }
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

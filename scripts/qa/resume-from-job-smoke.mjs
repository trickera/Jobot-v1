// The other way into Resume Studio, and the one an actual user takes: find a job
// in the search results, open it, and hit "Otimizar currículo". The studio then
// opens with that job already attached.
//
// resume-ui-smoke.mjs drives the studio standalone, with a pasted job description
// and no job attached. That leaves one whole panel unreachable: Section 8 lists a
// job's saved versions, and with no job it can only say "pick a job". So the panel
// that stores every tailored resume the user produces had never once been seen
// with anything in it.
//
// Prerequisites — same as resume-ui-smoke.mjs:
//   npm run backend:build ; npm run electron:build
//   npm run dev -w @sencia/desktop     # Vite on :1420, another shell
//
// Env:
//   SENCIA_DB_PATH       point at a COPY of a real db — it must already contain
//                        jobs, since this flow starts from the job list.
//   SENCIA_TEST_MODEL    optional provider/model override (see seedProvider)
//   SENCIA_TEST_API_KEY
//   SENCIA_OUT_DIR       screenshots

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const desktopDir = path.join(repoRoot, "apps/desktop");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-job-flow-"));

const AI_TIMEOUT = 240_000;

// A real search scrapes real boards under the app's own time budget (240s by
// default) and only then scores what it found, so the first card can be a long way
// out. This waits for the first one rather than for the whole search to finish.
const SEARCH_TIMEOUT = 420_000;

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
      if (!response.ok) return `config PUT failed: ${response.status}`;
      return `${next.form.provider} / ${next.form.model}`;
    },
    { model, apiKey },
  );
}

const devServer = await fetch("http://127.0.0.1:1420").catch(() => null);
if (!devServer?.ok) {
  console.error("The Vite dev server is not on :1420. Run `npm run dev -w @sencia/desktop` first.");
  process.exit(1);
}

const app = await electron.launch({ args: [desktopDir], cwd: desktopDir, env: { ...process.env } });

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
  record("app up, backend answering", true, "spawned by the Electron main process");

  const seeded = await seedProvider(page);
  if (seeded) record("seeded the provider under test", !seeded.startsWith("config PUT failed"), seeded);

  // A real search has to run first. The results list is session state, not a view
  // of the database — a fresh window shows "Nenhuma busca executada" no matter how
  // many jobs are stored — so there is no way to reach a job card without going and
  // finding one. Which makes this the whole flow, end to end, as a user has it:
  // search the boards, open a result, tailor the resume to it.
  await page.getByRole("button", { name: /nova busca/i }).first().click({ timeout: 20_000 });
  const jobCard = page.locator(".job-card").first();
  await jobCard.waitFor({ state: "visible", timeout: SEARCH_TIMEOUT });
  await jobCard.click();
  const jobTitle = (await jobCard.innerText()).replace(/\s+/g, " ").slice(0, 80);
  record("searched, then picked a job from the results", true, jobTitle);

  await clickWhenReady(page, /otimizar currículo/i);
  await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });
  record("opened Resume Studio from the job", true, await shot("01-studio-from-job"));

  await page.getByRole("button", { name: /use profile resume/i }).click({ timeout: 20_000 });
  await clickWhenReady(page, /^parse/i);
  await waitUntilIdle(page, /analyze job/i);
  record("parse resolved", true, await shot("02-parsed"));

  // Section 3 only exists once a resume is parsed, which is why this is checked
  // here rather than on arrival. The job travelled with the click, so its
  // description should already be in the box with nothing pasted. If it is empty
  // the handoff silently dropped the job, and every step below would still pass
  // while testing the wrong thing.
  const description = await page.getByRole("textbox", { name: /job description/i }).inputValue();
  record("the job came with it", description.trim().length > 0, `${description.trim().length} chars prefilled, no pasting`);

  await clickWhenReady(page, /analyze job/i);
  await waitUntilIdle(page, /find gaps/i);
  await clickWhenReady(page, /find gaps/i);
  await waitUntilIdle(page, /^optimize resume/i);
  await clickWhenReady(page, /^optimize resume/i);
  await clickWhenReady(page, /generate tailored resume/i);
  await waitUntilIdle(page, /compare scores/i);
  record("tailored against the job", true, await shot("03-tailored"));

  await clickWhenReady(page, /^save version/i);
  await page.getByText(/version saved/i).first().waitFor({ timeout: 30_000 });
  record("saved a version linked to the job", true, await shot("04-version-saved"));

  // The whole point. With a job attached, Section 8 must LIST the version that was
  // just saved rather than fall back to its "pick a job" hint.
  const versions = page.locator(".resume-versions-panel");
  await versions.waitFor({ state: "visible", timeout: 20_000 });
  const panelText = await versions.innerText();
  const listed = !/pick a job|no versions saved/i.test(panelText);
  record("versions panel listed the saved version", listed, panelText.split("\n").slice(0, 3).join(" | "));
  await shot("05-versions-listed");

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
  record("run completed", false, `${error.message.split("\n")[0]} — ${file}`);
} finally {
  await app.close().catch(() => {});
  const failed = steps.filter((step) => !step.ok);
  console.log(`\nScreenshots: ${outDir}`);
  console.log(`=== ${steps.length - failed.length}/${steps.length} passed ===`);
  if (failed.length > 0) process.exit(1);
}

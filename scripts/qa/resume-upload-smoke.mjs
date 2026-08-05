// The way a new user actually arrives: they have a resume as a FILE. They drop a
// PDF on the dropzone and expect the app to read it.
//
// Nothing tested that. resume-ui-smoke drives "Use profile resume", which loads
// text already sitting in the config; resume-client-smoke posts text it holds in a
// string. Both start from text, so the whole first mile — base64 upload, server-side
// extraction, the scanned-PDF warning, the parse that follows — has never once run
// under a click. It is the first thing an installer does and the last thing anyone
// checked.
//
// The fixtures are deliberately not app-generated. A resume the app exported is a
// resume the app already knows how to lay out, and reading it back proves little.
// These include deterministic synthetic persona PDFs plus format/overflow fixtures:
//
//   scripts/qa/fixtures/personas/*.pdf
//   scripts/qa/fixtures/formats/candidate-resume.docx
//   scripts/qa/fixtures/formats/candidate-resume-with-an-intentionally-long-
//     file-name-for-overflow-validation.pdf
//
// Prerequisites — same as the other UI smokes:
//   npm run backend:build ; npm run electron:build
//   npm run dev -w @sencia/desktop     # Vite on :1420, another shell
//
// Env: SENCIA_DB_PATH, SENCIA_TEST_MODEL, SENCIA_TEST_API_KEY, SENCIA_OUT_DIR

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const desktopDir = path.join(repoRoot, "apps/desktop");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-upload-smoke-"));

const AI_TIMEOUT = 240_000;

const fixtures = [
  {
    label: "a synthetic backend engineer PDF",
    file: path.join(repoRoot, "scripts/qa/fixtures/personas/1-software-backend.pdf"),
    parse: true,
  },
  ...[
    ["a synthetic nursing PDF", "2-nursing.pdf"],
    ["a synthetic finance PDF", "3-finance.pdf"],
    ["a synthetic marketing PDF", "4-marketing.pdf"],
    ["a synthetic product design PDF", "5-product-design.pdf"],
  ].map(([label, filename]) => ({
    label,
    file: path.join(repoRoot, "scripts/qa/fixtures/personas", filename),
    parse: false,
  })),
  {
    label: "a DOCX",
    file: path.join(repoRoot, "scripts/qa/fixtures/formats/candidate-resume.docx"),
    parse: false,
  },
  {
    label: "a PDF with an absurd file name",
    file: path.join(
      repoRoot,
      "scripts/qa/fixtures/formats/candidate-resume-with-an-intentionally-long-file-name-for-overflow-validation.pdf",
    ),
    parse: false,
  },
];

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
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
      return response.ok ? `${next.form.provider} / ${next.form.model}` : `config PUT failed: ${response.status}`;
    },
    { model, apiKey },
  );
}

for (const fixture of fixtures) {
  if (!fs.existsSync(fixture.file)) {
    console.error(`Missing fixture: ${fixture.file}`);
    process.exit(1);
  }
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

  await page.getByRole("button", { name: /resume/i }).first().click({ timeout: 30_000 });
  await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });

  for (const [index, fixture] of fixtures.entries()) {
    // Each file has to be read on a clean slate, or the ATS diagnosis left behind by
    // the previous one would satisfy this file's assertion without it having been
    // read at all. Resetting between them is also the only thing that exercises
    // "Start over", which is an inline confirmation rather than a native dialog and
    // so had no excuse for never having been clicked.
    if (index > 0) {
      await page.getByRole("button", { name: /^start over/i }).click({ timeout: 20_000 });
      await page.getByRole("button", { name: /confirm reset/i }).click({ timeout: 20_000 });
      await page.getByRole("heading", { name: /ats diagnosis/i }).waitFor({ state: "detached", timeout: 20_000 });
      record("start over cleared the session", true, "the ATS diagnosis from the previous file is gone");
    }

    // The dropzone has a real <input type=file> behind it, so the file can be handed
    // over directly. Clicking the dropzone would open the OS picker, which no driver
    // can dismiss — the run would hang there forever.
    await page.locator("input.resume-file-input").setInputFiles(fixture.file);

    // Upload mode never shows the extracted text — it goes straight to the backend
    // and comes back as the offline ATS diagnosis. So the proof that the file was
    // READ, and not merely accepted, is that diagnosis appearing: it is computed
    // from the extracted text and cannot exist without it. An upload that "succeeds"
    // and extracts nothing is the failure worth catching, because on screen it looks
    // exactly like success.
    await page.getByText(path.basename(fixture.file)).first().waitFor({ timeout: 60_000 });
    await page.getByRole("heading", { name: /ats diagnosis/i }).waitFor({ timeout: 60_000 });

    // Filtered by the heading it CONTAINS, not by hasText: a substring match walks up
    // to the outermost section that happens to mention the words, and the first
    // version of this check duly read "1. Base resume ..." and passed on the digit in
    // "1.". A test that passes for the wrong reason is worse than no test, because it
    // is also a claim that the thing was checked.
    const panel = page
      .locator(".resume-section")
      .filter({ has: page.getByRole("heading", { name: /ats diagnosis/i }) })
      .first();
    const diagnosis = (await panel.innerText()).replace(/\s+/g, " ");

    // These sub-scores are computed FROM the extracted text. They cannot be there if
    // nothing was extracted, which is exactly the question being asked. Impact is not
    // among them on purpose: it needs bullets, and an unparsed resume has none.
    const scores = [...diagnosis.matchAll(/(readability|content|keywords)\D{0,12}(\d+)/gi)];
    record(`read ${fixture.label}`, scores.length === 3, scores.map(([, name, value]) => `${name} ${value}`).join(" · ") || diagnosis.slice(0, 90));

    // And it must say so, rather than print the 0 it defaults to. A red bar at zero
    // under "Impact" is the first thing a new user saw after uploading their resume,
    // and it was not a judgement — nothing had looked at the resume yet.
    const unmeasured = /impact\s*not scored yet/i.test(diagnosis);
    record("impact is reported as unmeasured, not as zero", unmeasured, diagnosis.match(/impact[^·\n]{0,20}/i)?.[0] ?? "no impact row found");
    await shot(`0${index + 1}-uploaded`);

    if (!fixture.parse) continue;

    // Only the real PDF is taken all the way through: parsing costs an AI call, and
    // the point here is the first mile, not a fourth run of the tailoring flow.
    const parse = page.getByRole("button", { name: /^parse/i }).first();
    await parse.click({ timeout: AI_TIMEOUT });
    await page.getByRole("heading", { name: /target job/i }).waitFor({ timeout: AI_TIMEOUT });
    await page
      .getByRole("button", { name: /analyze job/i })
      .first()
      .and(page.locator("button:not([disabled])"))
      .waitFor({ timeout: AI_TIMEOUT });

    // The name is what the parser most often gets wrong on a real PDF, because a
    // two-column or heavily styled layout scrambles the text order — and a resume
    // with no name is rejected outright (errMissingName).
    const heading = await page.locator(".resume-parsed-name").first().innerText().catch(() => "");
    record("parsed the uploaded PDF", true, heading ? `parsed as: ${heading}` : "parsed, advanced to Target job");

    // And now that there are bullets, Impact becomes a real number on the same
    // document that a moment ago could not be scored at all. That transition is the
    // point: the zero was never a verdict.
    const afterParse = (await panel.innerText()).replace(/\s+/g, " ");
    const impact = afterParse.match(/impact\D{0,12}(\d+)/i);
    record("impact became a real score once the resume had bullets", Boolean(impact), impact ? `Impact ${impact[1]} (was "not scored yet")` : afterParse.slice(0, 90));
    await shot("0" + (index + 1) + "-parsed");
  }

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

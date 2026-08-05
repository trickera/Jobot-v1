// The parser has only ever been shown one resume.
//
// One two-column DevOps PDF, belonging to the person who wrote the parser. It
// works on that resume. That is the entire evidence base. A parser tested on
// one document has effectively been tested on none, and the failure is quiet:
// a resume that parses into the wrong shape still parses, still renders, still
// exports. Nothing throws.
//
// So this run shows it three resumes it has no reason to handle, and checks the
// shape of what comes back rather than merely that something came back:
//
//   en-two-column.pdf  A left sidebar and a main column, placed by coordinate.
//                      Naive extraction walks the page top to bottom and drops
//                      the skills list into the middle of the job history. If
//                      that happens, sections come back shuffled.
//
//   intern-cv.pdf      A student with no jobs at all. Every signal the parser
//                      leans on -- companies, dates, a headline -- is missing.
//                      Empty experience is the CORRECT answer here, and a parser
//                      that invents a job to fill the section is broken in the
//                      way AGENTS.md's anti-invention rule exists to prevent.
//
//   nursing.pdf        A registered nurse. No Go, no Kubernetes, nothing the
//                      keyword dictionary was tuned on. The check that matters
//                      is not just that it parses -- it is that no tech skill
//                      appears in the output, because none appears in the input.
//
// It drives the PACKAGED .exe, for the same reason packaged-ai-smoke.mjs does.
//
// The canonical resume asserted below is captured off the wire -- it is the
// exact response the app received from the click, not a second call made by
// this script.
//
// Prerequisites:
//   npm run release:electron
//   py -3 scripts/qa/fixtures/make-resume-fixtures.py   (fixtures are committed; this regenerates them)
//
// Env:
//   SENCIA_FRESH_API_KEY   required -- parse is AI-gated.
//   SENCIA_APP_EXE         optional
//   SENCIA_OUT_DIR         screenshots
//
// Run:
//   node --env-file=.env scripts/qa/diverse-resume-smoke.mjs

import { _electron as electron } from "playwright-core";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const appExe =
  process.env.SENCIA_APP_EXE ?? path.join(repoRoot, "release/electron/win-unpacked/Sencia Job.exe");
const fixtures = path.join(repoRoot, "scripts/qa/fixtures/resumes");
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-diverse-"));

const AI_TIMEOUT = 240_000;
const API = "http://127.0.0.1:48730";

const apiKey = process.env.SENCIA_FRESH_API_KEY;
if (!apiKey) {
  console.error("SENCIA_FRESH_API_KEY is required: /api/v1/resume/parse is AI-gated.");
  process.exit(1);
}
if (!fs.existsSync(appExe)) {
  console.error(`No packaged app at ${appExe}. Build it: npm run release:electron`);
  process.exit(1);
}

// Tech vocabulary that must not appear in a nurse's resume. If it does, the
// model is filling gaps from its own priors instead of from the document, which
// is the exact failure the anti-invention rule forbids.
const TECH_WORDS =
  /\b(go|golang|kubernetes|docker|terraform|aws|python|java|react|sql|devops|ci\/cd|microservices)\b/i;

const CASES = [
  {
    file: "en-two-column.pdf",
    label: "English, two-column with a sidebar",
    expectName: /amara\s+okonkwo/i,
    check(canonical) {
      const experience = canonical.experience ?? [];
      const skills = [...(canonical.skills?.hard ?? []), ...(canonical.skills?.tools ?? [])];
      const education = canonical.education ?? [];
      const companies = experience.map((entry) => `${entry.company}`.toLowerCase()).join(" ");
      return [
        [experience.length >= 3, `experience: ${experience.length} roles (expected 3)`],
        // The sidebar sits physically alongside the job history. If the gutter
        // was not respected, these come back attached to the wrong section.
        [
          /monzo/.test(companies) && /deliveroo/.test(companies),
          `companies survived the two-column layout: ${experience.map((e) => e.company).join(", ")}`,
        ],
        [skills.some((s) => /python/i.test(s)), `skills read out of the sidebar: ${skills.slice(0, 6).join(", ")}`],
        [education.length >= 1, `education: ${education.map((e) => e.institution).join(", ") || "none"}`],
      ];
    },
  },
  {
    file: "intern-cv.pdf",
    label: "student, no jobs",
    expectName: /lucas\s+meyer/i,
    check(canonical) {
      const experience = canonical.experience ?? [];
      const education = canonical.education ?? [];
      const projects = canonical.projects ?? [];
      return [
        // Not "experience.length >= 1". This CV has no jobs. Inventing one to
        // fill the section is the failure, not the pass.
        [experience.length === 0, `experience: ${experience.length} (must be 0 - this student has never had a job)`],
        [education.length >= 1, `education: ${education.map((e) => e.institution).join(", ") || "none"}`],
        [projects.length >= 1, `projects picked up instead: ${projects.length}`],
      ];
    },
  },
  {
    file: "nursing.pdf",
    label: "registered nurse, non-technical",
    expectName: /priya\s+raghunathan/i,
    check(canonical) {
      const experience = canonical.experience ?? [];
      const education = canonical.education ?? [];
      const skills = [
        ...(canonical.skills?.hard ?? []),
        ...(canonical.skills?.soft ?? []),
        ...(canonical.skills?.tools ?? []),
      ];
      const invented = skills.filter((skill) => TECH_WORDS.test(skill));
      return [
        [experience.length >= 3, `clinical roles: ${experience.length} (expected 3)`],
        [education.length >= 1, `education: ${education.map((e) => e.degree).join(", ") || "none"}`],
        [skills.length > 0, `skills: ${skills.slice(0, 6).join(", ")}`],
        [
          invented.length === 0,
          invented.length === 0
            ? "no tech vocabulary invented for a nurse"
            : `INVENTED tech skills not in the document: ${invented.join(", ")}`,
        ],
      ];
    },
  },
];

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
}

for (const testCase of CASES) {
  const file = path.join(fixtures, testCase.file);
  if (!fs.existsSync(file)) {
    console.error(`Missing fixture ${file} - run: py -3 scripts/qa/fixtures/make-resume-fixtures.py`);
    process.exit(1);
  }
}

const portInUse = await fetch(`${API}/health`)
  .then(() => true)
  .catch(() => false);
if (portInUse) {
  console.error(`Something is already serving ${API}. Close the running Sencia Job first.`);
  process.exit(1);
}

const profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), "sencia-diverse-profile-"));
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
  const packaged = await app.evaluate(({ app }) => app.isPackaged);
  if (!packaged) throw new Error("app.isPackaged is false - this is not the packaged artifact");

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

  // The key, once, through Settings.
  await page.getByRole("button", { name: /configuracoes/i }).first().click({ timeout: 20_000 });
  const keyField = page.locator('input[placeholder*="Cole a chave"]').first();
  await keyField.waitFor({ timeout: 20_000 });
  await keyField.fill(apiKey);
  await page.getByRole("button", { name: /^salvar$/i }).click({ timeout: 20_000 });
  await page.waitForTimeout(2000);

  await page.getByRole("button", { name: /^curr|resume/i }).first().click({ timeout: 30_000 });
  await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });

  for (const testCase of CASES) {
    const file = path.join(fixtures, testCase.file);
    console.log(`\n--- ${testCase.file}: ${testCase.label} ---`);

    // Upload through the real file input. Text extraction happens here, and for
    // the two-column PDF this is where a naive reader would already have lost.
    const [uploadResponse] = await Promise.all([
      page.waitForResponse((r) => r.url().endsWith("/api/v1/resume") && r.request().method() === "POST", {
        timeout: 60_000,
      }),
      page.locator("input.resume-file-input").setInputFiles(file),
    ]);
    const uploaded = await uploadResponse.json().catch(() => ({}));
    const extracted = String(uploaded.extractedText ?? "");
    record(
      `${testCase.file}: text came out of the PDF`,
      extracted.length > 200,
      `${extracted.length} chars extracted${uploaded.warnings?.length ? ` - warnings: ${uploaded.warnings.join("; ")}` : ""}`,
    );

    await page.getByRole("heading", { name: /ats diagnosis/i }).waitFor({ timeout: 60_000 });

    // The canonical asserted below is the one the app got from this click.
    //
    // The renderer never calls /api/v1/resume/parse. runResumeJob() posts to
    // /api/v1/resume/async/parse, gets a jobId back, and polls
    // /api/v1/resume/jobs/{id} until state === "done" -- so the canonical arrives
    // on a polling response, not on the click's own response. Waiting for
    // /resume/parse waits forever while the parse succeeds on screen behind you.
    const parseButton = page.getByRole("button", { name: /^parse/i }).first();
    await parseButton.waitFor({ state: "visible", timeout: AI_TIMEOUT });
    const [jobResponse] = await Promise.all([
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

    const job = await jobResponse.json();
    const status = job.status ?? 500;
    if (status < 200 || status >= 300) {
      record(
        `${testCase.file}: parsed`,
        false,
        `the app refused it: HTTP ${status} ${job.result?.code ?? ""} - ${job.result?.message ?? ""}`,
      );
      continue;
    }

    // ResumeParseResponse is { documentId, canonical, warnings, providerUsed } --
    // the resume is under .canonical, not at the top level.
    const canonical = job.result?.canonical ?? {};
    const name = canonical.basics?.name ?? "";

    // errMissingName rejects a resume with no name outright, so this is the
    // difference between the app working and the app refusing the document.
    record(
      `${testCase.file}: the name survived`,
      testCase.expectName.test(name),
      `basics.name = ${JSON.stringify(name)}`,
    );

    for (const [ok, detail] of testCase.check(canonical)) {
      record(`${testCase.file}: ${detail.split(":")[0]}`, ok, detail);
    }

    await page.screenshot({ path: path.join(outDir, `${testCase.file.replace(/\.pdf$/, "")}.png`) });
    fs.writeFileSync(
      path.join(outDir, `${testCase.file.replace(/\.pdf$/, "")}.json`),
      JSON.stringify(canonical, null, 2),
    );

    // Back to a clean studio for the next resume.
    await page.reload();
    await page.waitForSelector("button", { timeout: 30_000 });
    await page.getByRole("button", { name: /^curr|resume/i }).first().click({ timeout: 30_000 });
    await page.getByRole("heading", { name: /resume studio/i }).waitFor({ timeout: 30_000 });
  }
} catch (error) {
  if (page) {
    await page.screenshot({ path: path.join(outDir, "99-failure.png") }).catch(() => {});
  }
  record("run completed", false, error.message.split("\n")[0]);
} finally {
  await app.close().catch(() => {});
  const failed = steps.filter((step) => !step.ok);
  console.log(`\nParsed resumes written to: ${outDir}`);
  console.log(`=== ${steps.length - failed.length}/${steps.length} passed ===`);
  if (failed.length > 0) process.exit(1);
}

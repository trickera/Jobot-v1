// Drives the REAL apps/desktop/src/services/api.ts against a running backend.
//
// scripts/qa/resume-studio-smoke.ps1 exercises the backend routes directly, which
// says nothing about whether the client that talks to them works. That gap is not
// theoretical: the Resume Studio AI calls run as background jobs the client polls
// (runResumeJob), and a contract mismatch there breaks the whole tab while every
// backend test still passes.
//
// So this bundles the actual module — no reimplementation, no copy — and calls its
// exported functions exactly as the React views do.
//
// Usage (needs a configured AI key in the target DB):
//   SENCIA_DB_PATH=<copy of a real db> ./bin/sencia-job-backend.exe
//   node scripts/qa/resume-client-smoke.mjs
//
// Env:
//   SENCIA_API_URL    default http://127.0.0.1:48730
//   SENCIA_API_TOKEN  default sencia-dev (the backend's own default)
//   SENCIA_OUT_DIR    where the exported PDF lands; default os.tmpdir()

import { pathToFileURL } from "node:url";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const apiURL = process.env.SENCIA_API_URL ?? "http://127.0.0.1:48730";
const apiToken = process.env.SENCIA_API_TOKEN ?? "sencia-dev";
const outDir = process.env.SENCIA_OUT_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), "sencia-client-smoke-"));

const { build } = await import(pathToFileURL(path.join(repoRoot, "node_modules/esbuild/lib/main.js")).href);
const bundlePath = path.join(outDir, "api.bundle.mjs");

await build({
  entryPoints: [path.join(repoRoot, "apps/desktop/src/services/api.ts")],
  bundle: true,
  format: "esm",
  platform: "node",
  outfile: bundlePath,
  define: {
    "import.meta.env.VITE_SENCIA_API_URL": JSON.stringify(apiURL),
    "import.meta.env.VITE_SENCIA_API_TOKEN": JSON.stringify(apiToken),
  },
  logLevel: "error",
});

const api = await import(pathToFileURL(bundlePath).href);

const steps = [];
function record(name, ok, detail) {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}\n      ${detail}`);
}

async function timed(name, fn) {
  const startedAt = Date.now();
  try {
    const value = await fn();
    record(name, true, `${Date.now() - startedAt}ms`);
    return value;
  } catch (error) {
    record(
      name,
      false,
      `${Date.now() - startedAt}ms — ${error.message} (status=${error.status ?? "-"} code=${error.code ?? "-"})`,
    );
    throw error;
  }
}

const jobDescription = `Senior Infrastructure Engineer - NovaTech (Remote)
We need someone to own our AWS and Kubernetes platform.
Requirements: 5+ years infrastructure/DevOps, AWS, Terraform, Kubernetes, Docker, Linux.
CI/CD with GitLab or Jenkins. Observability with Prometheus and Grafana. Python or Bash.
Nice to have: Helm, Argo CD, FinOps cost work, on-call experience.`;

try {
  const config = await timed("loadConfig", () => api.loadConfig());
  const resumeText = config.form.resumeText;
  if (!resumeText || resumeText.length < 100) {
    throw new Error(`the target DB has no usable resumeText (len=${resumeText?.length ?? 0})`);
  }

  const parsed = await timed("parseResume (async job + poll)", () => api.parseResume(resumeText));
  const canonical = parsed.canonical;
  record(
    "parse produced a canonical resume",
    Boolean(canonical?.basics?.name) && Array.isArray(canonical?.experience),
    `name=${JSON.stringify(canonical?.basics?.name)} experience=${canonical?.experience?.length} provider=${parsed.providerUsed}`,
  );

  const diagnosis = await timed("diagnoseResume (sync, offline)", () => api.diagnoseResume(canonical, resumeText));
  record("diagnose returned scores", diagnosis?.scores != null, JSON.stringify(diagnosis.scores));

  const analyzed = await timed("analyzeJob (async job + poll)", () =>
    api.analyzeJob({ description: jobDescription, category: "tech", seniority: "Senior" }),
  );
  const requirements = analyzed.requirements;
  record(
    "analyze-job extracted requirements",
    (requirements?.hardRequirements?.length ?? 0) > 0,
    `title=${JSON.stringify(requirements?.jobTitle)} hard=${requirements?.hardRequirements?.length}`,
  );

  const gapped = await timed("gapAnalysis (async job + poll)", () => api.gapAnalysis(canonical, requirements));
  const gap = gapped.gap;
  record(
    "gap classified the requirements",
    gap != null && Array.isArray(gap.found),
    `found=${gap?.found?.length} partial=${gap?.partial?.length} missing=${gap?.missing?.length} toConfirm=${gap?.toConfirm?.length}`,
  );

  const before = await timed("scoreResume (sync, deterministic)", () => api.scoreResume(canonical, requirements));

  const confirmed = (gap?.toConfirm ?? []).map((item) => item.term).filter(Boolean);
  const optimized = await timed("optimizeResume (async job + poll)", () =>
    api.optimizeResume(canonical, requirements, confirmed, "en", "third"),
  );
  record(
    "optimize returned a reviewable patch set",
    optimized?.preview != null && Array.isArray(optimized?.patches),
    `patches=${optimized.patches?.length} rejected=${optimized.rejected?.length}`,
  );

  const after = await timed("scoreResume (optimized)", () => api.scoreResume(optimized.preview, requirements));
  record(
    "scores comparable before/after",
    typeof before.ats === "number" && typeof after.ats === "number",
    `ATS ${before.ats} -> ${after.ats} | HR ${before.hr} -> ${after.hr}`,
  );

  const letter = await timed("generateCoverLetter (async job + poll)", () =>
    api.generateCoverLetter({
      canonical: optimized.preview,
      jobDescription,
      company: "NovaTech",
      role: "Senior Infrastructure Engineer",
      language: "en",
      tone: "professional",
    }),
  );
  record("cover letter has content", (letter?.markdown?.length ?? 0) > 200, `${letter.markdown?.length} chars markdown`);

  const templates = await timed("listResumeTemplates", () => api.listResumeTemplates());
  const templateId = templates.templates?.[0]?.id ?? "template:ats-strict";
  const pdf = await timed("exportResume (pdf)", () => api.exportResume(optimized.preview, "pdf", templateId));
  const bytes = Buffer.from(pdf.content, "base64");
  const pdfPath = path.join(outDir, pdf.fileName ?? "resume.pdf");
  fs.writeFileSync(pdfPath, bytes);
  record(
    "export produced a real PDF",
    bytes.subarray(0, 4).toString() === "%PDF" && bytes.length > 5000,
    `${pdfPath} — ${bytes.length} bytes`,
  );
} catch (error) {
  console.log(`\nAborted: ${error.stack}`);
} finally {
  const failed = steps.filter((step) => !step.ok);
  console.log(`\n=== ${steps.length - failed.length}/${steps.length} passed ===`);
  if (failed.length > 0) {
    for (const step of failed) console.log(`FAILED: ${step.name} — ${step.detail}`);
    process.exit(1);
  }
}

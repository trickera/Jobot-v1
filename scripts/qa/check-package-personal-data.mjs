// Privacy gate for the desktop artifact. It intentionally scans only inputs that
// electron-builder packages; historical QA evidence and documentation stay outside
// the artifact and remain untouched by this check.

import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

const repoRoot = path.resolve(import.meta.dirname, "../..");

// Derived from whoever runs the build, never hardcoded: the repository must not
// publish the identity it exists to keep out of the artifact. Matching any
// /home/<name> instead would flag vendored third-party bundles that legitimately
// carry their own build machine's paths (pdf.js is one).
function currentUserPatterns(home = os.homedir()) {
  const account = path.basename(home);
  if (!account) return [];
  const quoted = account.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return [
    ["local Windows profile path", new RegExp(String.raw`c:[\\/]users[\\/]${quoted}\b`, "i")],
    ["local WSL profile path", new RegExp(String.raw`/mnt/c/users/${quoted}\b`, "i")],
    ["local Linux home path", new RegExp(String.raw`/home/${quoted}\b`, "i")],
  ];
}

// Optional, untracked (see .gitignore). Shape: [{ "label": "...", "pattern":
// "...", "flags": "i" }]. Use it for your own name, resume file names, or any
// other identifier that must never reach the packaged artifact.
const overridePath = path.join(repoRoot, "scripts/qa/personal-patterns.local.json");

function loadOverrides(file = overridePath) {
  if (!fs.existsSync(file)) return [];
  const entries = JSON.parse(fs.readFileSync(file, "utf8"));
  return entries.map(({ label, pattern, flags }) => [label, new RegExp(pattern, flags ?? "i")]);
}

const forbiddenContent = [...currentUserPatterns(), ...loadOverrides()];

const packagedRoots = [
  "apps/desktop/dist",
  "apps/desktop/dist-electron",
  "apps/desktop/package.json",
  "apps/backend-go/bin/sencia-job-backend.exe",
  "apps/browser-worker",
];

function filesUnder(target) {
  if (!fs.existsSync(target)) return [];
  const stat = fs.lstatSync(target);
  if (stat.isSymbolicLink()) return [];
  if (stat.isFile()) return [target];
  if (!stat.isDirectory()) return [];
  return fs
    .readdirSync(target, { withFileTypes: true })
    .flatMap((entry) => filesUnder(path.join(target, entry.name)));
}

export function scanPackagedRoots(root, roots = packagedRoots, patterns = forbiddenContent) {
  const findings = [];
  for (const relativeRoot of roots) {
    for (const file of filesUnder(path.join(root, relativeRoot))) {
      if (file.endsWith(".map") || file.includes(`${path.sep}__pycache__${path.sep}`)) continue;
      const relative = path.relative(root, file);
      const contents = fs.readFileSync(file).toString("utf8");
      for (const [label, pattern] of patterns) {
        if (pattern.test(relative) || pattern.test(contents)) findings.push(`${relative}: ${label}`);
      }
    }
  }
  return findings;
}

export function findForbiddenPackageIncludes(builderConfig) {
  const findings = [];
  const activeLines = builderConfig
    .split(/\r?\n/)
    .map((line) => line.replace(/#.*$/, "").trim())
    .filter(Boolean);
  for (const line of activeLines) {
    const packagePath = line.replace(/^-\s*/, "").replace(/^from:\s*/i, "");
    if (/^(?:\.\.[\\/])*qa-artifacts(?:[\\/]|$)/i.test(packagePath)) {
      findings.push(`electron-builder.yml includes QA evidence: ${line}`);
    }
    if (/^(?:\.\.[\\/])*(?:docs|scripts)(?:[\\/]|$)/i.test(packagePath)) {
      findings.push(`electron-builder.yml includes repository-only content: ${line}`);
    }
    if (/^(?:[a-z]:[\\/]|\/(?:home|mnt)\/)/i.test(packagePath)) {
      findings.push(`electron-builder.yml includes an absolute local path: ${line}`);
    }
  }
  return findings;
}

export function findBroadBrowserWorkerIncludes(builderConfig) {
  const findings = [];
  const lines = builderConfig.split(/\r?\n/);

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const normalized = line.trim().replaceAll("\\", "/");
    if (!/^-\s*from:\s*\.\.\/browser-worker\/?$/i.test(normalized)) continue;

    const itemIndent = line.search(/\S/);
    const block = [];
    for (let cursor = index + 1; cursor < lines.length; cursor += 1) {
      const nextLine = lines[cursor];
      if (!nextLine.trim()) continue;
      const nextIndent = nextLine.search(/\S/);
      if (nextIndent <= itemIndent) break;
      block.push(nextLine.trim());
    }

    const filterIndex = block.findIndex((entry) => entry === "filter:");
    const filterEntries = filterIndex < 0
      ? []
      : block.slice(filterIndex + 1).filter((entry) => entry.startsWith("- ")).map((entry) => entry.slice(2).trim());
    if (filterEntries.length !== 1 || filterEntries[0] !== "worker.py") {
      findings.push("electron-builder.yml packages browser-worker files beyond worker.py");
    }
  }

  return findings;
}

function runSelfTest() {
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), "sencia-package-privacy-"));
  try {
    const dist = path.join(temp, "dist");
    fs.mkdirSync(dist, { recursive: true });
    fs.writeFileSync(path.join(dist, "safe.js"), "const candidate = 'Sample Candidate';");
    if (scanPackagedRoots(temp, ["dist"]).length !== 0) throw new Error("safe artifact was rejected");

    const account = "local-user";
    const buildPath = ["C:", "Users", account, "resume.txt"].join("\\");
    fs.writeFileSync(path.join(dist, "unsafe.js"), `const source = '${buildPath}';`);
    const patterns = currentUserPatterns(`C:\\Users\\${account}`);
    if (!scanPackagedRoots(temp, ["dist"], patterns).some((item) => item.includes("local Windows profile path"))) {
      throw new Error("local profile path was not rejected");
    }
    // A different account's build path is third-party noise, not a local leak.
    if (scanPackagedRoots(temp, ["dist"], currentUserPatterns("C:\\Users\\someone-else")).length !== 0) {
      throw new Error("unrelated profile path was rejected");
    }

    const override = path.join(temp, "patterns.local.json");
    fs.writeFileSync(override, JSON.stringify([{ label: "override probe", pattern: "Sample Candidate" }]));
    if (loadOverrides(override).length !== 1) throw new Error("override patterns were not loaded");
    if (loadOverrides(path.join(temp, "missing.json")).length !== 0) {
      throw new Error("missing override file was not tolerated");
    }

    const unsafeConfig = "files:\n  - dist/**\nextraResources:\n  - from: ../../qa-artifacts\n";
    if (!findForbiddenPackageIncludes(unsafeConfig).some((item) => item.includes("QA evidence"))) {
      throw new Error("QA evidence inclusion was not rejected");
    }
    if (!findForbiddenPackageIncludes("files:\n  - C:\\Users\\local-user\\build\\dist\n").some((item) => item.includes("absolute local path"))) {
      throw new Error("absolute package path was not rejected");
    }

    const broadWorkerConfig = "extraResources:\n  - from: ../browser-worker\n    to: backend/backend-browser\n";
    if (!findBroadBrowserWorkerIncludes(broadWorkerConfig).some((item) => item.includes("beyond worker.py"))) {
      throw new Error("broad browser-worker include was not rejected");
    }
    const filteredWorkerConfig = "extraResources:\n  - from: ../browser-worker\n    to: backend/backend-browser\n    filter:\n      - worker.py\n";
    if (findBroadBrowserWorkerIncludes(filteredWorkerConfig).length !== 0) {
      throw new Error("worker.py-only browser-worker include was rejected");
    }
    console.log("Package privacy guard self-test passed.");
  } finally {
    fs.rmSync(temp, { recursive: true, force: true });
  }
}

function runGuard() {
  const configPath = path.join(repoRoot, "apps/desktop/electron-builder.yml");
  const findings = [
    ...scanPackagedRoots(repoRoot),
    ...findForbiddenPackageIncludes(fs.readFileSync(configPath, "utf8")),
    ...findBroadBrowserWorkerIncludes(fs.readFileSync(configPath, "utf8")),
  ];
  if (findings.length > 0) {
    console.error("Package privacy guard failed:");
    for (const finding of findings) console.error(`- ${finding}`);
    process.exitCode = 1;
    return;
  }
  console.log("Package privacy guard passed.");
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.argv.includes("--self-test")) runSelfTest();
  else runGuard();
}

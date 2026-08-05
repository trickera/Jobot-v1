// Drives a packaged Sencia Job .exe end-to-end through a genuinely isolated
// Windows profile (fresh APPDATA/LOCALAPPDATA/TEMP, PATH stripped to system
// dirs only) using the Chrome DevTools Protocol - the same technique used
// (and proven working, see qa-artifacts/clean-electron-smoke/clean-smoke.mjs)
// by prior sessions in this exact environment. Invoked by
// scripts/qa/clean-install-smoke.ps1; not meant to be run standalone outside
// that wrapper (it expects --exe/--reportDir).
//
// Covers the installer hardening plan's Phase 5 required behavior: launch,
// wait for health, install-health check (+ repair if needed), browser
// health, a small real search, offline (no-AI-key) ATS diagnosis, PDF/DOCX
// export, resume upload, close, and an orphan-process check.

import { spawn, execFileSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";

process.on("uncaughtException", (error) => {
  console.error("[clean-install-smoke] uncaughtException", error);
});
process.on("unhandledRejection", (error) => {
  console.error("[clean-install-smoke] unhandledRejection", error);
});

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i].startsWith("--")) {
      const key = argv[i].slice(2);
      const value = argv[i + 1] && !argv[i + 1].startsWith("--") ? argv[++i] : "true";
      out[key] = value;
    }
  }
  return out;
}

const args = parseArgs(process.argv.slice(2));
const appExe = args.exe;
const reportDir = args.reportDir ?? path.join(process.cwd(), "qa-artifacts", `clean-install-smoke-${new Date().toISOString().slice(0, 10)}`);
const label = args.label ?? "win-unpacked";
const debugPort = Number(args.debugPort ?? 9334);
const apiBase = "http://127.0.0.1:48730";

const canonical = {
  schemaVersion: 1,
  basics: {
    name: "Clean Install Candidate",
    headline: "Backend Engineer",
    email: "candidate@example.com",
    phone: "+55 11 99999-0000",
    location: "Brazil",
    links: [{ label: "Portfolio", url: "https://example.com/profile" }],
  },
  target: { jobTitle: "Backend Engineer", category: "Tech", seniority: "Pleno" },
  summary: "Backend engineer with Go, PostgreSQL and REST API experience.",
  skills: {
    hard: ["Go", "PostgreSQL", "REST", "Docker", "Linux"],
    soft: ["Communication", "Ownership"],
    tools: ["Git", "Grafana"],
  },
  experience: [
    {
      company: "Example Corp",
      role: "Backend Engineer",
      location: "Remote",
      start: "2022",
      end: "Present",
      bullets: ["Built REST APIs in Go.", "Operated PostgreSQL at scale."],
    },
  ],
  education: [{ institution: "Example University", degree: "Technology Degree", area: "Systems", start: "2018", end: "2021" }],
  projects: [],
  certifications: [],
  languages: [{ language: "English", fluency: "Professional" }],
};

const RESUME_RAW_TEXT = `Clean Install Candidate
candidate@example.com | +55 11 99999-0000 | Brazil

Summary
Backend engineer with Go, PostgreSQL and REST API experience.

Experience
Backend Engineer, Example Corp (2022-Present)
- Built REST APIs in Go.
- Operated PostgreSQL at scale.

Education
Technology Degree, Example University (2018-2021)

Skills
Go, PostgreSQL, REST, Docker, Linux, Git, Grafana`;

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function fetchText(url, options = {}) {
  const res = await fetch(url, options);
  const text = await res.text();
  return { res, text };
}

async function waitForURL(url, timeoutMs) {
  const started = Date.now();
  let lastError;
  while (Date.now() - started < timeoutMs) {
    try {
      const { res, text } = await fetchText(url);
      if (res.ok) return text;
      lastError = new Error(`HTTP ${res.status}: ${text}`);
    } catch (error) {
      lastError = error;
    }
    await wait(500);
  }
  throw lastError ?? new Error(`Timed out waiting for ${url}`);
}

async function getCDPPage() {
  const raw = await waitForURL(`http://127.0.0.1:${debugPort}/json`, 30000);
  const pages = JSON.parse(raw);
  const page = pages.find((item) => item.type === "page" && item.webSocketDebuggerUrl);
  if (!page) throw new Error("No Electron page found via CDP.");
  return page;
}

function connectCDP(wsURL) {
  const ws = new WebSocket(wsURL);
  let id = 0;
  const pending = new Map();
  ws.addEventListener("message", (event) => {
    const msg = JSON.parse(event.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) reject(new Error(JSON.stringify(msg.error)));
      else resolve(msg.result);
    }
  });
  // A command like "Browser.close" makes the remote Electron process exit
  // before it can ever send back an ack - without this, that call's promise
  // would hang forever (nothing else will ever resolve/reject it). Reject
  // every still-pending call the moment the socket closes for any reason.
  ws.addEventListener("close", () => {
    for (const { reject } of pending.values()) {
      reject(new Error("CDP websocket closed before a response arrived"));
    }
    pending.clear();
  });
  return new Promise((resolve, reject) => {
    ws.addEventListener("open", () => {
      resolve({
        send(method, params = {}, timeoutMs = 8000) {
          const callID = ++id;
          ws.send(JSON.stringify({ id: callID, method, params }));
          return new Promise((res, rej) => {
            const timer = setTimeout(() => {
              pending.delete(callID);
              rej(new Error(`CDP call ${method} timed out after ${timeoutMs}ms`));
            }, timeoutMs);
            pending.set(callID, {
              resolve: (value) => {
                clearTimeout(timer);
                res(value);
              },
              reject: (error) => {
                clearTimeout(timer);
                rej(error);
              },
            });
          });
        },
        close() {
          ws.close();
        },
      });
    });
    ws.addEventListener("error", () => reject(new Error("CDP websocket failed.")));
  });
}

async function evalExpr(cdp, expression, awaitPromise = true) {
  const result = await cdp.send("Runtime.evaluate", { expression, awaitPromise, returnByValue: true });
  if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
  return result.result.value;
}

async function apiJSON(token, route, options = {}) {
  const res = await fetch(`${apiBase}${route}`, {
    ...options,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", ...(options.headers ?? {}) },
  });
  const text = await res.text();
  let payload = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = { raw: text };
    }
  }
  if (!res.ok) {
    const err = new Error(payload?.message ?? text ?? `HTTP ${res.status}`);
    err.status = res.status;
    err.payload = payload;
    throw err;
  }
  return payload;
}

function listOrphanProcesses() {
  const names = ["sencia-job-backend", "python", "camoufox", "Sencia Job"];
  // execFileSync (no shell) so a name containing a space ("Sencia Job")
  // survives as one argv element instead of being re-split by an
  // intermediate cmd.exe layer - the earlier execSync-with-a-shell-string
  // version mis-parsed "Sencia Job" into two separate -Name arguments.
  const script = `Get-Process -Name ${names.map((n) => `'${n}'`).join(",")} -ErrorAction SilentlyContinue | Select-Object Name,Id | ConvertTo-Json -Compress`;
  // Windows PowerShell 5.1 sets its own process exit code to 1 whenever ANY
  // non-terminating error was recorded during the script - which happens
  // here for every name in -Name that matches zero processes, even though
  // -ErrorAction SilentlyContinue already suppressed the error text and the
  // OTHER names' real matches are sitting right there in stdout. So
  // execFileSync throwing here does not mean "no output" - read
  // error.stdout instead of discarding it.
  let out;
  try {
    out = execFileSync("powershell", ["-NoProfile", "-Command", script], { encoding: "utf8" });
  } catch (error) {
    out = error.stdout ?? "";
  }
  out = out.trim();
  if (!out) return [];
  try {
    const parsed = JSON.parse(out);
    return Array.isArray(parsed) ? parsed : [parsed];
  } catch {
    return [];
  }
}

function newProcessesSince(baseline, current) {
  const baselineIds = new Set(baseline.map((processInfo) => Number(processInfo.Id)));
  return current.filter((processInfo) => !baselineIds.has(Number(processInfo.Id)));
}

function runProcessBaselineSelfTest() {
  const baseline = [{ Name: "python", Id: 100136 }];
  const current = [
    { Name: "python", Id: 100136 },
    { Name: "sencia-job-backend", Id: 200001 },
    { Name: "python", Id: 200002 },
  ];
  const orphans = newProcessesSince(baseline, current);
  if (orphans.length !== 2 || orphans.some((item) => Number(item.Id) === 100136)) {
    throw new Error(`Process baseline self-test failed: ${JSON.stringify(orphans)}`);
  }
  if (newProcessesSince(baseline, baseline).length !== 0) {
    throw new Error("Process baseline self-test retained a preexisting process");
  }
  console.log("[clean-install-smoke] process baseline self-test passed");
}

async function main() {
  console.log(`[clean-install-smoke] starting (${label})`);
  if (!appExe || !existsSync(appExe)) throw new Error(`Packaged app not found: ${appExe}`);
  await mkdir(reportDir, { recursive: true });

  const tempRoot = path.join(reportDir, `profile-${label}-${Date.now()}`);
  const appData = path.join(tempRoot, "AppData", "Roaming");
  const localAppData = path.join(tempRoot, "AppData", "Local");
  const tempDir = path.join(tempRoot, "Temp");
  await mkdir(appData, { recursive: true });
  await mkdir(localAppData, { recursive: true });
  await mkdir(tempDir, { recursive: true });

  // Stripped to Windows system dirs only - no Node/Python/Go/repo paths, so
  // any resolution that accidentally depends on the dev PATH fails loudly
  // instead of silently working by coincidence (installer hardening's core
  // premise).
  const cleanEnv = {
    SystemRoot: process.env.SystemRoot,
    WINDIR: process.env.WINDIR,
    COMSPEC: process.env.COMSPEC,
    APPDATA: appData,
    LOCALAPPDATA: localAppData,
    TEMP: tempDir,
    TMP: tempDir,
    USERPROFILE: tempRoot,
    PATH: `${process.env.SystemRoot}\\System32;${process.env.SystemRoot};${process.env.SystemRoot}\\System32\\Wbem`,
  };

  const report = {
    label,
    startedAt: new Date().toISOString(),
    appExe,
    tempRoot,
    verdict: "UNKNOWN",
    checks: {},
    errors: [],
  };
  const preexistingProcesses = listOrphanProcesses();
  report.checks.preexistingProcesses = preexistingProcesses;

  console.log("[clean-install-smoke] launching", appExe);
  const child = spawn(appExe, [`--remote-debugging-port=${debugPort}`], {
    env: cleanEnv,
    detached: false,
    stdio: "ignore",
    windowsHide: true,
  });
  child.on("error", (error) => console.error("[clean-install-smoke] child spawn error", error));
  child.on("exit", (code, signal) => console.log("[clean-install-smoke] child exit", { code, signal }));

  let cdp;
  try {
    console.log("[clean-install-smoke] waiting for backend health");
    await waitForURL(`${apiBase}/health`, 45000);
    report.checks.backendHealth = "ok";

    console.log("[clean-install-smoke] connecting CDP");
    const page = await getCDPPage();
    cdp = await connectCDP(page.webSocketDebuggerUrl);
    await cdp.send("Runtime.enable");
    report.checks.documentTitle = await evalExpr(cdp, "document.title");
    const token = await evalExpr(cdp, "window.senciaElectron.getApiToken()");
    report.checks.apiTokenLength = token.length;

    console.log("[clean-install-smoke] install health");
    let installHealth = await apiJSON(token, "/api/v1/health/install");
    report.checks.installHealthBefore = installHealth;
    if (!installHealth.ok) {
      console.log("[clean-install-smoke] install health not ok, running repair");
      report.checks.repair = await apiJSON(token, "/api/v1/health/repair", { method: "POST", body: "{}" });
      installHealth = await apiJSON(token, "/api/v1/health/install");
      report.checks.installHealthAfterRepair = installHealth;
    }

    console.log("[clean-install-smoke] browser health");
    let browserHealth = await apiJSON(token, "/api/v1/browser/health");
    report.checks.browserHealth = browserHealth;

    if (!browserHealth.browserInstalled) {
      console.log("[clean-install-smoke] browser not installed after repair - falling back to bootstrap download");
      try {
        await apiJSON(token, "/api/v1/browser/bootstrap", { method: "POST", body: "{}" });
      } catch (error) {
        if (error.status !== 409) throw error;
      }
      const started = Date.now();
      let status = null;
      while (Date.now() - started < 10 * 60 * 1000) {
        status = await apiJSON(token, "/api/v1/browser/bootstrap/status");
        if (status.done) break;
        await wait(3000);
      }
      report.checks.browserBootstrap = status;
      browserHealth = await apiJSON(token, "/api/v1/browser/health");
      report.checks.browserHealthAfterBootstrap = browserHealth;
    }

    console.log("[clean-install-smoke] offline ATS diagnosis (no AI key, no prior parse)");
    const diagnose = await apiJSON(token, "/api/v1/resume/diagnose", {
      method: "POST",
      body: JSON.stringify({ rawText: RESUME_RAW_TEXT }),
    });
    report.checks.offlineDiagnose = {
      heuristic: diagnose.heuristic,
      scores: diagnose.scores,
      issueCount: Array.isArray(diagnose.issues) ? diagnose.issues.length : null,
    };

    console.log("[clean-install-smoke] configure short search");
    const config = await apiJSON(token, "/api/v1/config");
    const nextConfig = {
      ...config,
      form: {
        ...config.form,
        source: "LinkedIn",
        roles: "Backend",
        role: "Backend",
        levels: "Pleno",
        seniority: "Pleno",
        excludedLevels: "Senior, Sr, Lead, Staff, Principal",
        searchProfiles: "Backend | Pleno | Senior, Sr, Lead, Staff, Principal | 8",
        location: "Remoto",
        workMode: "remote",
        remoteCountry: "Brazil",
        onsiteLocation: "",
        keywords: "Go, PostgreSQL, REST, Docker, Linux",
        recentHours: 24,
        maxJobs: 1,
        linkedinPages: 1,
        scoreCut: 40,
        maxDelaySeconds: 1,
      },
      toggles: {
        ...config.toggles,
        useLinkedin: true,
        useIndeed: false,
        useGupy: false,
        compatibility: false,
        score: true,
        headless: true,
        remoteOnly: true,
        saveHistory: true,
      },
    };
    await apiJSON(token, "/api/v1/config", { method: "PUT", body: JSON.stringify(nextConfig) });
    await apiJSON(token, "/api/v1/search", { method: "POST", body: "{}" });
    let searchStatus = null;
    const searchStarted = Date.now();
    while (Date.now() - searchStarted < 4 * 60 * 1000) {
      searchStatus = await apiJSON(token, "/api/v1/search/status");
      if (!searchStatus.running) break;
      await wait(3000);
    }
    report.checks.searchStatus = {
      running: searchStatus?.running,
      total: searchStatus?.total,
      jobs: Array.isArray(searchStatus?.jobs) ? searchStatus.jobs.length : null,
      message: searchStatus?.message,
      error: searchStatus?.error,
    };

    console.log("[clean-install-smoke] export pdf");
    const pdfExport = await apiJSON(token, "/api/v1/resume/export", {
      method: "POST",
      body: JSON.stringify({ canonical, format: "pdf", templateId: "template:ats-clean", pageSize: "letter" }),
    });
    const pdfPath = path.join(reportDir, `${label}-export.pdf`);
    await writeFile(pdfPath, Buffer.from(pdfExport.content, "base64"));
    report.checks.pdfExport = { fileName: pdfExport.fileName, bytes: Buffer.from(pdfExport.content, "base64").length, path: pdfPath };

    console.log("[clean-install-smoke] export docx");
    const docxExport = await apiJSON(token, "/api/v1/resume/export", {
      method: "POST",
      body: JSON.stringify({ canonical, format: "docx", templateId: "template:ats-clean", pageSize: "letter" }),
    });
    const docxPath = path.join(reportDir, `${label}-export.docx`);
    await writeFile(docxPath, Buffer.from(docxExport.content, "base64"));
    report.checks.docxExport = { fileName: docxExport.fileName, bytes: Buffer.from(docxExport.content, "base64").length, path: docxPath };

    console.log("[clean-install-smoke] upload generated pdf");
    const upload = await apiJSON(token, "/api/v1/resume", {
      method: "POST",
      body: JSON.stringify({ fileName: `${label}-export.pdf`, mimeType: "application/pdf", contentBase64: pdfExport.content }),
    });
    report.checks.pdfUpload = {
      fileName: upload.fileName,
      extractedChars: String(upload.extractedText ?? "").length,
      warnings: upload.warnings ?? [],
    };

    report.finishedAt = new Date().toISOString();
  } catch (error) {
    report.errors.push({ message: error.message, status: error.status, payload: error.payload });
    report.finishedAt = new Date().toISOString();
  } finally {
    // CDP's "Browser.close" makes the remote Electron process exit, which
    // tears down the WebSocket without ever sending an ack for that pending
    // command. With no other active handle left, Node considers the event
    // loop empty and terminates the process mid-await - abandoning the rest
    // of this finally block (including writing the report!) before it ever
    // runs. A keep-alive timer holds the event loop open until the whole
    // cleanup+report sequence below explicitly finishes.
    const keepAlive = setInterval(() => {}, 1000);
    try {
      if (cdp) {
        try {
          // Routes through Electron's real quit flow (window-all-closed ->
          // shutdown() -> stopBackend()'s taskkill /T /F), the same path a
          // user closing the window takes - exercises Phase 4's lifecycle
          // hardening for real instead of just killing the outer process.
          await cdp.send("Browser.close");
        } catch {
          // ignore - closing may race the process actually exiting
        }
        cdp.close();
      }
      await wait(4000);
      if (!child.killed) {
        try {
          execFileSync("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore" });
        } catch {
          // already gone
        }
      }
      await wait(1500);
      const orphans = newProcessesSince(preexistingProcesses, listOrphanProcesses());
      report.checks.orphanProcesses = orphans;
      report.orphanFree = orphans.length === 0;

      const criticalOK =
        report.checks.backendHealth === "ok" &&
        (report.checks.installHealthAfterRepair ?? report.checks.installHealthBefore)?.ok !== false &&
        report.checks.searchStatus &&
        !report.checks.searchStatus.error &&
        report.checks.pdfExport?.bytes > 0 &&
        report.checks.docxExport?.bytes > 0 &&
        report.orphanFree;

      report.verdict = report.errors.length === 0 && criticalOK ? "PASS" : "FAIL";

      try {
        await writeFile(path.join(reportDir, `${label}-report.json`), JSON.stringify(report, null, 2));
        const md = renderMarkdown(report);
        await writeFile(path.join(reportDir, `${label}-report.md`), md);
        console.log(`[clean-install-smoke] reports written to ${reportDir}`);
      } catch (error) {
        console.error("[clean-install-smoke] failed to write report", error);
      }
      console.log(`[clean-install-smoke] verdict: ${report.verdict}`);
      if (report.verdict !== "PASS") process.exitCode = 1;
    } finally {
      clearInterval(keepAlive);
    }
  }
}

function renderMarkdown(report) {
  const lines = [];
  lines.push(`# Clean Install Smoke - ${report.label}`);
  lines.push("");
  lines.push(`- Verdict: **${report.verdict}**`);
  lines.push(`- App: \`${report.appExe}\``);
  lines.push(`- Started: ${report.startedAt}`);
  lines.push(`- Finished: ${report.finishedAt ?? "n/a"}`);
  lines.push(`- Orphan processes after close: ${report.orphanFree ? "none" : JSON.stringify(report.checks.orphanProcesses)}`);
  lines.push("");
  lines.push("## Checks");
  lines.push("");
  for (const [key, value] of Object.entries(report.checks)) {
    lines.push(`### ${key}`);
    lines.push("");
    lines.push("```json");
    lines.push(JSON.stringify(value, null, 2));
    lines.push("```");
    lines.push("");
  }
  if (report.errors.length > 0) {
    lines.push("## Errors");
    lines.push("");
    for (const error of report.errors) {
      lines.push(`- ${error.message}${error.status ? ` (status ${error.status})` : ""}`);
    }
  }
  return lines.join("\n");
}

if (args.selfTestProcessBaseline === "true") {
  runProcessBaselineSelfTest();
} else main().catch(async (error) => {
  console.error(error);
  try {
    await mkdir(reportDir, { recursive: true });
    await writeFile(
      path.join(reportDir, `${label}-report.json`),
      JSON.stringify({ label, verdict: "FAIL", fatal: error.message, stack: error.stack }, null, 2),
    );
  } catch {
    // best-effort
  }
  process.exit(1);
});

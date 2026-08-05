import { chromium } from "playwright-core";
import fs from "node:fs";
import path from "node:path";

const url = process.env.SENCIA_VISUAL_URL ?? "http://127.0.0.1:1420/";
const output = path.resolve(process.env.SENCIA_VISUAL_OUT ?? "qa-artifacts/precision-theme-2026-07-16");
const chrome = "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe";
const viewports = [[1280, 800], [980, 640]];
const routes = [
  ["search", "Buscar vagas"],
  ["saved", "Vagas salvas"],
  ["applications", "Candidaturas"],
  ["history", "Historico"],
  ["resume", "Resume Studio"],
  ["logs", "Logs"],
  ["settings", "Configuracoes"],
];

fs.mkdirSync(output, { recursive: true });
const browser = await chromium.launch({ executablePath: chrome, headless: true });
const report = { url, screens: [], consoleErrors: [], failures: [] };

async function pageMetrics(page) {
  return page.evaluate(() => {
    const workspace = document.querySelector(".workspace");
    const titlebar = document.querySelector(".titlebar");
    const brand = document.querySelector(".brand");
    return {
      theme: document.documentElement.dataset.theme,
      documentOverflow: document.documentElement.scrollWidth > window.innerWidth,
      workspaceOverflow: workspace ? workspace.scrollWidth > workspace.clientWidth : false,
      brand: brand?.textContent?.trim() ?? "",
      brandColor: brand ? getComputedStyle(brand).color : "",
      titlebarBackground: titlebar ? getComputedStyle(titlebar).backgroundColor : "",
      visibleReadyText: [...document.querySelectorAll(".titlebar *")].some((node) => node.textContent?.trim() === "Pronto"),
    };
  });
}

try {
  for (const theme of ["dark", "light"]) {
    for (const [width, height] of viewports) {
      const context = await browser.newContext({ viewport: { width, height }, reducedMotion: "reduce" });
      await context.addInitScript((value) => localStorage.setItem("sencia-theme", value), theme);
      const page = await context.newPage();
      page.on("console", (message) => {
        if (message.type() === "error") report.consoleErrors.push(`${theme}/${width}: ${message.text()}`);
      });
      page.on("pageerror", (error) => report.consoleErrors.push(`${theme}/${width}: ${error.message}`));
      page.on("requestfailed", (request) => report.failures.push(`${theme}/${width}: ${request.url()} ${request.failure()?.errorText ?? "failed"}`));

      await page.goto(url, { waitUntil: "networkidle" });
      const initialTheme = await page.locator("html").getAttribute("data-theme");
      if (initialTheme !== theme) throw new Error(`Expected ${theme} theme, received ${initialTheme}`);
      if ((await page.getByText("JoBot", { exact: true }).count()) !== 1) throw new Error("Renderer brand is missing");
      if ((await page.locator('.titlebar [role="status"]').count()) !== 1) throw new Error("Readiness dot is missing");

      for (const [slug, label] of routes) {
        await page.locator(".sidebar").getByRole("button", { name: label, exact: true }).click();
        // Chromium can otherwise capture stale compositor tiles for draggable
        // titlebar/backdrop layers while reduced motion is active.
        await page.waitForTimeout(450);
        const metrics = await pageMetrics(page);
        const file = path.join(output, `${theme}-${width}x${height}-${slug}.png`);
        await page.screenshot({ path: file });
        report.screens.push({ theme, width, height, slug, file, ...metrics });
      }

      for (const setting of ["ai", "sources", "profile"]) {
        await page.locator(`[data-setting-id="${setting}"]`).click();
        await page.waitForTimeout(450);
        const metrics = await pageMetrics(page);
        const file = path.join(output, `${theme}-${width}x${height}-settings-${setting}.png`);
        await page.screenshot({ path: file });
        report.screens.push({ theme, width, height, slug: `settings-${setting}`, file, ...metrics });
      }

      if (theme === "dark" && width === 1280) {
        await page.getByRole("button", { name: "Usar tema claro" }).click();
        await page.waitForTimeout(240);
        if ((await page.locator("html").getAttribute("data-theme")) !== "light") throw new Error("Theme toggle did not switch to light");
      }

      await context.close();
    }
  }
} finally {
  await browser.close();
}

report.passed =
  report.consoleErrors.length === 0 &&
  report.failures.length === 0 &&
  report.screens.every((screen) => screen.documentOverflow !== true && screen.workspaceOverflow !== true && screen.visibleReadyText !== true);
fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
console.log(JSON.stringify({ passed: report.passed, screens: report.screens.length, consoleErrors: report.consoleErrors, failures: report.failures }, null, 2));
process.exitCode = report.passed ? 0 : 1;

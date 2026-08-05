"""Browser smoke for the real Precision renderer against the local mock API.

The script is intentionally read-only: it navigates through the existing UI,
captures screenshots and records layout/computed-style evidence without saving
configuration or triggering job actions.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from playwright.sync_api import Page, sync_playwright


CHROME = Path(r"C:\Program Files\Google\Chrome\Application\chrome.exe")
VIEWPORTS = ((1280, 800), (980, 640))
ROUTES = (
    ("search", "Buscar vagas"),
    ("saved", "Vagas salvas"),
    ("applications", "Candidaturas"),
    ("history", "Historico"),
    ("resume", "Resume Studio"),
    ("logs", "Logs"),
    ("settings", "Configuracoes"),
)


def snapshot(page: Page, output: Path, slug: str, width: int, height: int) -> dict:
    # Let Chromium commit the reduced-motion layer tree before capturing; very
    # short waits can produce partial titlebar frames in headless screenshots.
    page.wait_for_timeout(450)
    workspace = page.locator(".workspace").first
    metrics = page.evaluate(
        """() => {
          const workspace = document.querySelector('.workspace');
          const primary = document.querySelector('.primary-button');
          const input = document.querySelector('input[placeholder], textarea[placeholder]');
          const settings = document.querySelector('.settings-workspace');
          const logTime = document.querySelector('.precision-log-line time');
          const style = (node, pseudo) => node ? getComputedStyle(node, pseudo || null) : null;
          return {
            title: document.querySelector('h1')?.textContent?.trim() || '',
            documentWidth: document.documentElement.scrollWidth,
            viewportWidth: window.innerWidth,
            horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth,
            workspaceOverflow: workspace ? workspace.scrollWidth > workspace.clientWidth : null,
            workspaceScrollHeight: workspace?.scrollHeight || 0,
            workspaceClientHeight: workspace?.clientHeight || 0,
            settingsPadding: style(settings)?.padding || null,
            settingsOverflowY: style(settings)?.overflowY || null,
            primaryColor: style(primary)?.color || null,
            primaryBackground: style(primary)?.backgroundColor || null,
            placeholderColor: style(input, '::placeholder')?.color || null,
            logTimeColor: style(logTime)?.color || null,
          };
        }"""
    )
    screenshot = output / f"{width}x{height}-{slug}.png"
    page.screenshot(path=str(screenshot), full_page=False)
    metrics["screenshot"] = str(screenshot)
    return metrics


def click_unique(page: Page, name: str) -> None:
    button = page.locator(".sidebar").get_by_role("button", name=name, exact=True)
    if button.count() != 1:
        raise RuntimeError(f"Expected one button named {name!r}, found {button.count()}")
    button.click()


def run(url: str, output: Path) -> dict:
    output.mkdir(parents=True, exist_ok=True)
    report: dict = {"url": url, "viewports": [], "consoleErrors": [], "httpFailures": []}

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(executable_path=str(CHROME), headless=True)
        for width, height in VIEWPORTS:
            context = browser.new_context(
                viewport={"width": width, "height": height},
                reduced_motion="reduce",
            )
            page = context.new_page()
            errors: list[str] = []
            http_failures: list[dict] = []
            page.on("console", lambda message: errors.append(message.text) if message.type == "error" else None)
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.on(
                "response",
                lambda response: http_failures.append({"status": response.status, "url": response.url})
                if response.status >= 400
                else None,
            )
            page.goto(url, wait_until="networkidle")
            page.add_style_tag(
                content="@media (prefers-reduced-motion: reduce) { *, *::before, *::after { animation: none !important; transition: none !important; } }"
            )

            viewport_result = {"size": [width, height], "screens": {}}
            for slug, name in ROUTES:
                click_unique(page, name)
                viewport_result["screens"][slug] = snapshot(page, output, slug, width, height)

            for setting in ("ai", "profile", "privacy"):
                target = page.locator(f'[data-setting-id="{setting}"]')
                if target.count() != 1:
                    raise RuntimeError(f"Expected one Settings item {setting!r}, found {target.count()}")
                target.click()
                viewport_result["screens"][f"settings-{setting}"] = snapshot(
                    page, output, f"settings-{setting}", width, height
                )

            page.keyboard.press("Tab")
            viewport_result["focusAfterTab"] = page.evaluate(
                "() => ({tag: document.activeElement?.tagName || '', name: document.activeElement?.getAttribute('aria-label') || document.activeElement?.textContent?.trim() || ''})"
            )
            favicon_only = bool(http_failures) and all(item["url"].endswith("/favicon.ico") for item in http_failures)
            if favicon_only:
                errors = [error for error in errors if "status of 404" not in error]
            report["consoleErrors"].extend(errors)
            report["httpFailures"].extend(http_failures)
            report["viewports"].append(viewport_result)
            context.close()
        browser.close()

    report["passed"] = not report["consoleErrors"] and all(
        not screen["horizontalOverflow"] and screen["workspaceOverflow"] is not True
        for viewport in report["viewports"]
        for screen in viewport["screens"].values()
    )
    (output / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    return report


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://127.0.0.1:1420/")
    parser.add_argument("--out", default="qa-artifacts/precision-all-sections-2026-07-15")
    args = parser.parse_args()
    report = run(args.url, Path(args.out).resolve())
    print(json.dumps({"passed": report["passed"], "consoleErrors": report["consoleErrors"]}, ensure_ascii=False))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

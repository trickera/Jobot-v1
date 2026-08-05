import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const globalCSS = readFileSync(path.join(process.cwd(), "src/styles/global.css"), "utf8");
const tokensCSS = readFileSync(path.join(process.cwd(), "src/styles/tokens.css"), "utf8");

const noviceGuidanceSelectors = [
  ".search-workspace .workspace-header p",
  ".search-plan-grid dt",
  ".search-plan-grid small",
  ".search-workspace .empty-state p",
  ".settings-page-head p",
  ".settings-nav-item small",
  ".settings-field > span",
];

function cssRule(selector: string) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return globalCSS.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`))?.[1] ?? "";
}

function token(name: string) {
  return tokensCSS.match(new RegExp(`${name}:\\s*(#[0-9a-f]{6})`, "i"))?.[1] ?? "";
}

function contrastRatio(foreground: string, background: string) {
  const luminance = (hex: string) => {
    const channels = hex.match(/[0-9a-f]{2}/gi)?.map((value) => Number.parseInt(value, 16) / 255) ?? [];
    const [red = 0, green = 0, blue = 0] = channels.map((value) =>
      value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4,
    );
    return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
  };
  const [lighter, darker] = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
  return (lighter + 0.05) / (darker + 0.05);
}

describe("novice guidance contrast", () => {
  it("uses the readable muted token only in the measured Search and Settings selectors", () => {
    for (const selector of noviceGuidanceSelectors) {
      expect(cssRule(selector), selector).toContain("color: var(--muted)");
    }
  });

  it("keeps the muted token above 4.5:1 on the app surface", () => {
    expect(contrastRatio(token("--muted"), token("--surface"))).toBeGreaterThanOrEqual(4.5);
  });
});

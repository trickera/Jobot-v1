import type { JobRequirements } from "../../types";

// Mirror the backend's seeded template ids (resume_store.go).
export const ATS_CLEAN_TEMPLATE_ID = "template:ats-clean";
export const MODERN_ACCENT_TEMPLATE_ID = "template:modern-accent";

export type TemplateRecommendation = { templateId: string; reason: string };

// Deterministic, offline heuristic (brainstorm §7.7): AI is never required
// to get a recommendation. Creative/marketing/design roles lean visual;
// everything else — including "no job analyzed yet" — defaults to a clean
// ATS-safe layout (never ATS Strict, which stays the plain fallback).
const CREATIVE_CATEGORY_HINTS = [
  "design",
  "marketing",
  "creative",
  "criativo",
  "publicidade",
  "advertising",
  "brand",
  "content",
  "conteúdo",
  "ux",
  "ui",
];

export function recommendTemplate(requirements: JobRequirements | null): TemplateRecommendation {
  if (!requirements) {
    return { templateId: ATS_CLEAN_TEMPLATE_ID, reason: "No job analyzed yet — a clean ATS-safe default." };
  }
  const haystack = `${requirements.category ?? ""} ${requirements.jobTitle ?? ""}`.toLowerCase();
  if (CREATIVE_CATEGORY_HINTS.some((hint) => haystack.includes(hint))) {
    return {
      templateId: MODERN_ACCENT_TEMPLATE_ID,
      reason: "Creative/marketing roles often favor a distinctive visual layout.",
    };
  }
  return { templateId: ATS_CLEAN_TEMPLATE_ID, reason: "A clean, ATS-safe layout fits most roles." };
}

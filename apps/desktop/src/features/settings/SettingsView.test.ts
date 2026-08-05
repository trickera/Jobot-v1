import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { SettingsForm } from "../../types";
import {
  modelAfterFetch,
  formAfterResumeUpload,
  formWithTargetRole,
  formWithTargetSeniority,
  normalizeLoadedForm,
  providerDefaultModel,
  SETTINGS_KEYWORD_EXAMPLE,
  SETTINGS_PROFILE_EXAMPLES,
  SETTINGS_ROLE_EXAMPLE,
  visibleFetchedModels,
  SettingsView,
} from "./SettingsView";

describe("Resume-driven search configuration", () => {
  it("replaces stale resume-derived fields and preserves manual search preferences", () => {
    const current = normalizeLoadedForm({
      provider: "Gemini",
      model: providerDefaultModel("Gemini"),
      fallback1Provider: "",
      fallback1Model: "",
      fallback2Provider: "",
      fallback2Model: "",
      role: "Registered Nurse",
      roles: "Registered Nurse, ICU Nurse",
      seniority: "Senior",
      levels: "Senior",
      searchProfiles: "Registered Nurse | Senior",
      keywords: "patient, epic, icu",
      keywordsForRoles: "Registered Nurse",
      workMode: "hybrid",
      onsiteLocation: "Chicago",
      remoteCountry: "Brazil",
      blacklistCompanies: "Outlier",
    } as SettingsForm);
    const next = formAfterResumeUpload(current, {
      fileName: "Rafael_Moreira-DevOps.pdf",
      storedPath: "C:/resume.pdf",
      markdownPath: "C:/resume.md",
      markdown: "# Curriculo",
      extractedText: "Analista DevOps",
      keywords: ["AWS", "Terraform", "Kubernetes"],
      detectedRole: "Analista DevOps",
      detectedSeniority: "",
      detectedLevels: "Junior, Jr, Pleno, Senior, Sr, Especialista",
    });

    expect(next).toMatchObject({
      role: "Analista DevOps",
      roles: "Analista DevOps",
      seniority: "",
      searchProfiles: "",
      keywords: "AWS, Terraform, Kubernetes",
      keywordsForRoles: "Analista DevOps",
      workMode: "hybrid",
      onsiteLocation: "Chicago",
      remoteCountry: "Brazil",
      blacklistCompanies: "Outlier",
    });
  });

  it("keeps the visible role and seniority controls synchronized with effective fields", () => {
    const current = normalizeLoadedForm({
      provider: "Gemini",
      model: providerDefaultModel("Gemini"),
      fallback1Provider: "",
      fallback1Model: "",
      fallback2Provider: "",
      fallback2Model: "",
      roles: "Registered Nurse",
      levels: "Senior",
    } as SettingsForm);
    const role = formWithTargetRole(current, "Product Designer");
    const seniority = formWithTargetSeniority(role, "Pleno");

    expect(seniority.role).toBe("Product Designer");
    expect(seniority.roles).toBe("Product Designer");
    expect(seniority.seniority).toBe("Pleno");
    expect(seniority.levels).toBe("Pleno, Mid-Level");

    const manager = formWithTargetSeniority(role, "Manager");
    expect(manager.seniority).toBe("Manager");
    expect(manager.levels).toBe("Manager, Gerente, Coordenador");
  });
});

describe("Settings model normalization", () => {
  it("migrates retired Gemini models in the primary and fallback slots", () => {
    const normalized = normalizeLoadedForm({
      provider: "Gemini",
      model: "gemini-2.5-flash",
      fallback1Provider: "Gemini",
      fallback1Model: "gemini-2.5-flash-lite",
      fallback2Provider: "Gemini",
      fallback2Model: "gemini-2.5-flash",
    } as SettingsForm);

    expect(normalized.model).toBe(providerDefaultModel("Gemini"));
    expect(normalized.fallback1Model).toBe(providerDefaultModel("Gemini"));
    expect(normalized.fallback2Model).toBe(providerDefaultModel("Gemini"));
  });

  it("does not overwrite a compatible model without retirement evidence", () => {
    const normalized = normalizeLoadedForm({
      provider: "Gemini",
      model: "gemini-user-selected-stable",
      fallback1Provider: "",
      fallback1Model: "",
      fallback2Provider: "",
      fallback2Model: "",
    } as SettingsForm);

    expect(normalized.model).toBe("gemini-user-selected-stable");
  });

  it("uses the provider default instead of the first alphabetical fetched model", () => {
    const models = ["gemini-a-alphabetical", providerDefaultModel("Gemini")];
    expect(modelAfterFetch("Gemini", "gemini-retired", models)).toBe(providerDefaultModel("Gemini"));
  });

  it("migrates an unavailable pin only to an advertised stable Flash-Lite model", () => {
    expect(modelAfterFetch("Gemini", "gemini-missing", ["gemini-flash-latest", "gemini-flash-lite-latest"])).toBe(
      "gemini-flash-lite-latest",
    );
    expect(modelAfterFetch("Gemini", "gemini-missing", ["gemini-3-flash-preview", "gemini-flash-latest"])).toBe(
      "gemini-missing",
    );
  });

  it("defaults new AI sharing to explicit consent and economy mode", () => {
    const normalized = normalizeLoadedForm({ provider: "Gemini", model: "" } as SettingsForm);
    expect(normalized.aiMode).toBe("free_economy");
    expect(normalized.aiDataConsent).toBe(false);
  });

  it("keeps stable models and hides preview or experimental choices", () => {
    expect(
      visibleFetchedModels([
        providerDefaultModel("Gemini"),
        "gemini-3-flash-preview",
        "gemini-exp-1201",
        "gemini-stable-custom",
      ]),
    ).toEqual([providerDefaultModel("Gemini"), "gemini-stable-custom"]);
  });
});

describe("Settings examples", () => {
  it("represent multiple career domains without infrastructure-only defaults", () => {
    const examples = [SETTINGS_ROLE_EXAMPLE, SETTINGS_PROFILE_EXAMPLES, SETTINGS_KEYWORD_EXAMPLE].join("\n");

    expect(examples).toMatch(/Registered Nurse/);
    expect(examples).toMatch(/Financial Analyst/);
    expect(examples).toMatch(/Backend Engineer/);
    expect(examples).toMatch(/user research/);
    expect(examples).not.toMatch(/DevOps|Terraform|Kubernetes|CI\/CD/);
  });
});

describe("Settings entry section", () => {
  it("renders the search profile fields when opened from first-run setup", () => {
    const markup = renderToStaticMarkup(createElement(SettingsView, { initialSection: "profile" }));

    expect(markup).toContain("Perfil e curriculo");
    expect(markup).toContain("Cargo alvo");
    expect(markup).not.toContain("Chave Gemini");
  });

  it("keeps all nine real sections addressable in the Precision sidebar", () => {
    const markup = renderToStaticMarkup(createElement(SettingsView, { initialSection: "profile" }));

    for (const id of ["ai", "ai-usage", "sources", "profile", "pipeline", "privacy", "local", "install", "shortcuts"]) {
      expect(markup).toContain(`data-setting-id="${id}"`);
    }
    expect(markup).toMatch(/<button[^>]*aria-current="page"[^>]*data-setting-id="profile"/);
    expect(markup).toContain('aria-label="Perfil e curriculo"');
    expect(markup).toContain('title="Perfil e curriculo"');
  });

  it("preserves raw search profiles behind a collapsed advanced disclosure", () => {
    const markup = renderToStaticMarkup(createElement(SettingsView, { initialSection: "profile" }));

    expect(markup).toContain("Perfis de busca (avancado)");
    expect(markup).toContain("profiles-textarea");
    expect(markup).toContain("profile-profiles");
    expect(markup).not.toContain('profile-profiles" open');
  });

  it("does not pretend that local data was deleted without a backend operation", () => {
    const privacy = renderToStaticMarkup(createElement(SettingsView, { initialSection: "privacy" }));
    const local = renderToStaticMarkup(createElement(SettingsView, { initialSection: "local" }));

    expect(privacy).toContain("Limpeza indisponivel nesta tela");
    expect(privacy).toMatch(/<button[^>]*disabled=""[^>]*>.*Apagar dados/s);
    expect(local).toContain("Nenhum dado sera apagado por engano");
    expect(local).toMatch(/<button[^>]*disabled=""[^>]*>.*Limpar banco/s);
  });

  it("labels export readiness as a preference instead of pretending to create an export", () => {
    const markup = renderToStaticMarkup(createElement(SettingsView, { initialSection: "privacy" }));

    expect(markup).toContain("Marcar como pronta");
    expect(markup).not.toContain("Preparar export");
    expect(markup).toContain("Privacidade e armazenamento");
    expect(markup).not.toContain("Seus dados permanecem locais");
  });
});

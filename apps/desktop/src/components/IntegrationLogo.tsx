import type { CSSProperties } from "react";
import anthropicLogo from "../assets/logos/anthropic.png";
import arbeitnowLogo from "../assets/logos/arbeitnow.png";
import geminiLogo from "../assets/logos/gemini.svg";
import groqLogo from "../assets/logos/groq.svg";
import gupyLogo from "../assets/logos/gupy.png";
import indeedLogo from "../assets/logos/indeed-mark.png";
import jobicyLogo from "../assets/logos/jobicy.png";
import linkedinLogo from "../assets/logos/linkedin.png";
import ollamaLogo from "../assets/logos/ollama.png";
import openAILogo from "../assets/logos/openai.svg";
import openRouterDarkLogo from "../assets/logos/openrouter-dark.svg";
import openRouterLightLogo from "../assets/logos/openrouter-light.svg";
import remoteOKLogo from "../assets/logos/remoteok.png";
import remotiveLogo from "../assets/logos/remotive.ico";
import weWorkRemotelyLogo from "../assets/logos/weworkremotely.png";

export type IntegrationKey =
  | "linkedin"
  | "indeed"
  | "gupy"
  | "remotive"
  | "remoteok"
  | "jobicy"
  | "arbeitnow"
  | "weworkremotely"
  | "gemini"
  | "anthropic"
  | "openrouter"
  | "openai"
  | "groq"
  | "ollama"
  | "other";

type LogoAsset = { src: string; lightSrc?: string };

const LOGO_BY_KEY: Partial<Record<IntegrationKey, LogoAsset>> = {
  linkedin: { src: linkedinLogo },
  indeed: { src: indeedLogo },
  gupy: { src: gupyLogo },
  remotive: { src: remotiveLogo },
  remoteok: { src: remoteOKLogo },
  jobicy: { src: jobicyLogo },
  arbeitnow: { src: arbeitnowLogo },
  weworkremotely: { src: weWorkRemotelyLogo },
  gemini: { src: geminiLogo },
  anthropic: { src: anthropicLogo },
  openrouter: { src: openRouterDarkLogo, lightSrc: openRouterLightLogo },
  openai: { src: openAILogo },
  groq: { src: groqLogo },
  ollama: { src: ollamaLogo },
};

export function integrationKeyForName(name: string): IntegrationKey {
  const value = name.trim().toLowerCase();
  if (value.includes("linkedin")) return "linkedin";
  if (value.includes("indeed")) return "indeed";
  if (value.includes("gupy")) return "gupy";
  if (value.includes("we work remotely") || value.includes("weworkremotely")) return "weworkremotely";
  if (value.includes("remoteok") || value.includes("remote ok")) return "remoteok";
  if (value.includes("remotive")) return "remotive";
  if (value.includes("jobicy")) return "jobicy";
  if (value.includes("arbeitnow")) return "arbeitnow";
  if (value.includes("gemini")) return "gemini";
  if (value.includes("anthropic") || value.includes("claude")) return "anthropic";
  if (value.includes("openrouter")) return "openrouter";
  if (value.includes("openai")) return "openai";
  if (value.includes("groq")) return "groq";
  if (value.includes("ollama")) return "ollama";
  return "other";
}

export function integrationLabel(name: string): string {
  const labels: Partial<Record<IntegrationKey, string>> = {
    linkedin: "LinkedIn",
    indeed: "Indeed",
    gupy: "Gupy",
    remotive: "Remotive",
    remoteok: "RemoteOK",
    jobicy: "Jobicy",
    arbeitnow: "Arbeitnow",
    weworkremotely: "We Work Remotely",
    gemini: "Gemini",
    anthropic: "Anthropic",
    openrouter: "OpenRouter",
    openai: "OpenAI",
    groq: "Groq",
    ollama: "Ollama local",
  };
  return labels[integrationKeyForName(name)] ?? name.trim();
}

function fallbackMark(name: string) {
  const words = name.trim().split(/\s+/).filter(Boolean);
  return (words.length > 1 ? words.map((word) => word[0]).join("") : words[0] ?? "?").slice(0, 2).toUpperCase();
}

export function IntegrationLogo({ name, size = 18, className = "" }: { name: string; size?: number; className?: string }) {
  const key = integrationKeyForName(name);
  const asset = LOGO_BY_KEY[key];
  const style = { "--integration-logo-size": `${size}px` } as CSSProperties;

  return (
    <span className={`integration-logo integration-logo--${key} ${className}`.trim()} style={style} aria-hidden="true">
      {asset ? (
        <>
          <img className={`integration-logo-image${asset.lightSrc ? " is-dark-theme" : ""}`} src={asset.src} alt="" />
          {asset.lightSrc ? <img className="integration-logo-image is-light-theme" src={asset.lightSrc} alt="" /> : null}
        </>
      ) : (
        <span className="integration-logo-fallback">{fallbackMark(name)}</span>
      )}
    </span>
  );
}

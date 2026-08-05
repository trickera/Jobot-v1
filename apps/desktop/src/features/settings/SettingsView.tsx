import {
  BarChart3,
  Bot,
  Check,
  ChevronDown,
  Database,
  Download,
  FileText,
  Keyboard,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
  Upload,
  Wrench,
} from "lucide-react";
import type { ReactNode } from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import { IntegrationLogo } from "../../components/IntegrationLogo";
import { ApiError, fetchAIUsage, fetchModels, loadConfig, saveConfig, testProvider, uploadResume } from "../../services/api";
import type { AIUsageResponse, AppConfig, ResumeUploadResponse, SettingsForm, SettingsLocalItems, SettingsToggleKey } from "../../types";
import { BrowserWorkerStatus } from "./BrowserWorkerStatus";
import { InstallHealthPanel } from "./InstallHealthPanel";
import "./SettingsPrecision.css";

const providerOptions = ["Gemini", "Anthropic", "OpenRouter", "OpenAI", "Groq", "Ollama local"] as const;
const recentHourOptions = [1, 2, 6, 8, 12, 18, 24] as const;

const UPLOAD_ERROR_PT: Record<string, string> = {
  invalid_request: "Upload de currículo inválido.",
  invalid_file: "Arquivo de currículo inválido ou vazio.",
  unsupported_format: "Formato não suportado; use PDF, DOCX ou TXT.",
  file_too_large: "O currículo excede 8 MB.",
  internal_error: "Não foi possível salvar o currículo.",
};

const PROVIDER_STATUS_PT: Record<string, string> = {
  no_key: "Nenhuma chave configurada para este provedor. Adicione uma API key acima e clique em Salvar antes de testar.",
  unauthorized: "A chave foi recusada pelo provedor (401). Confira se ela esta ativa e tem permissao para listar modelos.",
  forbidden: "Acesso negado (403). Verifique as permissoes/regiao da chave.",
  model_not_found: "Modelo não encontrado — use Buscar modelos.",
  rate_limited: "O provedor limitou as requisicoes (429). Aguarde um pouco ou use outro fallback.",
  quota_exhausted: "A cota diária de IA desta chave acabou. Ela reseta amanhã — ou configure outro provider aqui.",
  provider_unavailable: "Provider instável (503) — tente de novo em minutos.",
  invalid_response: "Resposta inválida do provider.",
  local_model_unreachable: "Servidor local inacessível — o Ollama está rodando?",
  timeout: "O provedor demorou demais para responder. Use um modelo mais rápido ou tente de novo.",
  network_error: "Nao foi possivel conectar ao provedor agora. Tente novamente em alguns segundos.",
};

const MODEL_FETCH_ERROR_PT: Record<string, string> = {
  api_key_required: "Adicione uma API key para buscar modelos deste provedor.",
};

type ProviderStatus = { tone: "success" | "error"; text: string };

const settings = [
  { id: "ai", title: "Provedores de IA", description: "Chave, modelo e fallback de analise.", icon: Bot },
  { id: "ai-usage", title: "Uso de IA", description: "Orcamento, cache, tarefas e modelo ativo.", icon: BarChart3 },
  { id: "sources", title: "Fontes de vagas", description: "LinkedIn, Indeed, Gupy e data de postagem.", icon: Search },
  { id: "profile", title: "Perfil e curriculo", description: "Upload local, keywords, query e localizacao.", icon: FileText },
  { id: "pipeline", title: "Pipeline e radar", description: "Limites, intervalo, corte e varredura automatica.", icon: SlidersHorizontal },
  { id: "privacy", title: "Privacidade e dados", description: "Banco local, exportacao e limpeza.", icon: ShieldCheck },
  { id: "local", title: "Banco local", description: "Vagas, candidaturas e historico.", icon: Database },
  { id: "install", title: "Instalacao", description: "Diagnostico e reparo dos componentes locais.", icon: Wrench },
  { id: "shortcuts", title: "Atalhos", description: "Comandos globais realmente usados.", icon: Keyboard },
] as const;

export type SettingId = (typeof settings)[number]["id"];

// The product pin is validated against ListModels on first real use. If it is
// absent, the backend visibly migrates to the stable Flash-Lite alias instead of
// spending a request on a model name that the user's key cannot use.
const GEMINI_DEFAULT_MODEL = "gemini-3.1-flash-lite";
const GEMINI_LITE_ALIAS = "gemini-flash-lite-latest";
const RETIRED_MODELS: Readonly<Record<string, string>> = {
  "gemini-2.5-flash": GEMINI_DEFAULT_MODEL,
  "gemini-2.5-flash-lite": GEMINI_DEFAULT_MODEL,
};

const initialForm: SettingsForm = {
  source: "",
  provider: "Gemini",
  model: GEMINI_DEFAULT_MODEL,
  apiKey: "",
  fallback1Provider: "",
  fallback1Model: "",
  fallback2Provider: "",
  fallback2Model: "",
  aiMode: "free_economy",
  aiDataConsent: false,
  role: "",
  roles: "",
  seniority: "",
  levels: "Junior, Jr, Pleno, Senior, Sr, Especialista",
  excludedLevels: "Tech Lead, Lead, Staff, Principal, Manager, Coordenador, Gerente",
  searchProfiles: "",
  maxYears: 8,
  location: "Remoto",
  workMode: "remote",
  onsiteLocation: "",
  remoteCountry: "Brazil",
  resumeName: "",
  resumePath: "",
  resumeMarkdownPath: "",
  resumeText: "",
  keywords: "",
  keywordsForRoles: "",
  blacklistCompanies: "",
  recentHours: 24,
  maxJobs: 40,
  maxDelaySeconds: 15,
  radarIntervalMinutes: 20,
  notificationThreshold: 85,
  linkedinPages: 2,
  responseSize: "compacto",
  responseStyle: "objetivo",
  basePrompt: "Priorize compatibilidade tecnica, senioridade e modalidade.",
  shortcutSearch: "F8",
  shortcutAsk: "F9",
  shortcutNotes: "F4",
  scoreCut: 60,
  rankingMode: "compatibilidade",
};

const initialToggles: Record<SettingsToggleKey, boolean> = {
  remoteOnly: true,
  useLinkedin: true,
  useIndeed: true,
  useGupy: false,
  useRemotive: false,
  useRemoteok: false,
  useJobicy: false,
  useArbeitnow: false,
  useWeworkremotely: false,
  headless: true,
  compatibility: true,
  score: true,
  localOnly: true,
  exportReady: false,
  desktop: false,
  daily: false,
  saveHistory: true,
  autoClean: false,
  radarMode: false,
};

const initialLocalItems: SettingsLocalItems = { jobs: 0, saved: 0, applications: 0, history: 0 };

const initialConfig: AppConfig = {
  version: 2,
  form: initialForm,
  toggles: initialToggles,
  localItems: initialLocalItems,
  apiKeySet: false,
};

function ToggleRow({
  label,
  description,
  checked,
  onChange,
}: {
  label: string;
  description: string;
  checked: boolean;
  onChange: () => void;
}) {
  return (
    <button className="toggle-row" type="button" aria-pressed={checked} onClick={onChange}>
      <span>
        <strong>{label}</strong>
        <small>{description}</small>
      </span>
      <span className="switch" aria-hidden="true">
        <span />
      </span>
    </button>
  );
}

function Field({ label, children, className = "" }: { label: string; children: ReactNode; className?: string }) {
  return (
    <label className={`settings-field ${className}`.trim()}>
      <span>{label}</span>
      {children}
    </label>
  );
}

function StatusRow({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="status-row">
      <strong>{title}</strong>
      <span>{detail}</span>
    </div>
  );
}

function SectionIntro({
  title,
  description,
  aside,
  className = "",
}: {
  title: string;
  description: string;
  aside?: ReactNode;
  className?: string;
}) {
  return (
    <div className={`precision-section-intro ${className}`.trim()}>
      <div>
        <h3>{title}</h3>
        <p>{description}</p>
      </div>
      {aside}
    </div>
  );
}

function SettingsDisclosure({
  icon,
  title,
  summary,
  note,
  children,
  defaultOpen = false,
  className = "",
}: {
  icon: ReactNode;
  title: string;
  summary: string;
  note?: string;
  children: ReactNode;
  defaultOpen?: boolean;
  className?: string;
}) {
  return (
    <details className={`precision-disclosure ${className}`.trim()} open={defaultOpen || undefined}>
      <summary>
        <span className="precision-row-icon">{icon}</span>
        <span className="precision-row-copy">
          <strong>{title}</strong>
          <small>{summary}</small>
        </span>
        {note ? <span className="precision-row-note">{note}</span> : null}
        <ChevronDown className="precision-disclosure-chevron" size={15} />
      </summary>
      <div className="precision-disclosure-body">{children}</div>
    </details>
  );
}

function SourceToggleCard({
  source,
  label,
  description,
  checked,
  onChange,
}: {
  source: string;
  label: string;
  description: string;
  checked: boolean;
  onChange: () => void;
}) {
  return (
    <button className="precision-source-card" type="button" aria-pressed={checked} onClick={onChange}>
      <IntegrationLogo name={source} size={34} className="precision-source-mark" />
      <span className="precision-row-copy">
        <strong>{label}</strong>
        <small>{description}</small>
      </span>
      <span className="switch" aria-hidden="true"><span /></span>
    </button>
  );
}

export function providerDefaultModel(provider: string) {
  const key = provider.toLowerCase();
  if (key.includes("gemini")) return GEMINI_DEFAULT_MODEL;
  if (key.includes("anthropic")) return "claude-sonnet-4-5";
  if (key.includes("openrouter")) return "openai/gpt-4.1-mini";
  if (key.includes("groq")) return "llama-3.3-70b-versatile";
  if (key.includes("ollama")) return "llama3.1";
  return "gpt-4.1-mini";
}

function replaceRetiredModel(provider: string, model: string) {
  if (!provider.toLowerCase().includes("gemini")) return model;
  return RETIRED_MODELS[model.trim().toLowerCase()] ?? model;
}

function isModelCompatible(provider: string, model: string) {
  const providerKey = provider.toLowerCase();
  const modelKey = model.toLowerCase();
  if (!modelKey) return false;
  if (providerKey.includes("gemini")) return modelKey.startsWith("gemini");
  if (providerKey.includes("anthropic")) return modelKey.startsWith("claude");
  if (providerKey.includes("openrouter")) return modelKey.includes("/");
  return true;
}

export function normalizeLoadedForm(form: SettingsForm) {
  const provider = form.provider || initialForm.provider;
  const compatibleModel = isModelCompatible(provider, form.model) ? form.model : providerDefaultModel(provider);
  const model = replaceRetiredModel(provider, compatibleModel);
  const fallback1Provider = form.fallback1Provider || "";
  const compatibleFallback1Model = fallback1Provider
    ? isModelCompatible(fallback1Provider, form.fallback1Model)
      ? form.fallback1Model
      : providerDefaultModel(fallback1Provider)
    : "";
  const fallback1Model = replaceRetiredModel(fallback1Provider, compatibleFallback1Model);
  const fallback2Provider = form.fallback2Provider || "";
  const compatibleFallback2Model = fallback2Provider
    ? isModelCompatible(fallback2Provider, form.fallback2Model)
      ? form.fallback2Model
      : providerDefaultModel(fallback2Provider)
    : "";
  const fallback2Model = replaceRetiredModel(fallback2Provider, compatibleFallback2Model);
  return { ...initialForm, ...form, provider, model, fallback1Provider, fallback1Model, fallback2Provider, fallback2Model };
}

export function formAfterResumeUpload(form: SettingsForm, result: ResumeUploadResponse): SettingsForm {
  return {
    ...form,
    resumeName: result.fileName,
    resumePath: result.storedPath,
    resumeMarkdownPath: result.markdownPath,
    resumeText: result.markdown,
    role: result.detectedRole,
    roles: result.detectedRole,
    seniority: result.detectedSeniority,
    levels: result.detectedLevels,
    searchProfiles: "",
    keywords: result.keywords.join(", "),
    keywordsForRoles: result.detectedRole,
  };
}

export function formWithTargetRole(form: SettingsForm, role: string): SettingsForm {
  return { ...form, role, roles: role };
}

const levelsBySeniority: Record<string, string> = {
  Junior: "Junior, Jr, Júnior",
  Pleno: "Pleno, Mid-Level",
  Senior: "Senior, Sr, Sênior",
  Staff: "Staff",
  Lead: "Lead",
  Principal: "Principal",
  Manager: "Manager, Gerente, Coordenador",
  Especialista: "Especialista, Specialist",
  Estágio: "Estágio, Intern, Trainee",
};

export function formWithTargetSeniority(form: SettingsForm, seniority: string): SettingsForm {
  return { ...form, seniority, levels: levelsBySeniority[seniority] ?? form.levels };
}

function isPreviewModel(model: string) {
  return /(?:^|[-_.])(preview|experimental|exp)(?:$|[-_.])/i.test(model);
}

export function visibleFetchedModels(models: string[]) {
  return [...new Set(models)].filter((model) => !isPreviewModel(model));
}

export function modelAfterFetch(provider: string, current: string, models: string[]) {
  const visible = visibleFetchedModels(models);
  if (visible.includes(current)) return current;
  if (provider.toLowerCase().includes("gemini")) {
    return (
      [GEMINI_DEFAULT_MODEL, GEMINI_LITE_ALIAS].find((model) => visible.includes(model)) ??
      visible.find((model) => model.toLowerCase().includes("flash-lite")) ??
      current
    );
  }
  const providerDefault = providerDefaultModel(provider);
  return visible.includes(providerDefault) ? providerDefault : (visible[0] ?? providerDefault);
}

function csvItems(value: string) {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function booleanGroup(items: string[]) {
  if (items.length === 0) return "";
  return `(${items.map((item) => `"${item}"`).join(" OR ")})`;
}

export const SETTINGS_ROLE_EXAMPLE = "ex: Registered Nurse, Financial Analyst, Backend Engineer";
export const SETTINGS_PROFILE_EXAMPLES =
  "Registered Nurse, ICU Nurse | Staff, Senior | Director | 10\n" +
  "Financial Analyst, FP&A Analyst | Junior, Pleno, Senior | Director | 8\n" +
  "Backend Engineer, API Engineer | Junior, Pleno, Senior | Staff | 6";
export const SETTINGS_KEYWORD_EXAMPLE = "patient assessment, financial modeling, Go, user research...";

type ParsedProfile = {
  name: string;
  roles: string[];
  levels: string[];
  excluded: string[];
  maxYears: string;
};

function parseSearchProfiles(form: SettingsForm): ParsedProfile[] {
  const raw = (form.searchProfiles || "").trim();
  const globalLevels = csvItems(form.levels || form.seniority);
  const globalExcluded = csvItems(form.excludedLevels);
  const globalMaxYears = form.maxYears ? String(form.maxYears) : "";

  if (raw) {
    const profiles: ParsedProfile[] = [];
    for (const line of raw.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      const parts = trimmed.split("|").map((part) => part.trim());
      const roles = csvItems(parts[0] ?? "");
      if (roles.length === 0) continue;
      const levels = csvItems(parts[1] ?? "");
      const excluded = csvItems(parts[2] ?? "");
      profiles.push({
        name: roles[0],
        roles,
        levels: levels.length ? levels : globalLevels,
        excluded: excluded.length ? excluded : globalExcluded,
        maxYears: (parts[3] ?? "").trim() || globalMaxYears,
      });
    }
    if (profiles.length > 0) return profiles;
  }

  const roles = csvItems(form.roles || form.role);
  return [
    {
      name: roles[0] ?? "Global",
      roles,
      levels: globalLevels,
      excluded: globalExcluded,
      maxYears: globalMaxYears,
    },
  ];
}

function buildQueryPreview(form: SettingsForm) {
  const profiles = parseSearchProfiles(form);
  const withRoles = profiles.filter((profile) => profile.roles.length > 0);
  if (withRoles.length === 0) {
    return "Preencha cargos (roles) ou perfis de busca para gerar as queries.";
  }
  return withRoles
    .map((profile, index) => {
      const query = booleanGroup(profile.roles);
      const levels = profile.levels.length ? profile.levels.join(", ") : "(todas)";
      const years = profile.maxYears ? ` · max ${profile.maxYears} anos` : "";
      return `Busca ${index + 1} [${profile.name}]\n  query: ${query}\n  niveis aceitos: ${levels}${years}`;
    })
    .join("\n\n");
}

function fileToBase64(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("Nao foi possivel ler o arquivo."));
    reader.onload = () => {
      const result = String(reader.result ?? "");
      resolve(result.includes(",") ? result.split(",")[1] : result);
    };
    reader.readAsDataURL(file);
  });
}

export function SettingsView({ initialSection = "ai" }: { initialSection?: SettingId }) {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [activeId, setActiveId] = useState<SettingId>(initialSection);
  const [saved, setSaved] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [fetchingModels, setFetchingModels] = useState(false);
  const [testingProvider, setTestingProvider] = useState(false);
  const [uploadingResume, setUploadingResume] = useState(false);
  const [configError, setConfigError] = useState<string | null>(null);
  const [modelError, setModelError] = useState<string | null>(null);
  const [modelNotice, setModelNotice] = useState<string | null>(null);
  const [configNotices, setConfigNotices] = useState<string[]>([]);
  const [providerStatus, setProviderStatus] = useState<ProviderStatus | null>(null);
  const [resumeNotice, setResumeNotice] = useState<string | null>(null);
  const [apiKeySet, setApiKeySet] = useState(false);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [modelValidation, setModelValidation] = useState<AppConfig["modelValidation"]>();
  const [aiUsage, setAIUsage] = useState<AIUsageResponse | null>(null);
  const [aiUsageLoading, setAIUsageLoading] = useState(false);
  const [aiUsageError, setAIUsageError] = useState<string | null>(null);
  const [aiUsageRefresh, setAIUsageRefresh] = useState(0);
  const [form, setForm] = useState<SettingsForm>(initialForm);
  const [localItems, setLocalItems] = useState<SettingsLocalItems>(initialLocalItems);
  const [toggles, setToggles] = useState<Record<SettingsToggleKey, boolean>>(initialToggles);

  useEffect(() => {
    let mounted = true;

    async function hydrateConfig() {
      try {
        const config = await loadConfig();
        if (!mounted) return;
        const loadedForm = { ...initialForm, ...config.form };
        const normalizedForm = normalizeLoadedForm(loadedForm);
        const clientMigrations = [
          ["Modelo principal", loadedForm.model, normalizedForm.model],
          ["Fallback 1", loadedForm.fallback1Model, normalizedForm.fallback1Model],
          ["Fallback 2", loadedForm.fallback2Model, normalizedForm.fallback2Model],
        ]
          .filter(([, before, after]) => before && before !== after && RETIRED_MODELS[before.toLowerCase()])
          .map(([slot, before, after]) => `${slot}: o modelo retirado ${before} foi substituído por ${after}.`);
        setForm(normalizedForm);
        setConfigNotices([...(config.notices ?? []), ...clientMigrations]);
        setToggles({ ...initialToggles, ...config.toggles });
        setLocalItems({ ...initialLocalItems, ...config.localItems });
        setApiKeySet(Boolean(config.apiKeySet));
        setModelValidation(config.modelValidation);
        setSaved(true);
        setConfigError(null);
      } catch (error) {
        if (!mounted) return;
        setConfigError(error instanceof ApiError ? error.message : "Servico de configuracao offline.");
      } finally {
        if (mounted) setLoading(false);
      }
    }

    void hydrateConfig();
    return () => {
      mounted = false;
    };
  }, []);

  useEffect(() => {
    if (activeId !== "ai-usage") return;
    let mounted = true;
    setAIUsageLoading(true);
    setAIUsageError(null);
    void fetchAIUsage()
      .then((usage) => {
        if (!mounted) return;
        setAIUsage(usage);
        setModelValidation(usage.modelValidation);
      })
      .catch((error) => {
        if (mounted) setAIUsageError(error instanceof ApiError ? error.message : "Nao foi possivel carregar o uso de IA.");
      })
      .finally(() => {
        if (mounted) setAIUsageLoading(false);
      });
    return () => {
      mounted = false;
    };
  }, [activeId, aiUsageRefresh]);

  const activeSetting = useMemo(() => settings.find((setting) => setting.id === activeId) ?? settings[0], [activeId]);
  const queryPreview = useMemo(() => buildQueryPreview(form), [form]);
  const effectiveProfiles = useMemo(() => parseSearchProfiles(form), [form]);
  const activeProfile = effectiveProfiles.find((profile) => profile.roles.length > 0);
  const profileConfigured = Boolean(activeProfile);

  function markDirty() {
    setSaved(false);
  }

  function flip(key: SettingsToggleKey) {
    setToggles((current) => ({ ...current, [key]: !current[key] }));
    markDirty();
  }

  function setToggle(key: SettingsToggleKey, value: boolean) {
    setToggles((current) => ({ ...current, [key]: value }));
    markDirty();
  }

  function updateField<Key extends keyof SettingsForm>(key: Key, value: SettingsForm[Key]) {
    setForm((current) => ({ ...current, [key]: value }));
    markDirty();
  }

  // remoteOnly is derived from the work mode in normalizeConfig — it is no longer
  // an independently writable second source of truth for the same fact. It is set
  // here only so the UI does not show a stale value before the next save.
  function updateWorkMode(value: string) {
    setForm((current) => ({ ...current, workMode: value, location: value === "remote" ? current.remoteCountry : current.onsiteLocation }));
    setToggle("remoteOnly", value === "remote");
  }

  function updateProvider(provider: string) {
    setForm((current) => ({ ...current, provider, model: providerDefaultModel(provider) }));
    setModelOptions([]);
    setModelError(null);
    setModelNotice(null);
    setProviderStatus(null);
    markDirty();
  }

  function updateFallbackProvider(slot: 1 | 2, provider: string) {
    const providerKey = slot === 1 ? "fallback1Provider" : "fallback2Provider";
    const modelKey = slot === 1 ? "fallback1Model" : "fallback2Model";
    setForm((current) => ({ ...current, [providerKey]: provider, [modelKey]: provider ? providerDefaultModel(provider) : "" }));
    markDirty();
  }

  function updateFallbackModel(slot: 1 | 2, model: string) {
    updateField(slot === 1 ? "fallback1Model" : "fallback2Model", model);
  }

  async function handleClearApiKey() {
    setSaving(true);
    setConfigError(null);
    try {
      const clearedForm = { ...form, apiKey: "" };
      const savedConfig = await saveConfig({
        ...initialConfig,
        form: clearedForm,
        toggles,
        localItems,
        apiKeySet: false,
      });
      setForm(normalizeLoadedForm({ ...initialForm, ...savedConfig.form }));
      setConfigNotices(savedConfig.notices ?? []);
      setApiKeySet(false);
      setModelValidation(savedConfig.modelValidation);
      setSaved(true);
    } catch (error) {
      setConfigError(error instanceof ApiError ? error.message : "Nao foi possivel remover a chave.");
    } finally {
      setSaving(false);
    }
  }

  async function handleSave() {
    setSaving(true);
    setConfigError(null);
    try {
      const nextKeySet = form.apiKey.trim() ? true : apiKeySet;
      const savedConfig = await saveConfig({ ...initialConfig, form, toggles, localItems, apiKeySet: nextKeySet });
      setForm(normalizeLoadedForm({ ...initialForm, ...savedConfig.form }));
      setConfigNotices(savedConfig.notices ?? []);
      setToggles({ ...initialToggles, ...savedConfig.toggles });
      setLocalItems({ ...initialLocalItems, ...savedConfig.localItems });
      setApiKeySet(Boolean(savedConfig.apiKeySet));
      setModelValidation(savedConfig.modelValidation);
      setSaved(true);
    } catch (error) {
      setSaved(false);
      setConfigError(error instanceof ApiError ? error.message : "Nao foi possivel salvar no backend Go.");
    } finally {
      setSaving(false);
    }
  }

  async function handleFetchModels() {
    setFetchingModels(true);
    setModelError(null);
    setModelNotice(null);
    try {
      const result = await fetchModels({ provider: form.provider, apiKey: form.apiKey });
      const visibleModels = visibleFetchedModels(result.models);
      setModelOptions(visibleModels);
      const nextModel = modelAfterFetch(form.provider, form.model, result.models);
      const notices: string[] = [];
      const hiddenCount = result.models.length - visibleModels.length;
      if (hiddenCount > 0) {
        notices.push(`${hiddenCount} modelo(s) preview/experimental foram ocultados da lista estável.`);
      }
      if (nextModel !== form.model) {
        notices.push(`O modelo ${form.model} não foi anunciado pelo provedor; o padrão ${nextModel} foi selecionado.`);
        updateField("model", nextModel);
      }
      if (!result.models.includes(nextModel)) {
        notices.push(`O padrão ${nextModel} não apareceu em ListModels; confira a lista antes de salvar.`);
      }
      setModelNotice(notices.join(" ") || null);
    } catch (error) {
      setModelOptions([]);
      setModelError(
        error instanceof ApiError
          ? (MODEL_FETCH_ERROR_PT[error.code ?? ""] ?? `Erro ${error.status}: ${error.message}`)
          : `Erro de conexao com o backend: ${error instanceof Error ? error.message : "falha desconhecida"}`,
      );
    } finally {
      setFetchingModels(false);
    }
  }

  async function handleTestProvider() {
    setTestingProvider(true);
    setProviderStatus(null);
    try {
      const result = await testProvider({
        provider: form.provider,
        apiKey: form.apiKey.trim() || undefined,
        model: form.model,
      });
      setProviderStatus(
        result.ok
          ? { tone: "success", text: `Conectado (${result.latencyMs} ms) — ${result.maskedKey}` }
          : { tone: "error", text: PROVIDER_STATUS_PT[result.errorCode ?? ""] ?? "Falha no teste do provider." },
      );
    } catch (error) {
      setProviderStatus({ tone: "error", text: error instanceof ApiError ? error.message : "Serviço indisponível." });
    } finally {
      setTestingProvider(false);
    }
  }

  async function handleResumeFile(file: File | undefined) {
    if (!file) return;
    setUploadingResume(true);
    setResumeNotice(null);
    try {
      const contentBase64 = await fileToBase64(file);
      const result = await uploadResume({ fileName: file.name, mimeType: file.type, contentBase64 });
      setForm((current) => formAfterResumeUpload(current, result));
      setResumeNotice(
        result.detectedRole
          ? `Curriculo salvo. Busca ajustada para ${result.detectedRole}${result.detectedSeniority ? ` (${result.detectedSeniority})` : ""}, com ${result.keywords.length} termos do proprio curriculo.`
          : result.extractedText
            ? "Curriculo salvo, mas nenhum cargo atual foi identificado. O perfil anterior foi limpo; preencha Cargo alvo."
            : "Curriculo salvo e Markdown criado com aviso. O PDF parece nao ter texto extraivel; se for escaneado, precisa de OCR.",
      );
      markDirty();
    } catch (error) {
      setResumeNotice(
        error instanceof ApiError
          ? UPLOAD_ERROR_PT[error.code ?? ""] ?? `Erro ${error.status}: ${error.message}`
          : error instanceof Error
            ? error.message
            : "Falha no upload.",
      );
    } finally {
      setUploadingResume(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  }

  const statusText = loading ? "carregando" : configError ? configError : saved ? "configuracao salva" : "alteracoes nao salvas";

  return (
    <section className="workspace simple-workspace settings-workspace">
      <div className="settings-shell">
        <aside className="settings-sidebar">
          <div className="settings-page-head">
            <h1>Configuracoes</h1>
            <p>Ajustes operacionais do JoBot.</p>
          </div>
          <nav className="settings-nav" aria-label="Secoes de configuracao">
            {settings.map(({ id, title, description, icon: Icon }) => (
              <button
                aria-label={title}
                aria-current={activeId === id ? "page" : undefined}
                className={`settings-nav-item ${activeId === id ? "is-selected" : ""}`}
                data-setting-id={id}
                key={id}
                onClick={() => setActiveId(id)}
                title={title}
                type="button"
              >
                <Icon size={17} />
                <span>
                  <strong>{title}</strong>
                  <small>{description}</small>
                </span>
                <ChevronDown size={14} />
              </button>
            ))}
          </nav>
        </aside>

        <section className="settings-panel" aria-labelledby="settings-panel-title">
          <header className="settings-panel-head">
            <div className="settings-panel-title">
              <h2 id="settings-panel-title">{activeSetting.title}</h2>
              <p>{activeSetting.description}</p>
            </div>
            <div className="settings-save-group">
              <span aria-live="polite">{statusText}</span>
              <button className="primary-button" type="button" aria-busy={saving} disabled={saving || loading} onClick={() => void handleSave()}>
                <Save size={15} />
                {saving ? "Salvando" : "Salvar"}
              </button>
            </div>
          </header>

          <div className="settings-panel-body" key={activeId}>
            {activeId === "ai" ? (
              <>
                <SectionIntro
                  title="Escolha como analisar"
                  description="Defina um provedor principal. Chaves, modelos e fallbacks continuam sob seu controle."
                  aside={<span className="precision-summary-badge">{form.provider} ativo</span>}
                />
                <div className="precision-provider-grid" aria-label="Provedores disponiveis">
                  {providerOptions.map((provider) => {
                    const selected = form.provider === provider;
                    return (
                      <button
                        aria-pressed={selected}
                        className="precision-provider-card"
                        key={provider}
                        onClick={() => {
                          if (!selected) updateProvider(provider);
                        }}
                        type="button"
                      >
                        <IntegrationLogo name={provider} size={34} className="precision-provider-mark" />
                        <span className={`precision-status-chip ${selected ? "is-active" : ""}`}>
                          <i />{selected ? (apiKeySet ? "Credencial no app" : "Principal") : "Disponivel"}
                        </span>
                        <strong>{provider}</strong>
                        <small>{selected ? "Usado nas proximas analises." : "Selecionar como provedor principal."}</small>
                      </button>
                    );
                  })}
                </div>

                <SectionIntro title="Configuracao ativa" description="Modelo, autenticacao e teste do provedor selecionado." />
                <section className="precision-surface precision-form-surface">
                <div className="settings-form-grid two">
                  <Field label="Provedor">
                    <select title={form.provider} value={form.provider} onChange={(event) => updateProvider(event.target.value)}>
                      {providerOptions.map((provider) => (
                        <option key={provider}>{provider}</option>
                      ))}
                    </select>
                  </Field>
                  <Field label="Modelo">
                    <select title={form.model} value={form.model} onChange={(event) => updateField("model", event.target.value)}>
                      <option>{form.model || providerDefaultModel(form.provider)}</option>
                      {modelOptions
                        .filter((model) => model !== form.model)
                        .map((model) => (
                          <option key={model}>{model}</option>
                        ))}
                    </select>
                  </Field>
                </div>
                <SettingsDisclosure
                  icon={<Bot size={16} />}
                  title="Fallbacks"
                  summary="Provedores alternativos usados quando o principal nao responde."
                  note={[form.fallback1Provider, form.fallback2Provider].filter(Boolean).length ? "Configurado" : "Desligado"}
                >
                <div className="settings-form-grid two">
                  <Field label="Fallback 1 provedor">
                    <select title={form.fallback1Provider} value={form.fallback1Provider} onChange={(event) => updateFallbackProvider(1, event.target.value)}>
                      <option value="">— desligado —</option>
                      {providerOptions.map((provider) => (
                        <option key={provider}>{provider}</option>
                      ))}
                    </select>
                  </Field>
                  <Field label="Fallback 1 modelo">
                    <select title={form.fallback1Model} disabled={!form.fallback1Provider} value={form.fallback1Model} onChange={(event) => updateFallbackModel(1, event.target.value)}>
                      {form.fallback1Provider ? (
                        <option>{form.fallback1Model || providerDefaultModel(form.fallback1Provider)}</option>
                      ) : (
                        <option value="">— desligado —</option>
                      )}
                    </select>
                  </Field>
                  <Field label="Fallback 2 provedor">
                    <select title={form.fallback2Provider} value={form.fallback2Provider} onChange={(event) => updateFallbackProvider(2, event.target.value)}>
                      <option value="">— desligado —</option>
                      {providerOptions.map((provider) => (
                        <option key={provider}>{provider}</option>
                      ))}
                    </select>
                  </Field>
                  <Field label="Fallback 2 modelo">
                    <select title={form.fallback2Model} disabled={!form.fallback2Provider} value={form.fallback2Model} onChange={(event) => updateFallbackModel(2, event.target.value)}>
                      {form.fallback2Provider ? (
                        <option>{form.fallback2Model || providerDefaultModel(form.fallback2Provider)}</option>
                      ) : (
                        <option value="">— desligado —</option>
                      )}
                    </select>
                  </Field>
                </div>
                </SettingsDisclosure>
                <div className="settings-inline-action">
                  <Field label={`Chave ${form.provider}`}>
                    <input
                      type="password"
                      value={form.apiKey}
                      placeholder={apiKeySet ? "Cole uma chave deste provider para substituir" : "Cole a chave deste provider"}
                      onChange={(event) => updateField("apiKey", event.target.value)}
                    />
                  </Field>
                  <button className="secondary-button" type="button" disabled={fetchingModels || testingProvider} onClick={() => void handleFetchModels()}>
                    <RefreshCw className={fetchingModels ? "is-spinning" : undefined} size={15} />
                    {fetchingModels ? "Buscando" : "Buscar modelos"}
                  </button>
                  <button className="secondary-button" type="button" aria-busy={testingProvider} disabled={fetchingModels || testingProvider} onClick={() => void handleTestProvider()}>
                    <RefreshCw className={testingProvider ? "is-spinning" : undefined} size={15} />
                    {testingProvider ? "Testando" : "Testar provider"}
                  </button>
                </div>
                <div className="settings-form-grid two">
                  <Field label="Modo Free Tier">
                    <select value={form.aiMode} onChange={(event) => updateField("aiMode", event.target.value as SettingsForm["aiMode"])}>
                      <option value="free_economy">Economia — Flash-Lite em todas as tarefas</option>
                      <option value="free_quality">Qualidade — Flash em documentos de alto valor</option>
                    </select>
                  </Field>
                  <StatusRow
                    title="Limite por operacao"
                    detail={form.aiMode === "free_quality" ? "Ate 8 chamadas em documentos; 6 nas demais operacoes." : "Ate 6 chamadas por operacao, incluindo retries e fallbacks."}
                  />
                </div>
                {configNotices.map((notice) => (
                  <div className="settings-alert" key={notice}>{notice}</div>
                ))}
                {modelNotice ? <div className="settings-alert">{modelNotice}</div> : null}
                {modelError ? <div className="settings-alert">{modelError}</div> : null}
                {providerStatus ? <div className={`settings-alert ${providerStatus.tone}`}>{providerStatus.text}</div> : null}
                {modelValidation ? (
                  <StatusRow
                    title={`Modelo ${modelValidation.status}`}
                    detail={`${modelValidation.active || modelValidation.requested} — ${modelValidation.message}`}
                  />
                ) : null}
                <ToggleRow checked={toggles.compatibility} description="Usar o modelo selecionado para pontuar e explicar compatibilidade." label="Analise por IA" onChange={() => flip("compatibility")} />
                <ToggleRow
                  checked={form.aiDataConsent}
                  description="Autoriza o provider configurado a processar o curriculo. Nome, email, telefone e URLs sao removidos antes do envio e restaurados apenas localmente."
                  label="Permitir processamento seguro por IA"
                  onChange={() => updateField("aiDataConsent", !form.aiDataConsent)}
                />
                {apiKeySet ? (
                  <div className="settings-actions">
                    <button
                      className="secondary-button danger-button"
                      type="button"
                      onClick={() => void handleClearApiKey()}
                    >
                      <Trash2 size={15} />
                      Remover chave salva
                    </button>
                  </div>
                ) : null}
                </section>
              </>
            ) : null}

            {activeId === "ai-usage" ? (
              <>
                <SectionIntro
                  title="Uso de hoje"
                  description="Consumo real do provedor, cache local e limite disponivel."
                  aside={<div className="settings-actions">
                  <button className="secondary-button" type="button" disabled={aiUsageLoading} onClick={() => setAIUsageRefresh((value) => value + 1)}>
                    <RefreshCw className={aiUsageLoading ? "is-spinning" : undefined} size={15} />
                    {aiUsageLoading ? "Atualizando" : "Atualizar uso"}
                  </button>
                  </div>}
                />
                {aiUsageError ? <div className="settings-alert">{aiUsageError}</div> : null}
                {aiUsage ? (
                  <>
                    <div className="precision-metric-strip">
                      <StatusRow title="Chamadas hoje" detail={`${aiUsage.requests} de ${aiUsage.budget || "sem limite"}`} />
                      <StatusRow title="Restante" detail={aiUsage.budget ? `${aiUsage.remaining} chamadas` : "Provider local sem limite diario"} />
                      <StatusRow title="Cache local" detail={`${aiUsage.cacheHits} resultados reutilizados`} />
                    </div>
                    <div className="precision-settings-list">
                    <StatusRow title="Modo ativo" detail={aiUsage.mode === "free_quality" ? "Free Quality" : "Free Economy"} />
                    <StatusRow title="Consentimento" detail={aiUsage.consent ? "Ativo; identificadores diretos sao redigidos antes do envio." : "Desativado; tarefas com curriculo nao sao enviadas a providers remotos."} />
                    {aiUsage.modelValidation ? (
                      <StatusRow title={`Validacao do modelo: ${aiUsage.modelValidation.status}`} detail={aiUsage.modelValidation.message} />
                    ) : null}
                    </div>
                    <SettingsDisclosure
                      icon={<BarChart3 size={16} />}
                      title="Detalhamento por operacao"
                      summary="Limites e chamadas agrupados por tarefa, provedor e modelo."
                      note={`${aiUsage.breakdown.length} grupos`}
                    >
                    <div className="settings-form-grid two">
                      {Object.entries(aiUsage.operationBudgets).map(([purpose, budget]) => (
                        <StatusRow key={purpose} title={purpose.replaceAll("_", " ")} detail={`ate ${budget} chamada(s) por operacao`} />
                      ))}
                    </div>
                    {aiUsage.breakdown.length ? (
                      aiUsage.breakdown.map((item) => (
                        <StatusRow
                          key={`${item.purpose}-${item.provider}-${item.model}`}
                          title={`${item.purpose.replaceAll("_", " ")} · ${item.provider}`}
                          detail={`${item.requests} chamada(s), ${item.cacheHits} cache · ${item.model}`}
                        />
                      ))
                    ) : (
                      <StatusRow title="Sem uso hoje" detail="Nenhuma chamada de provider foi registrada neste dia." />
                    )}
                    </SettingsDisclosure>
                  </>
                ) : null}
              </>
            ) : null}

            {activeId === "sources" ? (
              <>
                <SectionIntro
                  title="Fontes conectadas"
                  description="Ative apenas as plataformas que deseja consultar em cada rodada."
                  aside={<span className="precision-summary-badge">8 fontes</span>}
                />
                <div className="precision-source-grid">
                  <SourceToggleCard source="LinkedIn" label="LinkedIn" description="Listagens publicas" checked={toggles.useLinkedin} onChange={() => flip("useLinkedin")} />
                  <SourceToggleCard source="Indeed" label="Indeed (bloqueado pela Cloudflare)" description="Listing-only; o detalhe abre no navegador" checked={toggles.useIndeed} onChange={() => flip("useIndeed")} />
                  <SourceToggleCard source="Gupy" label="Gupy" description="Vagas brasileiras" checked={toggles.useGupy} onChange={() => flip("useGupy")} />
                  <SourceToggleCard source="Remotive" label="Remotive" description="Remoto mundial" checked={toggles.useRemotive} onChange={() => flip("useRemotive")} />
                  <SourceToggleCard source="RemoteOK" label="RemoteOK" description="Remoto em tecnologia" checked={toggles.useRemoteok} onChange={() => flip("useRemoteok")} />
                  <SourceToggleCard source="Jobicy" label="Jobicy" description="Remoto por regiao" checked={toggles.useJobicy} onChange={() => flip("useJobicy")} />
                  <SourceToggleCard source="Arbeitnow" label="Arbeitnow" description="Europa e Alemanha" checked={toggles.useArbeitnow} onChange={() => flip("useArbeitnow")} />
                  <SourceToggleCard source="We Work Remotely" label="We Work Remotely" description="Feed remoto mundial" checked={toggles.useWeworkremotely} onChange={() => flip("useWeworkremotely")} />
                </div>

                <SectionIntro title="Regras da coleta" description="Preferencias que valem para as fontes habilitadas." />
                <section className="precision-surface precision-form-surface">
                <div className="settings-form-grid three">
                  <Field label="Fonte principal">
                    <select value={form.source} onChange={(event) => updateField("source", event.target.value)}>
                      <option value="" disabled>Selecionar fonte</option>
                      <option>LinkedIn</option>
                      <option>Indeed</option>
                      <option>Gupy</option>
                    </select>
                  </Field>
                  <Field label="Data de postagem">
                    <select value={form.recentHours} onChange={(event) => updateField("recentHours", Number(event.target.value))}>
                      {recentHourOptions.map((hours) => (
                        <option key={hours} value={hours}>Ultimas {hours} {hours === 1 ? "hora" : "horas"}</option>
                      ))}
                    </select>
                  </Field>
                  <Field label="Paginas do LinkedIn">
                    <input min="1" max="10" type="number" value={form.linkedinPages} onChange={(event) => updateField("linkedinPages", Number(event.currentTarget.value))} />
                  </Field>
                </div>
                <ToggleRow checked={toggles.saveHistory} description="Registrar filtros e resultados no banco local." label="Salvar historico" onChange={() => flip("saveHistory")} />
                </section>
                <div className="precision-info-callout">
                  <ShieldCheck size={17} />
                  <div><strong>Indeed usa apenas os dados da listagem.</strong><p>Ao abrir a vaga, qualquer verificacao do site acontece diretamente no seu navegador.</p></div>
                </div>
                <SectionIntro title="Worker do navegador" description="Diagnostico do transporte usado pelas fontes que precisam de navegador." />
                <BrowserWorkerStatus />
              </>
            ) : null}

            {activeId === "profile" ? (
              <>
                <div className="precision-profile-page">
                <input
                  ref={fileInputRef}
                  className="hidden-file-input"
                  type="file"
                  accept=".pdf,.docx,.txt,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain"
                  onChange={(event) => void handleResumeFile(event.target.files?.[0])}
                />
                <div className="resume-upload-row precision-surface">
                  <span className="precision-row-icon"><FileText size={17} /></span>
                  <div className="precision-row-copy">
                    <strong>{form.resumeName || "Nenhum curriculo salvo"}</strong>
                    <small>
                      {form.resumeMarkdownPath
                        ? "Original salvo e resume.md gerado para a LLM."
                        : form.resumePath
                          ? "Arquivo salvo localmente e persistido no banco."
                          : "Aceita PDF, DOCX ou TXT."}
                    </small>
                  </div>
                  <span className={`precision-status-chip ${form.resumePath ? "is-success" : ""}`}><i />{form.resumePath ? "Pronto" : "Pendente"}</span>
                  <button className="secondary-button" type="button" disabled={uploadingResume} onClick={() => fileInputRef.current?.click()}>
                    <Upload size={15} />
                    {uploadingResume ? "Enviando" : "Upload local"}
                  </button>
                </div>
                {resumeNotice ? <div className="settings-alert">{resumeNotice}</div> : null}

                <SectionIntro
                  title="Direcao da busca"
                  description="Informe o cargo desejado; o curriculo enviado fornece os termos usados na compatibilidade."
                  className="profile-direction-intro"
                />
                <section className="precision-surface precision-direction-panel">
                  <div className="precision-direction-head">
                    <span className={`precision-direction-check ${profileConfigured ? "" : "is-pending"}`.trim()}>
                      {profileConfigured ? <Check size={19} /> : <Search size={18} />}
                    </span>
                    <div className="precision-row-copy">
                      <span className="precision-direction-kicker">Configuracao atual</span>
                      <strong>{activeProfile?.name || "Defina um cargo alvo"}</strong>
                      <small>
                        {form.searchProfiles.trim()
                          ? `${effectiveProfiles.length} perfil(is) avancado(s) ativo(s)`
                          : form.resumeName
                            ? `Baseado em ${form.resumeName}`
                            : "Envie um curriculo para usar seus termos no score."}
                      </small>
                    </div>
                  </div>
                <div className="settings-form-grid two">
                  <Field label="Cargo alvo">
                    <input
                      value={form.role}
                      placeholder={SETTINGS_ROLE_EXAMPLE}
                      onChange={(event) => {
                        setForm((current) => formWithTargetRole(current, event.target.value));
                        markDirty();
                      }}
                    />
                  </Field>
                  <Field label="Senioridade principal">
                    <select
                      value={form.seniority}
                      onChange={(event) => {
                        setForm((current) => formWithTargetSeniority(current, event.target.value));
                        markDirty();
                      }}
                    >
                      <option value="" disabled>Selecionar senioridade</option>
                      <option>Junior</option>
                      <option>Pleno</option>
                      <option>Senior</option>
                      <option>Staff</option>
                      <option>Lead</option>
                      <option>Principal</option>
                      <option>Manager</option>
                      <option>Especialista</option>
                      <option>Estágio</option>
                    </select>
                  </Field>
                </div>
                <div className="precision-direction-summary">
                  <div><span>Cargos procurados</span><strong>{activeProfile?.roles.join(", ") || "Nao definido"}</strong></div>
                  <div><span>Senioridade</span><strong>{activeProfile?.levels.join(", ") || "Todas"}</strong></div>
                  <div><span>Base do score</span><strong>{csvItems(form.keywords).length} termos do curriculo</strong></div>
                </div>
                </section>

                <SettingsDisclosure
                  icon={<FileText size={16} />}
                  title="Perfis de busca (avancado)"
                  summary="Formato tecnico preservado para configuracoes existentes. Deixe vazio para usar os campos simples."
                  note={form.searchProfiles.trim() ? "Em uso" : "Vazio"}
                  className="profile-profiles"
                >
                <div className="profiles-block">
                  <div className="profiles-block-head">
                    <strong>Perfis de busca (avancado)</strong>
                    <span>
                      Um perfil por linha, no formato <code>cargos | niveis aceitos | niveis bloqueados | max anos</code>.
                      Cada perfil roda uma busca propria com a sua senioridade. Deixe vazio para usar o modo simples abaixo.
                    </span>
                  </div>
                  <Field label="Perfis (uma linha por combinacao cargo + senioridade)">
                    <textarea
                      className="large-textarea profiles-textarea"
                      value={form.searchProfiles}
                      placeholder={SETTINGS_PROFILE_EXAMPLES}
                      onChange={(event) => updateField("searchProfiles", event.target.value)}
                    />
                  </Field>
                </div>
                </SettingsDisclosure>

                <SettingsDisclosure
                  icon={<SlidersHorizontal size={16} />}
                  title="Filtros adicionais"
                  summary="Titulos relacionados, senioridades aceitas e limite de experiencia."
                  note="Avancado"
                  className="profile-filters"
                >
                <div className="simple-mode">
                  <div className="settings-form-grid two">
                    <Field label="Roles da busca (comma-separated)">
                      <textarea value={form.roles} onChange={(event) => updateField("roles", event.target.value)} />
                    </Field>
                    <Field label="Levels da busca (comma-separated)">
                      <textarea value={form.levels} onChange={(event) => updateField("levels", event.target.value)} />
                    </Field>
                  </div>

                  <div className="settings-form-grid two">
                    <Field label="Senioridades excluidas (comma-separated)">
                      <textarea
                        value={form.excludedLevels}
                        placeholder="Tech Lead, Lead, Staff, Principal, Manager..."
                        onChange={(event) => updateField("excludedLevels", event.target.value)}
                      />
                    </Field>
                    <Field label="Experiencia maxima (anos)">
                      <input
                        min="1"
                        max="30"
                        type="number"
                        value={form.maxYears}
                        onChange={(event) => updateField("maxYears", Number(event.currentTarget.value))}
                      />
                    </Field>
                  </div>
                </div>
                </SettingsDisclosure>

                <SettingsDisclosure
                  icon={<Search size={16} />}
                  title="Termos de compatibilidade"
                  summary={`${csvItems(form.keywords).length} termos usados para calcular o score das vagas.`}
                  note="Editavel"
                  className="profile-keywords"
                >
                  <Field label="CV core skills / keywords (definem o score — editavel, prioridade sobre o curriculo)">
                    <textarea className="large-textarea" value={form.keywords} placeholder={SETTINGS_KEYWORD_EXAMPLE} onChange={(event) => updateField("keywords", event.target.value)} />
                  </Field>
                </SettingsDisclosure>

                <SettingsDisclosure className="profile-query" icon={<Search size={16} />} title="Plano tecnico da busca" summary="Confira a consulta efetiva antes de iniciar uma rodada." note="Somente leitura">
                  <div className="query-preview"><span>Plano de buscas</span><code>{queryPreview}</code></div>
                </SettingsDisclosure>

                <SectionIntro className="profile-location-intro" title="Onde procurar" description="Modalidade e localizacao nao sao inferidas do curriculo." />
                <section className="precision-surface precision-form-surface profile-location">
                <div className="settings-form-grid three">
                  <Field label="Modalidade">
                    {/*
                      This used to offer a single "hybrid_onsite" option — a token
                      the Go pipeline has never recognized. It matched neither
                      hybrid nor onsite, so both search passes were skipped and a
                      remote-only fallback took over: a saved "Chicago, on-site"
                      left the app as "location=United States&f_WT=2" (UX-015).
                      The three values here are now exactly the three the backend
                      understands.
                    */}
                    <select value={form.workMode} onChange={(event) => updateWorkMode(event.target.value)}>
                      <option value="remote">Remoto</option>
                      <option value="hybrid">Hibrido (remoto + presencial)</option>
                      <option value="onsite">Presencial</option>
                    </select>
                  </Field>
                  <Field label="Local presencial/hibrido">
                    <input value={form.onsiteLocation} onChange={(event) => updateField("onsiteLocation", event.target.value)} />
                  </Field>
                  <Field label="Pais remoto">
                    <input value={form.remoteCountry} onChange={(event) => updateField("remoteCountry", event.target.value)} />
                  </Field>
                </div>
                </section>

                <SettingsDisclosure className="profile-blacklist" icon={<ShieldCheck size={16} />} title="Empresas bloqueadas" summary="Nao mostrar vagas das empresas informadas." note={csvItems(form.blacklistCompanies).length ? `${csvItems(form.blacklistCompanies).length} bloqueadas` : "Nenhuma"}>
                  <Field label="Empresas bloqueadas (comma-separated)">
                    <textarea value={form.blacklistCompanies} placeholder="Outlier, DataAnnotation, Turing..." onChange={(event) => updateField("blacklistCompanies", event.target.value)} />
                  </Field>
                </SettingsDisclosure>

                <div className={`precision-search-plan ${profileConfigured ? "" : "is-pending"}`.trim()}>
                  <span>{profileConfigured ? <Check size={15} /> : <Search size={15} />}{profileConfigured ? "Busca configurada" : "Configuracao pendente"}</span>
                  <strong>{activeProfile?.roles.join(", ") || "Defina o cargo alvo"} · {form.workMode === "remote" ? `Remoto em ${form.remoteCountry || "local a definir"}` : `${form.workMode === "hybrid" ? "Hibrido" : "Presencial"} em ${form.onsiteLocation || "local a definir"}`}</strong>
                </div>
                </div>
              </>
            ) : null}

            {activeId === "pipeline" ? (
              <>
                <SectionIntro title="Ritmo da busca" description="Controle volume, frequencia e corte de compatibilidade." />
                <section className="precision-surface precision-form-surface">
                <div className="settings-form-grid three">
                  <Field label="Max jobs por rodada">
                    <input min="1" max="200" type="number" value={form.maxJobs} onChange={(event) => updateField("maxJobs", Number(event.currentTarget.value))} />
                  </Field>
                  <Field label="Delay maximo entre jobs (s)">
                    <input min="0" max="60" step="0.5" type="number" value={form.maxDelaySeconds} onChange={(event) => updateField("maxDelaySeconds", Number(event.currentTarget.value))} />
                  </Field>
                  <Field label="Intervalo do radar (min)">
                    <input min="1" max="240" type="number" value={form.radarIntervalMinutes} onChange={(event) => updateField("radarIntervalMinutes", Number(event.currentTarget.value))} />
                  </Field>
                </div>
                <div className="settings-form-grid three">
                  <Field label={`Match threshold: ${form.scoreCut}`}>
                    <input type="range" min="40" max="95" value={form.scoreCut} onChange={(event) => updateField("scoreCut", Number(event.currentTarget.value))} />
                  </Field>
                  <Field label="Notificacao threshold">
                    <input min="0" max="100" type="number" value={form.notificationThreshold} onChange={(event) => updateField("notificationThreshold", Number(event.currentTarget.value))} />
                  </Field>
                  <Field label="Modo de ranking">
                    <select value={form.rankingMode} onChange={(event) => updateField("rankingMode", event.target.value)}>
                      <option>compatibilidade</option>
                      <option>remoto primeiro</option>
                      <option>mais recentes</option>
                    </select>
                  </Field>
                </div>
                </section>
                <SectionIntro title="Automacao" description="Comportamentos opcionais durante e depois de cada rodada." />
                <div className="settings-toggle-grid">
                  <ToggleRow checked={toggles.radarMode} description={`Varre automaticamente a cada ${form.radarIntervalMinutes || 20} minutos enquanto o app estiver aberto.`} label="Radar mode" onChange={() => flip("radarMode")} />
                  <ToggleRow checked={toggles.autoClean} description="Ocultar vagas abaixo do match threshold." label="Corte automatico" onChange={() => flip("autoClean")} />
                  <ToggleRow checked={toggles.headless} description="Executar scraping sem janela visual quando possivel." label="Modo headless" onChange={() => flip("headless")} />
                  <ToggleRow checked={toggles.desktop} description="Avisar quando aparecer vaga acima do threshold." label="Alertas no desktop" onChange={() => flip("desktop")} />
                </div>
              </>
            ) : null}

            {activeId === "privacy" ? (
              <>
                <SectionIntro title="Privacidade e armazenamento" description="Controle o armazenamento local e prepare a configuracao para uma futura exportacao." />
                <div className="precision-settings-list">
                <ToggleRow checked={toggles.localOnly} description="Manter historico, preferencias e dados neste computador." label="Modo local" onChange={() => flip("localOnly")} />
                <ToggleRow checked={toggles.exportReady} description="Marcar esta configuracao como pronta para uma futura rotina de exportacao." label="Exportacao pronta" onChange={() => flip("exportReady")} />
                </div>
                <div className="settings-actions">
                  <button className="secondary-button" type="button" onClick={() => setToggle("exportReady", true)}>
                    <Download size={15} />
                    Marcar como pronta
                  </button>
                  <button className="secondary-button danger-button" type="button" disabled title="A limpeza real do banco ainda nao esta disponivel nesta tela.">
                    <Trash2 size={15} />
                    Apagar dados
                  </button>
                </div>
                <div className="precision-info-callout is-warning"><ShieldCheck size={17} /><div><strong>Limpeza indisponivel nesta tela.</strong><p>O botao permanece desativado para nao indicar uma exclusao que o backend ainda nao executa.</p></div></div>
              </>
            ) : null}

            {activeId === "local" ? (
              <>
                <SectionIntro title="Resumo do banco local" description="Contagens carregadas diretamente da configuracao persistida." />
                <div className="precision-metric-strip is-four">
                <StatusRow title="Vagas locais" detail={`${localItems.jobs} vagas salvas no banco local.`} />
                <StatusRow title="Favoritas" detail={`${localItems.saved} vagas marcadas para revisar depois.`} />
                <StatusRow title="Candidaturas" detail={`${localItems.applications} candidaturas registradas.`} />
                <StatusRow title="Historico" detail={`${localItems.history} buscas registradas.`} />
                </div>
                <div className="settings-actions">
                  <button className="secondary-button danger-button" type="button" disabled title="A limpeza real do banco ainda nao esta disponivel nesta tela.">
                    <Trash2 size={15} />
                    Limpar banco
                  </button>
                </div>
                <div className="precision-info-callout is-warning"><Database size={17} /><div><strong>Nenhum dado sera apagado por engano.</strong><p>A limpeza ficara disponivel somente quando existir uma operacao real e verificavel no backend.</p></div></div>
              </>
            ) : null}

            {activeId === "install" ? (
              <>
                <SectionIntro title="Saude da instalacao" description="Verifique os componentes locais e execute apenas reparos suportados." />
                <InstallHealthPanel />
              </>
            ) : null}

            {activeId === "shortcuts" ? (
              <>
                <SectionIntro title="Comandos globais" description="Atalhos realmente usados pelo aplicativo." />
                <section className="precision-surface precision-form-surface">
                <div className="settings-form-grid two">
                  <Field label="Nova busca">
                    <input value={form.shortcutSearch} onChange={(event) => updateField("shortcutSearch", event.target.value)} />
                  </Field>
                  <Field label="Notas">
                    <input value={form.shortcutNotes} onChange={(event) => updateField("shortcutNotes", event.target.value)} />
                  </Field>
                </div>
                </section>
                <div className="settings-actions">
                  <button
                    className="secondary-button"
                    type="button"
                    onClick={() => {
                      setForm((current) => ({ ...current, shortcutSearch: "F8", shortcutNotes: "F4" }));
                      markDirty();
                    }}
                  >
                    <RotateCcw size={15} />
                    Restaurar padrao
                  </button>
                </div>
              </>
            ) : null}
          </div>
        </section>
      </div>
    </section>
  );
}

import { AlertTriangle, ArrowUpDown, CheckCircle2, ChevronRight, Filter, LoaderCircle, Search, SlidersHorizontal, Sparkles } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { ApiError, applyJobAction, fetchSearchPlan, loadConfig, openJobUrl, saveConfig } from "../../services/api";
import type { AppConfig, JobAction, JobSummary, SearchDiagnostics, SearchPlan } from "../../types";
import { useSearch } from "../../app/SearchContext";
import { SourceBadge, formatJobMeta } from "../../components/SourceBadge";
import { JobDetailPanel, MatchGauge } from "./JobDetailPanel";

type SearchNotice = { tone: "neutral" | "error"; text: string };
type PlanAction = "use-simple-role" | "clear-profiles" | "keep-keywords" | "clear-keywords";
type ResultGroup = "recommended" | "lowScore";

function statusClass(status: string) {
  const normalized = status.toLowerCase();
  if (normalized.includes("scoring")) return "pending";
  if (normalized.includes("apply")) return "apply";
  if (normalized.includes("adjust")) return "adjust";
  return "discard";
}

function JobCard({
  job,
  selected,
  onSelect,
}: {
  job: JobSummary;
  selected: boolean;
  onSelect: () => void;
}) {
  const cls = statusClass(job.status);
  const pending = Boolean(job.scoringPending);
  const missing = pending ? [] : (job.missingKeywords?.filter(Boolean).slice(0, 4) ?? []);
  const readiness = pending
    ? "Analisando score"
    : cls === "apply"
      ? "Curriculo pronto"
      : missing.length > 0
        ? `${missing.length} ajuste(s) sugerido(s)`
        : "Revisar vaga";

  return (
    <article className={`job-card ${cls} ${selected ? "is-selected" : ""}`}>
      <div className="job-card-row">
        <SourceBadge source={job.source} size="sm" />
        <MatchGauge score={job.score} pending={pending} />
      </div>
      <div className="job-card-main">
        <h2>{job.title}</h2>
        <p>{formatJobMeta(job.company, job.location)}</p>
      </div>
      <span className={`job-card-readiness ${cls === "apply" ? "is-ready" : ""}`}>
        {pending ? <LoaderCircle className="is-spinning" size={13} /> : cls === "apply" ? <CheckCircle2 size={13} /> : <Sparkles size={13} />}
        {readiness}
      </span>
      <button
        className="job-card-select"
        type="button"
        aria-label={`Ver detalhes de ${job.title} em ${job.company}`}
        aria-current={selected ? "true" : undefined}
        onClick={onSelect}
      />
    </article>
  );
}

function actionLabel(action: JobAction) {
  switch (action) {
    case "applied":
      return "aplicada";
    case "blacklist":
      return "blacklist";
    case "save":
      return "salva";
    case "unsave":
      return "removida das salvas";
    default:
      return "dispensada";
  }
}

function actionFailure(action: JobAction) {
  switch (action) {
    case "applied":
      return "Nao foi possivel marcar a vaga como aplicada.";
    case "blacklist":
      return "Nao foi possivel adicionar a empresa a blacklist.";
    case "save":
      return "Nao foi possivel salvar a vaga.";
    case "unsave":
      return "Nao foi possivel remover a vaga das salvas.";
    default:
      return "Nao foi possivel dispensar a vaga.";
  }
}

function matchesModality(job: JobSummary, modality: "all" | "remote" | "hybrid") {
  const local = `${job.title} ${job.location} ${job.company}`.toLowerCase();
  if (modality === "remote") {
    return local.includes("remote") || local.includes("remoto") || local.includes("brazil") || local.includes("brasil");
  }
  if (modality === "hybrid") {
    return local.includes("hybrid") || local.includes("hibrid") || local.includes("sao paulo") || local.includes("porto alegre");
  }
  return true;
}

type SearchViewProps = {
  onOpenSettings?: (section: "profile") => void;
  onOptimizeResume?: (job: JobSummary) => void;
  onViewGaps?: (job: JobSummary) => void;
  onGenerateCoverLetter?: (job: JobSummary) => void;
};

function hasDiagnostics(diagnostics: SearchDiagnostics | null) {
  return Boolean(diagnostics && (diagnostics.collected > 0 || diagnostics.fresh > 0 || diagnostics.evaluated > 0));
}

// searchRanButCollectedNothing is true when a real search completed yet the
// sources returned zero listings (typically anti-bot blocking, not a missing
// setup). diagnostics is null only before any search has run this session.
function searchRanButCollectedNothing(diagnostics: SearchDiagnostics | null) {
  return Boolean(diagnostics && diagnostics.collected === 0);
}

function emptyTitle(running: boolean, diagnostics: SearchDiagnostics | null, hasNotice: boolean) {
  if (running) return "Buscando vagas...";
  if (hasDiagnostics(diagnostics)) return "Nenhuma vaga aprovada";
  if (searchRanButCollectedNothing(diagnostics)) return "Nenhuma vaga coletada";
  if (hasNotice) return "Nenhuma vaga aprovada";
  return "Nenhuma busca executada";
}

function emptyDescription({
  running,
  diagnostics,
  jobs,
  liveCount,
  filteredByModality,
}: {
  running: boolean;
  diagnostics: SearchDiagnostics | null;
  jobs: JobSummary[];
  liveCount: number;
  filteredByModality: boolean;
}) {
  if (filteredByModality) {
    return `${jobs.length} vaga(s) encontradas, mas ocultas pelo filtro de modalidade. Use "Todas" para ve-las.`;
  }
  if (running) {
    if (liveCount > 0) {
      return `${liveCount} vaga(s) aprovada(s) ate agora. Novos cards aparecem conforme a analise avanca.`;
    }
    return "Coletando listings e analisando compatibilidade. Voce pode abrir Logs sem interromper a busca.";
  }
  if (hasDiagnostics(diagnostics)) {
    if ((diagnostics?.evaluated ?? 0) === 0 && (diagnostics?.dropped ?? 0) > 0) {
      return `A busca coletou ${diagnostics?.collected ?? 0} vaga(s), mas os filtros de senioridade, anos, modalidade ou blacklist bloquearam os primeiros candidatos antes do score.`;
    }
    if ((diagnostics?.evaluated ?? 0) > 0 && (diagnostics?.discarded ?? 0) >= (diagnostics?.evaluated ?? 0)) {
      return `A busca analisou ${diagnostics?.evaluated ?? 0} vaga(s), mas nenhuma atingiu o corte atual. Reduza o score minimo ou ajuste as keywords.`;
    }
    if ((diagnostics?.skippedNoDescription ?? 0) > 0 && (diagnostics?.evaluated ?? 0) === 0) {
      return `As fontes coletaram vagas, mas ${diagnostics?.skippedNoDescription ?? 0} ficaram sem descricao suficiente para comparar com o perfil.`;
    }
    return `A busca coletou ${diagnostics?.collected ?? 0} vaga(s), ${diagnostics?.fresh ?? 0} ficaram dentro da janela e ${diagnostics?.evaluated ?? 0} foram analisadas. Nenhuma passou no corte atual.`;
  }
  if (searchRanButCollectedNothing(diagnostics)) {
    return "As fontes nao retornaram vagas nesta busca. Isso costuma ser bloqueio anti-bot do LinkedIn/Indeed ou ausencia de vagas recentes — tente novamente, aumente a janela de data ou revise cargos e fontes.";
  }
  return "Configure perfil, fontes e keywords para encontrar vagas compativeis.";
}

function diagnosticSourceRows(diagnostics: SearchDiagnostics | null) {
  return Object.entries(diagnostics?.sources ?? {})
    .filter(([, stats]) => stats.collected > 0 || stats.fresh > 0 || stats.evaluated > 0 || stats.skippedNoDescription > 0)
    .map(([source, stats]) => {
      const detail = stats.detailFetched > 0 ? `, ${stats.detailFetched} c/ detalhe` : "";
      return `${source}: ${stats.collected} coletadas, ${stats.fresh} recentes, ${stats.evaluated} analisadas, ${stats.approved} aprovadas${detail}, ${stats.dropped + stats.discarded + stats.skippedNoDescription} fora`;
    });
}

function searchPlanModeLabel(mode: SearchPlan["workMode"]) {
  switch (mode) {
    case "hybrid":
      return "Hibrida";
    case "onsite":
      return "Presencial";
    default:
      return "Remota";
  }
}

function searchPlanLocationLabel(plan: SearchPlan) {
  if (plan.locations.length === 0) return "Nenhuma localizacao resolvida";
  return plan.locations
    .map((item) => `${item.location || "Sem local"} (${item.remote ? "remota" : "presencial"})`)
    .join(" + ");
}

// Older/fresh backends can encode an empty Go slice as JSON null even though
// SearchPlan's public contract is an array. Normalize once at the boundary so
// a malformed optional response cannot take down the entire first-run UI.
function normalizeSearchPlan(plan: SearchPlan): SearchPlan {
  return {
    ...plan,
    roles: Array.isArray(plan.roles) ? plan.roles : [],
    ignoredRoles: Array.isArray(plan.ignoredRoles) ? plan.ignoredRoles : [],
    levels: Array.isArray(plan.levels) ? plan.levels : [],
    excludedLevels: Array.isArray(plan.excludedLevels) ? plan.excludedLevels : [],
    scoringTerms: Array.isArray(plan.scoringTerms) ? plan.scoringTerms : [],
    keywordsForRoles: Array.isArray(plan.keywordsForRoles) ? plan.keywordsForRoles : [],
    locations: Array.isArray(plan.locations) ? plan.locations : [],
    sources: Array.isArray(plan.sources) ? plan.sources : [],
  };
}

function SearchPlanPanel({
  plan,
  loading,
  error,
  busyAction,
  onAction,
  onAdjust,
}: {
  plan: SearchPlan | null;
  loading: boolean;
  error: string | null;
  busyAction: PlanAction | null;
  onAction: (action: PlanAction) => void;
  onAdjust: () => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const ignoredRoles = plan?.ignoredRoles ?? [];
  const roles = plan?.roles.join(" · ") || "Nenhum cargo configurado";
  const location = plan?.locations.find((item) => item.location.trim())?.location || "Local nao definido";
  const sourceCount = plan?.sources.length ?? 0;

  return (
    <section className="search-plan" aria-labelledby="search-plan-title" aria-busy={loading}>
      <div className="search-plan-summary">
        <span className="search-plan-icon">
          {loading ? <LoaderCircle className="is-spinning" size={17} aria-label="Atualizando plano" /> : <Sparkles size={17} aria-hidden="true" />}
        </span>
        <div className="search-plan-copy">
          <span>{plan?.scoringSource === "resume" ? "Busca preparada pelo curriculo" : "Busca configurada"}</span>
          <strong id="search-plan-title">{loading && !plan ? "Preparando a direcao da busca" : roles}</strong>
        </div>
        {plan ? (
          <div className="search-plan-chips" aria-label="Resumo da busca">
            <span>{searchPlanModeLabel(plan.workMode)}</span>
            <span>{location}</span>
            <span>{sourceCount} {sourceCount === 1 ? "fonte" : "fontes"}</span>
          </div>
        ) : null}
        <button
          className="search-plan-disclosure"
          type="button"
          aria-expanded={expanded}
          aria-controls="search-plan-details"
          disabled={!plan}
          onClick={() => setExpanded((current) => !current)}
        >
          Como foi definido
          <ChevronRight size={14} aria-hidden="true" />
        </button>
        <button className="secondary-button search-plan-adjust" type="button" onClick={onAdjust}>
          <SlidersHorizontal size={15} />
          Ajustar
        </button>
      </div>

      {error ? <div className="search-plan-error" role="alert">{error}</div> : null}
      {plan ? (
        <div className="search-plan-details" id="search-plan-details" hidden={!expanded}>
          <dl className="search-plan-grid">
            <div>
              <dt>Cargos procurados</dt>
              <dd>{plan.roles.join(" · ") || "Nenhum cargo configurado"}</dd>
              <small>{plan.rolesSource === "profiles" ? "Perfis avancados" : "Modo simples"}</small>
            </div>
            <div>
              <dt>Senioridade compativel</dt>
              <dd>{plan.levels.join(" · ") || "Sem filtro"}</dd>
              {plan.excludedLevels?.length ? <small>Exclui: {plan.excludedLevels.join(", ")}</small> : null}
            </div>
            <div>
              <dt>Base do score</dt>
              <dd>{plan.scoringTerms.length} termos</dd>
              <small>{plan.scoringTerms.slice(0, 6).join(", ") || "Nenhuma"}</small>
            </div>
            <div>
              <dt>Modalidade e local</dt>
              <dd>{searchPlanModeLabel(plan.workMode)}</dd>
              <small>{searchPlanLocationLabel(plan)}</small>
            </div>
            <div className="search-plan-sources">
              <dt>Fontes ativas</dt>
              <dd>{plan.sources.join(", ") || "Nenhuma fonte ativa"}</dd>
            </div>
          </dl>
        </div>
      ) : null}

      {ignoredRoles.length > 0 ? (
        <div className="search-plan-warning" role="alert">
          <AlertTriangle size={18} aria-hidden="true" />
          <div>
            <strong>Perfis avancados estao ignorando o cargo simples: {ignoredRoles.join(", ")}.</strong>
            <p>A busca usara os cargos dos perfis mostrados acima ate voce escolher outra configuracao.</p>
            <div className="search-plan-actions">
              <button type="button" disabled={busyAction !== null} onClick={() => onAction("use-simple-role")}>Usar cargo simples</button>
              <button type="button" disabled={busyAction !== null} onClick={() => onAction("clear-profiles")}>Limpar perfis avancados</button>
            </div>
          </div>
        </div>
      ) : null}

      {plan?.staleKeywords ? (
        <div className="search-plan-warning" role="alert">
          <AlertTriangle size={18} aria-hidden="true" />
          <div>
            <strong>As keywords foram escritas para {plan.keywordsForRoles?.join(", ") || "outro cargo"}.</strong>
            <p>Elas ainda pontuarao esta busca. Confirme que deseja mante-las ou limpe a lista manualmente.</p>
            <div className="search-plan-actions">
              <button type="button" disabled={busyAction !== null} onClick={() => onAction("keep-keywords")}>Manter keywords</button>
              <button type="button" disabled={busyAction !== null} onClick={() => onAction("clear-keywords")}>Limpar keywords</button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}

export function SearchView({ onOpenSettings, onOptimizeResume, onViewGaps, onGenerateCoverLetter }: SearchViewProps) {
  const { jobs, lowScoreJobs, running, notice: searchNotice, liveCount, diagnostics, setJobSaved, startSearch } = useSearch();
  const [resultGroup, setResultGroup] = useState<ResultGroup>("recommended");
  const [modality, setModality] = useState<"all" | "remote" | "hybrid">("all");
  const [scoreOrder, setScoreOrder] = useState<"desc" | "asc">("desc");
  const [localNotice, setLocalNotice] = useState<SearchNotice | null>(null);
  const [dismissed, setDismissed] = useState<Set<string>>(new Set());
  const [blacklistedCompanies, setBlacklistedCompanies] = useState<Set<string>>(new Set());
  const [savedJobIds, setSavedJobIds] = useState<Set<string>>(new Set());
  const [busyJob, setBusyJob] = useState<{ id: string; action: JobAction } | null>(null);
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null);
  const [plan, setPlan] = useState<SearchPlan | null>(null);
  const [planLoading, setPlanLoading] = useState(true);
  const [planError, setPlanError] = useState<string | null>(null);
  const [planAction, setPlanAction] = useState<PlanAction | null>(null);
  const recommendedTabRef = useRef<HTMLButtonElement>(null);
  const lowScoreTabRef = useRef<HTMLButtonElement>(null);

  const notice = localNotice ?? searchNotice;

  const refreshPlan = useCallback(async () => {
    setPlanLoading(true);
    setPlanError(null);
    try {
      setPlan(normalizeSearchPlan(await fetchSearchPlan()));
    } catch (error) {
      setPlanError(error instanceof ApiError ? error.message : "Nao foi possivel carregar o plano efetivo da busca.");
    } finally {
      setPlanLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshPlan();
  }, [refreshPlan]);

  useEffect(() => {
    setSavedJobIds((current) => {
      const next = new Set(current);
      for (const group of [jobs, lowScoreJobs]) {
        for (const job of group) {
          if (job.savedAt) next.add(job.id);
        }
      }
      return next;
    });
  }, [jobs, lowScoreJobs]);

  async function handlePlanAction(action: PlanAction) {
    if (!plan) return;
    setPlanAction(action);
    setPlanError(null);
    try {
      const config = await loadConfig();
      let next: AppConfig;
      switch (action) {
        case "use-simple-role": {
          const simpleRoles = (plan.ignoredRoles ?? []).filter(Boolean);
          next = {
            ...config,
            form: {
              ...config.form,
              searchProfiles: "",
              roles: simpleRoles.length > 0 ? simpleRoles.join(", ") : config.form.roles,
              role: simpleRoles[0] ?? config.form.role,
            },
          };
          break;
        }
        case "clear-profiles":
          next = { ...config, form: { ...config.form, searchProfiles: "" } };
          break;
        case "keep-keywords":
          next = { ...config, form: { ...config.form, keywordsForRoles: plan.roles.join(", ") } };
          break;
        case "clear-keywords":
          next = { ...config, form: { ...config.form, keywords: "", keywordsForRoles: "" } };
          break;
      }
      await saveConfig(next);
      setLocalNotice({ tone: "neutral", text: "Configuracao da busca atualizada." });
      await refreshPlan();
    } catch (error) {
      setPlanError(error instanceof ApiError ? error.message : "Nao foi possivel atualizar a configuracao da busca.");
    } finally {
      setPlanAction(null);
    }
  }

  async function handleStartSearch() {
    setResultGroup("recommended");
    setSelectedJobId(null);
    await refreshPlan();
    await startSearch();
  }

  async function handleOpen(job: JobSummary) {
    try {
      const result = await openJobUrl(job.url);
      setLocalNotice({ tone: "neutral", text: result.message });
    } catch (error) {
      setLocalNotice({
        tone: "error",
        text: error instanceof ApiError ? error.message : "Nao foi possivel abrir a vaga no navegador.",
      });
    }
  }

  async function persistJobAction(job: JobSummary, action: JobAction) {
    setBusyJob({ id: job.id, action });
    try {
      const result = await applyJobAction({ action, job });
      if (action === "blacklist") {
        setBlacklistedCompanies((current) => new Set(current).add(job.company.trim().toLowerCase()));
      } else if (action === "save" || action === "unsave") {
        setJobSaved(job.id, action === "save");
        setSavedJobIds((current) => {
          const next = new Set(current);
          if (action === "save") next.add(job.id);
          else next.delete(job.id);
          return next;
        });
      } else {
        setDismissed((current) => new Set(current).add(job.id));
      }
      const removesFromResults = action === "applied" || action === "dismiss" || action === "blacklist";
      if (removesFromResults && (selectedJobId === job.id || action === "blacklist")) {
        setSelectedJobId(null);
      }
      setLocalNotice({ tone: "neutral", text: result.message || `Vaga ${actionLabel(action)}.` });
    } catch (error) {
      setLocalNotice({
        tone: "error",
        text: error instanceof ApiError ? error.message : actionFailure(action),
      });
    } finally {
      setBusyJob(null);
    }
  }

  const actionableByGroup = useMemo(() => {
    const isActionable = (job: JobSummary) => !dismissed.has(job.id) && !blacklistedCompanies.has(job.company.trim().toLowerCase());
    return {
      recommended: jobs.filter(isActionable),
      lowScore: lowScoreJobs.filter(isActionable),
    };
  }, [blacklistedCompanies, dismissed, jobs, lowScoreJobs]);
  const actionableJobs = actionableByGroup[resultGroup];

  const visibleJobs = useMemo(() => actionableJobs
    .filter((job) => matchesModality(job, modality))
    .sort((a, b) => scoreOrder === "desc" ? b.score - a.score : a.score - b.score),
  [actionableJobs, modality, scoreOrder]);

  useEffect(() => {
    setSelectedJobId((current) => visibleJobs.some((job) => job.id === current) ? current : (visibleJobs[0]?.id ?? null));
  }, [visibleJobs]);

  const selectedJob = visibleJobs.find((job) => job.id === selectedJobId) ?? visibleJobs[0] ?? null;
  const sourceRows = diagnosticSourceRows(diagnostics);
  const suggestions = diagnostics?.suggestions?.filter(Boolean).slice(0, 4) ?? [];
  const scoredOffline = diagnostics?.scoredOffline ?? 0;
  const skippedByPrefilter = diagnostics?.skippedByPrefilter ?? 0;
  const searchReady = Boolean(plan && plan.roles.length > 0);
  const hasSearched = diagnostics !== null || jobs.length > 0 || lowScoreJobs.length > 0;
  const firstRunReady = !running && !hasSearched && notice?.tone !== "error";
  const filteredByModality = actionableJobs.length > 0 && visibleJobs.length === 0;
  const readyDescription = plan && searchReady
    ? `Vamos buscar ${plan.roles.join(" e ")} em ${plan.locations[0]?.location || "locais compativeis"} e ordenar pela compatibilidade com seu curriculo.`
    : "Configure ao menos um cargo para preparar sua primeira busca.";
  const resultCountLabel = running && visibleJobs.length === 0
    ? `Consultando ${plan?.sources.length ?? 0} fontes`
    : hasSearched || visibleJobs.length > 0
      ? `${visibleJobs.length} ${visibleJobs.length === 1 ? "vaga" : "vagas"}`
      : "Pronta para buscar";
  const showResultTabs = running || hasSearched;

  function handleResultTabKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    const next = event.key === "ArrowRight" || event.key === "End"
      ? "lowScore"
      : event.key === "ArrowLeft" || event.key === "Home"
        ? "recommended"
        : null;
    if (!next) return;
    event.preventDefault();
    setResultGroup(next);
    (next === "recommended" ? recommendedTabRef : lowScoreTabRef).current?.focus();
  }

  return (
    <section className="workspace search-workspace" aria-labelledby="search-title">
      <div className="workspace-header">
        <div>
          <h1 id="search-title">Vagas</h1>
          <p>Oportunidades compativeis com o seu curriculo.</p>
        </div>
        <button
          className="primary-button"
          type="button"
          aria-busy={running}
          aria-describedby="search-plan-title"
          disabled={running || planLoading || !searchReady}
          title={!planLoading && !searchReady ? "Configure ao menos um cargo antes de buscar." : undefined}
          onClick={() => void handleStartSearch()}
        >
          <Search className={running ? "is-spinning" : undefined} size={17} />
          {running ? "Buscando..." : hasSearched ? "Nova busca" : "Buscar vagas"}
        </button>
      </div>

      <SearchPlanPanel
        plan={plan}
        loading={planLoading}
        error={planError}
        busyAction={planAction}
        onAction={(action) => void handlePlanAction(action)}
        onAdjust={() => onOpenSettings?.("profile")}
      />

      <div className="toolbar" aria-label="Filtros da busca">
        {showResultTabs ? (
          <>
            <div className="segmented-control search-result-tabs" role="tablist" aria-label="Faixa de compatibilidade">
              <button
                ref={recommendedTabRef}
                id="recommended-jobs-tab"
                className={resultGroup === "recommended" ? "is-selected" : ""}
                type="button"
                role="tab"
                aria-selected={resultGroup === "recommended"}
                aria-controls="search-results-panel"
                tabIndex={resultGroup === "recommended" ? 0 : -1}
                onClick={() => setResultGroup("recommended")}
                onKeyDown={handleResultTabKeyDown}
              >
                Recomendadas ({actionableByGroup.recommended.length})
              </button>
              <button
                ref={lowScoreTabRef}
                id="low-score-jobs-tab"
                className={resultGroup === "lowScore" ? "is-selected" : ""}
                type="button"
                role="tab"
                aria-selected={resultGroup === "lowScore"}
                aria-controls="search-results-panel"
                tabIndex={resultGroup === "lowScore" ? 0 : -1}
                onClick={() => setResultGroup("lowScore")}
                onKeyDown={handleResultTabKeyDown}
              >
                Score baixo ({actionableByGroup.lowScore.length})
              </button>
            </div>
            <span className="toolbar-divider" />
          </>
        ) : null}
        <div className="segmented-control" aria-label="Modalidade">
          <button className={modality === "all" ? "is-selected" : ""} type="button" onClick={() => setModality("all")}>Todas</button>
          <button className={modality === "remote" ? "is-selected" : ""} type="button" onClick={() => setModality("remote")}>Remotas</button>
          <button className={modality === "hybrid" ? "is-selected" : ""} type="button" onClick={() => setModality("hybrid")}>Hibridas</button>
        </div>
        <span className="toolbar-divider" />
        <button className="tool-button" type="button" onClick={() => onOpenSettings?.("profile")}><Filter size={16} />Filtros</button>
        <button className="tool-button" type="button" onClick={() => setScoreOrder((current) => current === "desc" ? "asc" : "desc")}>
          <ArrowUpDown size={16} />
          Score {scoreOrder === "desc" ? "alto" : "baixo"}
        </button>
        <span className="result-count">{resultCountLabel}</span>
      </div>

      {notice ? <div className={`inline-notice ${notice.tone}`}>{notice.text}</div> : null}

      {/* Sits outside the empty state on purpose: a search that ran out of AI
          quota still returns jobs, and the user has to know those scores came
          from the cruder offline scorer rather than assume the app got worse. */}
      {scoredOffline > 0 ? (
        <div className="inline-notice warning">
          {scoredOffline} vaga(s) receberam uma estimativa offline
          {skippedByPrefilter > 0 ? `; ${skippedByPrefilter} ficaram fora do prefilter de IA` : ""}.
          {diagnostics?.aiConsentRequired
            ? " Autorize o processamento seguro em Configuracoes > Provedores de IA para ativar o score remoto; identificadores diretos serao removidos antes do envio."
            : diagnostics?.aiQuotaExhausted
            ? " A cota diaria de IA acabou; veja os Logs ou configure outro provider."
            : " Abra uma vaga para ver a origem exata do score e consulte os Logs para o motivo do fallback."}
        </div>
      ) : null}

      <div
        id="search-results-panel"
        className={`results-layout ${visibleJobs.length === 0 ? "is-empty" : ""} ${running ? "is-running" : ""}`}
        role={showResultTabs ? "tabpanel" : undefined}
        aria-labelledby={showResultTabs ? (resultGroup === "recommended" ? "recommended-jobs-tab" : "low-score-jobs-tab") : undefined}
      >
        <div className="results-list">
          {visibleJobs.length === 0 ? (
            <div className="empty-state">
              <span className="empty-icon">
                {running ? <LoaderCircle className="is-spinning" size={23} /> : <Search size={23} />}
              </span>
              <h2>{firstRunReady
                ? (searchReady ? "Tudo pronto para procurar" : "Configure sua busca")
                : filteredByModality
                  ? "Nenhuma vaga neste filtro"
                  : resultGroup === "lowScore" && !running
                    ? "Nenhuma vaga abaixo do corte"
                    : emptyTitle(running, diagnostics, Boolean(notice))}</h2>
              <p>{firstRunReady
                ? readyDescription
                : resultGroup === "lowScore" && !running && !filteredByModality
                  ? "As vagas analisadas abaixo do score minimo aparecem aqui para revisao manual."
                  : emptyDescription({ running, diagnostics, jobs: actionableJobs, liveCount, filteredByModality })}</p>
              {running && plan?.sources.length ? (
                <div className="search-running" aria-label="Fontes sendo consultadas">
                  <div className="search-running-sources">
                    {plan.sources.slice(0, 3).map((source) => <span key={source}>{source}</span>)}
                    {plan.sources.length > 3 ? <span>+{plan.sources.length - 3} fontes</span> : null}
                  </div>
                  <span className="search-running-track" aria-hidden="true"><i /></span>
                  <small>Voce pode navegar pelo aplicativo sem interromper a busca.</small>
                </div>
              ) : null}
              {!running && hasDiagnostics(diagnostics) ? (
                <div className="empty-diagnostics" aria-label="Diagnostico da busca">
                  <span>Sem descricao: {diagnostics?.skippedNoDescription ?? 0}</span>
                  <span>Descartadas: {diagnostics?.discarded ?? 0}</span>
                  <span>Bloqueadas: {diagnostics?.dropped ?? 0}</span>
                  {(diagnostics?.scoredFromCache ?? 0) > 0 ? (
                    <span>Reaproveitadas do cache: {diagnostics?.scoredFromCache}</span>
                  ) : null}
                </div>
              ) : null}
              {!running && sourceRows.length > 0 ? (
                <ul className="empty-source-list" aria-label="Resumo por fonte">
                  {sourceRows.map((row) => <li key={row}>{row}</li>)}
                </ul>
              ) : null}
              {!running && suggestions.length > 0 ? (
                <ul className="empty-suggestions" aria-label="Sugestoes para melhorar a busca">
                  {suggestions.map((suggestion) => <li key={suggestion}>{suggestion}</li>)}
                </ul>
              ) : null}
              {!running ? (
                <div className="empty-actions">
                  {firstRunReady && searchReady ? (
                    <button className="primary-button" type="button" onClick={() => void handleStartSearch()}>
                      <Search size={16} />
                      Buscar agora
                    </button>
                  ) : null}
                  <button className="secondary-button" type="button" onClick={() => onOpenSettings?.("profile")}>
                    <SlidersHorizontal size={16} />
                    Configurar busca
                  </button>
                </div>
              ) : null}
            </div>
          ) : (
            <div className="job-list approved-list" aria-label="Vagas encontradas">
              <div className="job-list-heading">
                <div>
                  <span>{resultGroup === "recommended" ? "Melhores oportunidades" : "Abaixo do corte"}</span>
                  <strong>{visibleJobs.length} {visibleJobs.length === 1 ? "vaga revisavel" : "vagas revisaveis"}</strong>
                </div>
                <small>{running ? "Atualizando agora" : "Ordenadas por compatibilidade"}</small>
              </div>
              {visibleJobs.map((job) => (
                <JobCard
                  job={job}
                  key={job.id}
                  selected={selectedJob?.id === job.id}
                  onSelect={() => setSelectedJobId(job.id)}
                />
              ))}
            </div>
          )}
        </div>
        {selectedJob ? (
          <div className="detail-panel">
            <JobDetailPanel
              job={selectedJob}
              onOpen={() => void handleOpen(selectedJob)}
              onMarkApplied={() => void persistJobAction(selectedJob, "applied")}
              onDismiss={() => void persistJobAction(selectedJob, "dismiss")}
              onBlacklist={() => void persistJobAction(selectedJob, "blacklist")}
              onToggleSave={() => void persistJobAction(selectedJob, savedJobIds.has(selectedJob.id) ? "unsave" : "save")}
              saved={savedJobIds.has(selectedJob.id)}
              busyAction={busyJob?.id === selectedJob.id ? busyJob.action : null}
              onOptimizeResume={onOptimizeResume}
              onViewGaps={onViewGaps}
              onGenerateCoverLetter={onGenerateCoverLetter}
            />
          </div>
        ) : null}
      </div>
    </section>
  );
}

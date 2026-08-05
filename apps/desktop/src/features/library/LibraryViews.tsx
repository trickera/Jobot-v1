import {
  Bookmark,
  BriefcaseBusiness,
  CalendarClock,
  ExternalLink,
  History,
  LoaderCircle,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { MatchGauge } from "../../components/MatchGauge";
import { SourceBadge, formatJobMeta } from "../../components/SourceBadge";
import { ApiError, applyJobAction, loadApplications, loadSavedJobs, loadSearchHistory, openJobUrl } from "../../services/api";
import type { Application, JobSummary, SearchHistoryEntry } from "../../types";
import "./LibraryViews.css";

function formatTimestamp(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("pt-BR", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

type LibraryEmptyProps = {
  icon: typeof Bookmark;
  title: string;
  detail: string;
  loading?: boolean;
};

function LibraryEmpty({ icon: Icon, title, detail, loading = false }: LibraryEmptyProps) {
  return (
    <div className="empty-state library-empty precision-library-state" role={loading ? "status" : undefined}>
      <span className="empty-icon">
        <Icon className={loading ? "is-spinning" : undefined} size={22} />
      </span>
      <h2>{title}</h2>
      <p>{detail}</p>
    </div>
  );
}

function RefreshButton({ loading, onClick }: { loading: boolean; onClick: () => void }) {
  return (
    <button
      className="secondary-button precision-refresh-button"
      type="button"
      disabled={loading}
      aria-busy={loading}
      onClick={onClick}
    >
      <RefreshCw className={loading ? "is-spinning" : undefined} size={15} />
      {loading ? "Atualizando" : "Atualizar"}
    </button>
  );
}

function LibraryHeader({
  id,
  eyebrow,
  title,
  description,
  loading,
  onRefresh,
}: {
  id: string;
  eyebrow: string;
  title: string;
  description: string;
  loading: boolean;
  onRefresh: () => void;
}) {
  return (
    <header className="workspace-header precision-library-header">
      <div>
        <span className="precision-library-eyebrow">{eyebrow}</span>
        <h1 id={id}>{title}</h1>
        <p>{description}</p>
      </div>
      <RefreshButton loading={loading} onClick={onRefresh} />
    </header>
  );
}

function JobIdentity({ job }: { job: JobSummary }) {
  return (
    <div className="precision-library-job">
      <div className="precision-library-job-copy">
        <SourceBadge source={job.source} size="sm" />
        <h2>{job.title}</h2>
        <p>{formatJobMeta(job.company, job.location)}</p>
      </div>
      <MatchGauge pending={Boolean(job.scoringPending)} score={job.score} size="sm" />
    </div>
  );
}

export function SavedJobsView() {
  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await loadSavedJobs();
      setJobs(result.jobs ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Nao foi possivel carregar as vagas salvas.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  async function open(job: JobSummary) {
    try {
      await openJobUrl(job.url);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Nao foi possivel abrir a vaga.");
    }
  }

  async function unsave(job: JobSummary) {
    setBusyId(job.id);
    setError(null);
    try {
      await applyJobAction({ action: "unsave", job });
      setJobs((current) => current.filter((item) => item.id !== job.id));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Nao foi possivel remover a vaga das salvas.");
    } finally {
      setBusyId(null);
    }
  }

  return (
    <section className="workspace library-workspace precision-library" aria-labelledby="saved-title">
      <LibraryHeader
        id="saved-title"
        eyebrow="Sua selecao"
        title="Vagas salvas"
        description="Revise oportunidades guardadas antes de se candidatar."
        loading={loading}
        onRefresh={() => void refresh()}
      />
      {error ? <div className="inline-notice error" role="alert">{error}</div> : null}
      <div className="precision-library-content">
        {loading && jobs.length === 0 ? (
          <LibraryEmpty icon={LoaderCircle} title="Carregando vagas salvas..." detail="Consultando o banco local." loading />
        ) : null}
        {!loading && !error && jobs.length === 0 ? (
          <LibraryEmpty icon={Bookmark} title="Nenhuma vaga salva" detail="Use o marcador nos resultados da busca para guardar uma vaga aqui." />
        ) : null}
        {jobs.length > 0 ? (
          <>
            <div className="precision-library-section-heading">
              <div>
                <span>Para revisar</span>
                <strong>{jobs.length} {jobs.length === 1 ? "vaga" : "vagas"}</strong>
              </div>
              {loading ? <LoaderCircle className="is-spinning" aria-label="Atualizando vagas salvas" size={16} /> : null}
            </div>
            <div className="library-grid precision-library-grid" aria-label="Vagas salvas">
              {jobs.map((job) => (
                <article className="library-card precision-library-card" key={job.id}>
                  <JobIdentity job={job} />
                  <div className="precision-library-card-footnote">
                    <CalendarClock size={13} aria-hidden="true" />
                    <span>Salva em {formatTimestamp(job.savedAt ?? "")}</span>
                  </div>
                  <div className="library-card-actions precision-library-actions">
                    <button className="primary-button" type="button" onClick={() => void open(job)}>
                      <ExternalLink size={15} />Abrir vaga
                    </button>
                    <button
                      className="job-muted-button precision-library-remove"
                      type="button"
                      disabled={busyId === job.id}
                      aria-label={`Remover ${job.title} das salvas`}
                      onClick={() => void unsave(job)}
                    >
                      {busyId === job.id ? <LoaderCircle className="is-spinning" size={14} /> : <Trash2 size={14} />}
                      Remover
                    </button>
                  </div>
                </article>
              ))}
            </div>
          </>
        ) : null}
      </div>
    </section>
  );
}

export function ApplicationsView() {
  const [items, setItems] = useState<Application[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await loadApplications();
      setItems(result.applications ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Nao foi possivel carregar as candidaturas.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  async function open(job: JobSummary) {
    try {
      await openJobUrl(job.url);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Nao foi possivel abrir a vaga.");
    }
  }

  return (
    <section className="workspace library-workspace precision-library" aria-labelledby="applications-title">
      <LibraryHeader
        id="applications-title"
        eyebrow="Acompanhamento"
        title="Candidaturas"
        description="Acompanhe as vagas que voce marcou como aplicadas."
        loading={loading}
        onRefresh={() => void refresh()}
      />
      {error ? <div className="inline-notice error" role="alert">{error}</div> : null}
      <div className="precision-library-content">
        {loading && items.length === 0 ? (
          <LibraryEmpty icon={LoaderCircle} title="Carregando candidaturas..." detail="Consultando o banco local." loading />
        ) : null}
        {!loading && !error && items.length === 0 ? (
          <LibraryEmpty icon={BriefcaseBusiness} title="Nenhuma candidatura registrada" detail="Marque uma vaga como aplicada para acompanhar o registro aqui." />
        ) : null}
        {items.length > 0 ? (
          <>
            <div className="precision-library-section-heading">
              <div>
                <span>Registro local</span>
                <strong>{items.length} {items.length === 1 ? "candidatura" : "candidaturas"}</strong>
              </div>
              {loading ? <LoaderCircle className="is-spinning" aria-label="Atualizando candidaturas" size={16} /> : null}
            </div>
            <div className="library-grid precision-library-grid" aria-label="Candidaturas registradas">
              {items.map((item) => (
                <article className="library-card precision-library-card" key={item.id}>
                  <JobIdentity job={item.job} />
                  <div className="library-card-meta precision-library-card-meta">
                    <span className="precision-status-chip">{item.status}</span>
                    <small>Atualizada em {formatTimestamp(item.updatedAt)}</small>
                  </div>
                  <div className="library-card-actions precision-library-actions">
                    <button className="primary-button" type="button" onClick={() => void open(item.job)}>
                      <ExternalLink size={15} />Abrir vaga
                    </button>
                  </div>
                </article>
              ))}
            </div>
          </>
        ) : null}
      </div>
    </section>
  );
}

function historyDetails(item: SearchHistoryEntry) {
  const details: string[] = [];
  const workMode = item.filters.workMode;
  const location = item.filters.location;
  const recentHours = item.filters.recentHours;
  if (typeof workMode === "string" && workMode) details.push(workMode);
  if (typeof location === "string" && location) details.push(location);
  if (typeof recentHours === "number") details.push(`ultimas ${recentHours}h`);
  return details;
}

export function HistoryView() {
  const [items, setItems] = useState<SearchHistoryEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await loadSearchHistory();
      setItems(result.history ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Nao foi possivel carregar o historico.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  return (
    <section className="workspace library-workspace precision-library" aria-labelledby="history-title">
      <LibraryHeader
        id="history-title"
        eyebrow="Atividade"
        title="Historico"
        description="Consulte as buscas executadas e os resultados encontrados."
        loading={loading}
        onRefresh={() => void refresh()}
      />
      {error ? <div className="inline-notice error" role="alert">{error}</div> : null}
      <div className="precision-library-content">
        {loading && items.length === 0 ? (
          <LibraryEmpty icon={LoaderCircle} title="Carregando historico..." detail="Consultando o banco local." loading />
        ) : null}
        {!loading && !error && items.length === 0 ? (
          <LibraryEmpty icon={History} title="Nenhuma busca registrada" detail="Execute uma busca com Salvar historico habilitado nas configuracoes." />
        ) : null}
        {items.length > 0 ? (
          <>
            <div className="precision-library-section-heading">
              <div>
                <span>Buscas recentes</span>
                <strong>{items.length} {items.length === 1 ? "registro" : "registros"}</strong>
              </div>
              {loading ? <LoaderCircle className="is-spinning" aria-label="Atualizando historico" size={16} /> : null}
            </div>
            <div className="history-list precision-history-list" aria-label="Historico de buscas">
              {items.map((item, index) => {
                const details = historyDetails(item);
                return (
                  <article className="history-card precision-history-card" key={item.id}>
                    <div className="precision-history-index" aria-hidden="true">{String(index + 1).padStart(2, "0")}</div>
                    <div className="precision-history-query">
                      <span>Busca</span>
                      <h2>{item.query || "Sem cargo informado"}</h2>
                      <p>{details.join(" / ") || "Filtros padrao"}</p>
                    </div>
                    <div className="history-card-result precision-history-result">
                      <strong>{item.resultsCount}</strong>
                      <span>{item.resultsCount === 1 ? "vaga" : "vagas"}</span>
                      <small>{formatTimestamp(item.createdAt)}</small>
                    </div>
                  </article>
                );
              })}
            </div>
          </>
        ) : null}
      </div>
    </section>
  );
}

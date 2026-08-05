import { Ban, Bookmark, CheckCircle2, CircleHelp, ExternalLink, FileText, LoaderCircle, Mail, Search, X } from "lucide-react";
import { MatchGauge } from "../../components/MatchGauge";
import { SourceBadge, formatJobMeta } from "../../components/SourceBadge";
import type { JobAction, JobSummary } from "../../types";
import { formatDescriptionBody, parseDescriptionSections } from "./jobDescription";

type JobDetailPanelProps = {
  job: JobSummary;
  onOpen: () => void;
  onDismiss: () => void;
  onMarkApplied: () => void;
  onBlacklist: () => void;
  onToggleSave?: () => void;
  saved?: boolean;
  busyAction: JobAction | null;
  // Resume Studio integration (Task 16) — all optional so JobDetailPanel
  // keeps working standalone (e.g. in tests) without these wired up.
  onOptimizeResume?: (job: JobSummary) => void;
  onViewGaps?: (job: JobSummary) => void;
  onGenerateCoverLetter?: (job: JobSummary) => void;
};

function statusClass(status: string) {
  const normalized = status.toLowerCase();
  if (normalized.includes("scoring")) return "pending";
  if (normalized.includes("apply")) return "apply";
  if (normalized.includes("adjust")) return "adjust";
  return "discard";
}

export { MatchGauge } from "../../components/MatchGauge";

function scoreVerdict(job: JobSummary) {
  if (job.scoringPending) return "Calculando compatibilidade";
  if (job.score >= 90) return "Excelente encaixe";
  if (job.score >= 75) return "Boa compatibilidade";
  if (job.score >= 60) return "Compatibilidade moderada";
  return "Revisao recomendada";
}

export function scoreSourceCopy(job: JobSummary) {
  if (job.scoringPending) {
    return "AI scoring pending — this collected job is visible while its description and final score are processed.";
  }
  const reason = job.scoreReason?.trim();
  let label: string;
  switch (job.scoreSource) {
    case "ai":
      label = `AI score ${job.score}`;
      break;
    case "ai_cache":
      label = `Cached AI score ${job.score}`;
      break;
    case "offline_fallback":
      label = `Offline estimate ${job.score}`;
      break;
    case "offline_prefilter":
    case "offline_no_key":
      label = `Not AI-scored — offline estimate ${job.score}`;
      break;
    default:
      label = `Score ${job.score} — source unavailable`;
  }
  return reason ? `${label}. ${reason}` : `${label}.`;
}

export function JobDetailPanel({
  job,
  onOpen,
  onDismiss,
  onMarkApplied,
  onBlacklist,
  onToggleSave,
  saved = false,
  busyAction,
  onOptimizeResume,
  onViewGaps,
  onGenerateCoverLetter,
}: JobDetailPanelProps) {
  const missing = job.missingKeywords?.filter(Boolean) ?? [];
  const description = job.description?.trim() ?? "";
  const sections = parseDescriptionSections(description);
  const cls = statusClass(job.status);
  const pending = Boolean(job.scoringPending);

  return (
    <article className={`job-detail-pane ${cls}`}>
      <header className="job-detail-header">
        <div className="job-detail-header-top">
          <SourceBadge source={job.source} size="md" />
          <span className="job-detail-status">{pending ? "Analisando score" : (job.status || "Pronta para revisar")}</span>
        </div>
        <h2>{job.title}</h2>
        <p className="job-detail-meta">{formatJobMeta(job.company, job.location)}</p>
        {job.profile ? <span className="job-detail-profile">Perfil: {job.profile}</span> : null}
        <div className="job-detail-primary-actions">
          <button className="primary-button" type="button" onClick={onOpen}>
            <ExternalLink size={15} />
            Candidatar no navegador
          </button>
          {onToggleSave ? (
            <button className={`job-action-button ${saved ? "is-saved" : ""}`} type="button" disabled={pending || busyAction !== null} onClick={onToggleSave}>
              {busyAction === "save" || busyAction === "unsave" ? <LoaderCircle className="is-spinning" size={14} /> : <Bookmark size={14} fill={saved ? "currentColor" : "none"} />}
              {saved ? "Salva" : "Salvar"}
            </button>
          ) : null}
        </div>
      </header>

      <div className="job-detail-body">
        <section className="job-match-summary" aria-label="Resumo da compatibilidade">
          <MatchGauge score={job.score} pending={pending} size="lg" />
          <div>
            <span>Compatibilidade</span>
            <strong>{scoreVerdict(job)}</strong>
            <p>{scoreSourceCopy(job)}</p>
          </div>
        </section>

        <div className="job-detail-insights">
          <section>
            <div className="job-detail-section-title">
              <h3>Analise do score</h3>
              <button className="job-detail-help" type="button" aria-label="Como esta nota foi calculada" aria-describedby="score-help-copy">
                <CircleHelp size={14} />
                <span id="score-help-copy" role="tooltip">A nota compara os requisitos da vaga com as evidencias presentes no seu curriculo. A origem exata aparece no resumo acima.</span>
              </button>
            </div>
            <p>
              {job.profile
                ? <>Esta vaga foi encontrada pelo perfil <strong>{job.profile}</strong> e comparada ao seu curriculo atual.</>
                : "A vaga foi comparada ao curriculo e aos criterios atuais da busca."}
            </p>
          </section>

          <section>
            <div className="job-detail-section-title">
              <h3>Antes de se candidatar</h3>
              <button className="job-detail-help" type="button" aria-label="Por que revisar estes pontos" aria-describedby="gap-help-copy">
                <CircleHelp size={14} />
                <span id="gap-help-copy" role="tooltip">Estes itens nao foram comprovados no curriculo. Confirme que sao verdadeiros antes de editar qualquer documento.</span>
              </button>
            </div>
            {!pending && missing.length > 0 ? (
              <div className="cv-tweak">
                <strong>Requirements not evidenced on your resume</strong>
                <span>{missing.join(", ")}</span>
                <small>Verify each item is true before adding it. Unsupported skills must not be inserted.</small>
              </div>
            ) : !pending ? (
              <p className="job-detail-muted">Nenhum gap critico de keyword identificado para esta vaga.</p>
            ) : (
              <p className="job-detail-muted">Os pontos de revisao aparecem quando o score terminar.</p>
            )}
          </section>
        </div>

        <div className="job-detail-secondary-actions" aria-label="Acoes da vaga">
          {onOptimizeResume ? (
            <button className="job-action-button" type="button" disabled={pending} onClick={() => onOptimizeResume(job)}>
              <FileText size={14} />
              Abrir no Resume Studio
            </button>
          ) : null}
          <button className="job-action-button success" type="button" disabled={pending || busyAction !== null} onClick={onMarkApplied}>
            {busyAction === "applied" ? <LoaderCircle className="is-spinning" size={14} /> : <CheckCircle2 size={14} />}
            Marcar aplicada
          </button>
          <button className="job-action-button" type="button" disabled={pending || busyAction !== null} onClick={onDismiss}>
            {busyAction === "dismiss" ? <LoaderCircle className="is-spinning" size={14} /> : <X size={14} />}
            Dispensar
          </button>
          <button className="job-action-button danger" type="button" disabled={pending || busyAction !== null} onClick={onBlacklist}>
            {busyAction === "blacklist" ? <LoaderCircle className="is-spinning" size={14} /> : <Ban size={14} />}
            Bloquear empresa
          </button>
          {onViewGaps ? (
            <button className="job-action-button" type="button" disabled={pending} onClick={() => onViewGaps(job)}>
              <Search size={14} />
              Ver gaps ATS
            </button>
          ) : null}
          {onGenerateCoverLetter ? (
            <button className="job-action-button" type="button" disabled={pending} onClick={() => onGenerateCoverLetter(job)}>
              <Mail size={14} />
              Gerar cover letter
            </button>
          ) : null}
        </div>

        {description ? (
          sections.map((section) => (
            <section className="job-detail-section" key={section.title}>
              <h3>{section.title}</h3>
              <div className="job-detail-prose">{formatDescriptionBody(section.body)}</div>
            </section>
          ))
        ) : (
          <section className="job-detail-section">
            <h3>Sobre a vaga</h3>
            <p className="job-detail-muted">
              Descricao nao disponivel nesta coleta. Abra a vaga no navegador para ver o texto completo.
            </p>
          </section>
        )}

        <section className="job-detail-section job-detail-section--link">
          <h3>Link da vaga</h3>
          <a className="job-detail-link" href={job.url} onClick={(event) => { event.preventDefault(); onOpen(); }}>
            {job.url}
          </a>
        </section>
      </div>
    </article>
  );
}

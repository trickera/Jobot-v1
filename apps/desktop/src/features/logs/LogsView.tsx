import { AlertCircle, LoaderCircle, RefreshCw, TerminalSquare } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { ApiError, loadLogs } from "../../services/api";
import type { LogEntry } from "../../types";
import "./LogsView.css";

function formatLogTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("pt-BR", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date);
}

const levelLabel: Record<LogEntry["level"], string> = {
  info: "info",
  success: "ok",
  warning: "aviso",
  error: "erro",
  muted: "sistema",
};

export function LogsView() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refreshLogs = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await loadLogs();
      setLogs(result.logs ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Nao foi possivel carregar os logs.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshLogs();
    const timer = window.setInterval(() => void refreshLogs(), 3000);
    return () => window.clearInterval(timer);
  }, [refreshLogs]);

  return (
    <section className="workspace logs-workspace precision-logs" aria-labelledby="logs-title">
      <header className="workspace-header precision-logs-header">
        <div>
          <span className="precision-logs-eyebrow">Diagnostico em tempo real</span>
          <h1 id="logs-title">Logs</h1>
          <p>Execucao da pipeline, fontes, filtros e analise.</p>
        </div>
        <button
          className="secondary-button precision-logs-refresh"
          type="button"
          disabled={loading}
          aria-busy={loading}
          onClick={() => void refreshLogs()}
        >
          <RefreshCw className={loading ? "is-spinning" : undefined} size={15} />
          {loading ? "Atualizando" : "Atualizar"}
        </button>
      </header>

      {error ? (
        <div className="inline-notice error precision-logs-error" role="alert">
          <AlertCircle size={15} aria-hidden="true" />
          <span>{error}</span>
        </div>
      ) : null}

      <div className="precision-logs-content">
        <div className="precision-logs-console-heading">
          <div>
            <span className="precision-console-dot" />
            <strong>Pipeline local</strong>
          </div>
          <span aria-live="polite" role="status">{logs.length} {logs.length === 1 ? "evento" : "eventos"}</span>
        </div>

        <div className="log-console precision-log-console" aria-busy={loading}>
          {loading && logs.length === 0 ? (
            <div className="empty-state precision-log-state" role="status">
              <span className="empty-icon"><LoaderCircle className="is-spinning" size={22} /></span>
              <h2>Carregando atividade...</h2>
              <p>Conectando ao registro local da pipeline.</p>
            </div>
          ) : null}

          {!loading && !error && logs.length === 0 ? (
            <div className="empty-state precision-log-state">
              <span className="empty-icon"><TerminalSquare size={22} /></span>
              <h2>Nenhum log ainda</h2>
              <p>Inicie uma busca para acompanhar cada etapa da pipeline.</p>
            </div>
          ) : null}

          {!loading && error && logs.length === 0 ? (
            <div className="empty-state precision-log-state precision-log-unavailable">
              <span className="empty-icon"><AlertCircle size={22} /></span>
              <h2>Atividade indisponivel</h2>
              <p>Use Atualizar para tentar reconectar ao registro local.</p>
            </div>
          ) : null}

          {logs.map((entry) => (
            <div className={`log-line precision-log-line ${entry.level}`} key={entry.id}>
              <time dateTime={entry.ts}>{formatLogTime(entry.ts)}</time>
              <span className="precision-log-level">{levelLabel[entry.level]}</span>
              <span className="precision-log-message">{entry.message}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

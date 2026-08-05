import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ApiError, fetchBrowserHealth, fetchSearchStatus, startBackgroundSearch } from "../services/api";
import type { JobSummary, SearchDiagnostics } from "../types";

type SearchNotice = { tone: "neutral" | "error"; text: string };

type SearchContextValue = {
  jobs: JobSummary[];
  lowScoreJobs: JobSummary[];
  running: boolean;
  notice: SearchNotice | null;
  liveCount: number;
  diagnostics: SearchDiagnostics | null;
  setJobSaved: (jobId: string, saved: boolean) => void;
  startSearch: (options?: { reset?: boolean }) => Promise<void>;
};

const SearchContext = createContext<SearchContextValue | null>(null);

function asJobList(value: unknown): JobSummary[] {
  return Array.isArray(value) ? value : [];
}

export function SearchProvider({ children }: { children: ReactNode }) {
  const pollRef = useRef<number | null>(null);
  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [lowScoreJobs, setLowScoreJobs] = useState<JobSummary[]>([]);
  const [running, setRunning] = useState(false);
  const [notice, setNotice] = useState<SearchNotice | null>(null);
  const [liveCount, setLiveCount] = useState(0);
  const [diagnostics, setDiagnostics] = useState<SearchDiagnostics | null>(null);

  const setJobSaved = useCallback((jobId: string, saved: boolean) => {
    const savedAt = saved ? new Date().toISOString() : undefined;
    const update = (items: JobSummary[]) => items.map((job) => job.id === jobId ? { ...job, savedAt } : job);
    setJobs(update);
    setLowScoreJobs(update);
  }, []);

  const stopPolling = useCallback(() => {
    if (pollRef.current != null) {
      window.clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  const applyStatus = useCallback((status: Awaited<ReturnType<typeof fetchSearchStatus>>) => {
    const nextJobs = asJobList(status.jobs);
    const nextLowScoreJobs = asJobList(status.lowScoreJobs);
    setJobs(nextJobs);
    setLowScoreJobs(nextLowScoreJobs);
    setLiveCount(status.total ?? nextJobs.length);
    setRunning(Boolean(status.running));
    setDiagnostics(status.diagnostics ?? null);
    if (status.error) {
      setNotice({ tone: "error", text: status.error });
      return;
    }
    if (status.running) {
      setNotice({ tone: "neutral", text: status.message || "Buscando vagas..." });
      return;
    }
    // The message used to be shown only when jobs came back, which threw away
    // the one case where the user most needs it: a search that ends with nothing
    // BECAUSE a source was blocked. That outcome would clear the notice entirely
    // and leave an empty screen with no explanation.
    if (status.message) {
      const blocked = Object.values(status.diagnostics?.sources ?? {}).some((source) => source.blocked);
      setNotice({ tone: blocked ? "error" : "neutral", text: status.message });
    } else if (!status.running) {
      setNotice(null);
    }
  }, []);

  const pollOnce = useCallback(async () => {
    try {
      const status = await fetchSearchStatus();
      applyStatus(status);
      if (!status.running) {
        stopPolling();
      }
    } catch {
      // keep last known UI state while backend is busy
    }
  }, [applyStatus, stopPolling]);

  const startPolling = useCallback((immediate = true) => {
    stopPolling();
    pollRef.current = window.setInterval(() => {
      void pollOnce();
    }, 1000);
    if (immediate) {
      void pollOnce();
    }
  }, [pollOnce, stopPolling]);

  useEffect(() => {
    let disposed = false;

    void (async () => {
      try {
        const status = await fetchSearchStatus();
        if (disposed) {
          return;
        }
        applyStatus(status);
        if (status.running) {
          startPolling(false);
        }
      } catch {
        // fresh session starts empty
      }
    })();

    return () => {
      disposed = true;
      stopPolling();
    };
  }, [applyStatus, startPolling, stopPolling]);

  const startSearch = useCallback(async (options?: { reset?: boolean }) => {
    if (running) {
      return;
    }

    try {
      const health = await fetchBrowserHealth();
      if (!health.pythonFound) {
        setNotice({
          tone: "error",
          text: `${health.message} Veja mais detalhes na aba Logs ou em Configuracoes > Fontes de vagas.`,
        });
        return;
      }
    } catch {
      // The health check itself failing shouldn't block a search attempt -
      // let the real search surface its own error instead of guessing.
    }

    if (options?.reset !== false) {
      setJobs([]);
      setLowScoreJobs([]);
      setLiveCount(0);
      setDiagnostics(null);
    }
    setNotice({ tone: "neutral", text: "Iniciando busca..." });

    try {
      await startBackgroundSearch();
      setRunning(true);
      startPolling();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setRunning(true);
        startPolling();
        return;
      }
      setNotice({
        tone: "error",
        text: error instanceof ApiError ? error.message : "O servico de busca nao esta disponivel.",
      });
      setRunning(false);
    }
  }, [running, startPolling]);

  const value = useMemo(
    () => ({ jobs, lowScoreJobs, running, notice, liveCount, diagnostics, setJobSaved, startSearch }),
    [diagnostics, jobs, liveCount, lowScoreJobs, notice, running, setJobSaved, startSearch],
  );

  return <SearchContext.Provider value={value}>{children}</SearchContext.Provider>;
}

export function useSearch() {
  const context = useContext(SearchContext);
  if (!context) {
    throw new Error("useSearch must be used within SearchProvider");
  }
  return context;
}

import { Download, LoaderCircle, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, fetchBrowserBootstrapStatus, fetchBrowserHealth, startBrowserBootstrap } from "../../services/api";
import type { BrowserHealthResponse } from "../../types";

// BrowserWorkerStatus surfaces whether the Camoufox scraping transport is
// ready (CH-01/D9) so a broken commercial install (missing bundled Python,
// browser not yet downloaded) is diagnosable from Settings instead of
// showing up as an opaque search failure.
export function BrowserWorkerStatus() {
  const [health, setHealth] = useState<BrowserHealthResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [installing, setInstalling] = useState(false);
  const [installMessage, setInstallMessage] = useState<string | null>(null);
  const pollRef = useRef<number | null>(null);

  const stopPolling = useCallback(() => {
    if (pollRef.current != null) {
      window.clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  const loadHealth = useCallback(async () => {
    setLoading(true);
    try {
      setHealth(await fetchBrowserHealth());
    } catch {
      setHealth(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadHealth();
    return stopPolling;
  }, [loadHealth, stopPolling]);

  async function handleInstall() {
    setInstalling(true);
    setInstallMessage("Iniciando instalacao...");
    try {
      await startBrowserBootstrap();
    } catch (error) {
      if (!(error instanceof ApiError && error.status === 409)) {
        setInstallMessage(error instanceof ApiError ? error.message : "Nao foi possivel iniciar a instalacao.");
        setInstalling(false);
        return;
      }
    }

    stopPolling();
    pollRef.current = window.setInterval(() => {
      void fetchBrowserBootstrapStatus()
        .then((status) => {
          setInstallMessage(status.message);
          if (status.done) {
            stopPolling();
            setInstalling(false);
            void loadHealth();
          }
        })
        .catch(() => {
          // keep polling - a transient failure shouldn't stop the check
        });
    }, 2000);
  }

  if (loading) {
    return <p className="settings-alert">Verificando worker do navegador...</p>;
  }

  if (!health) {
    return <p className="settings-alert error">Nao foi possivel verificar o worker do navegador.</p>;
  }

  const canInstall = health.pythonFound && health.workerFound && health.camoufoxImportable && !health.browserInstalled;
  const alertTone = health.browserInstalled && health.camoufoxImportable ? "success" : health.pythonFound ? "" : "error";

  return (
    <div className="browser-worker-status">
      <div className="browser-worker-status-head">
        <strong>Worker do navegador (Camoufox)</strong>
        <button type="button" className="secondary-button browser-worker-refresh" onClick={() => void loadHealth()}>
          <RefreshCw size={14} />
          Atualizar
        </button>
      </div>
      <ul className="browser-worker-checklist">
        <li className={health.pythonFound ? "is-ok" : "is-fail"}>Python: {health.pythonFound ? "encontrado" : "nao encontrado"}</li>
        <li className={health.workerFound ? "is-ok" : "is-fail"}>Script do worker: {health.workerFound ? "encontrado" : "nao encontrado"}</li>
        <li className={health.camoufoxImportable ? "is-ok" : "is-fail"}>Camoufox: {health.camoufoxImportable ? "disponivel" : "indisponivel"}</li>
        <li className={health.browserInstalled ? "is-ok" : "is-fail"}>Navegador: {health.browserInstalled ? "instalado" : "nao instalado"}</li>
      </ul>
      <p className={`settings-alert ${alertTone}`}>{installMessage ?? health.message}</p>
      {canInstall ? (
        <button className="secondary-button" type="button" disabled={installing} onClick={() => void handleInstall()}>
          {installing ? <LoaderCircle className="is-spinning" size={14} /> : <Download size={14} />}
          {installing ? "Instalando..." : "Instalar Camoufox"}
        </button>
      ) : null}
    </div>
  );
}

import { FolderOpen, LoaderCircle, RefreshCw, Wrench } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { fetchInstallHealth, runInstallRepair } from "../../services/api";
import { isElectron } from "../../services/runtime";
import type { InstallHealthResponse } from "../../types";

// InstallHealthPanel surfaces the installer hardening health/repair contract
// (docs/SONNET5-INSTALLER-HARDENING-PLAN.md Phase 2): every local piece the
// app depends on - backend, local DB, bundled Python, browser worker,
// Camoufox import/browser, AppData/cache write permission, internet - in one
// place, with a safe repair action, instead of a first search silently
// failing deep in the pipeline on a machine that never had these validated.
export function InstallHealthPanel() {
  const [health, setHealth] = useState<InstallHealthResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [repairing, setRepairing] = useState(false);
  const [repairMessage, setRepairMessage] = useState<string | null>(null);

  const loadHealth = useCallback(async () => {
    setLoading(true);
    try {
      setHealth(await fetchInstallHealth());
    } catch {
      setHealth(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadHealth();
  }, [loadHealth]);

  async function handleRepair() {
    setRepairing(true);
    setRepairMessage("Executando reparo...");
    try {
      const result = await runInstallRepair();
      setRepairMessage(result.message);
    } catch {
      setRepairMessage("Nao foi possivel executar o reparo agora.");
    } finally {
      setRepairing(false);
      await loadHealth();
    }
  }

  if (loading) {
    return <p className="settings-alert">Verificando instalacao...</p>;
  }

  if (!health) {
    return <p className="settings-alert error">Nao foi possivel verificar o estado da instalacao.</p>;
  }

  const alertTone = health.ok ? "success" : "error";

  return (
    <div className="browser-worker-status">
      <div className="browser-worker-status-head">
        <strong>Instalacao {health.packaged ? "" : "(modo desenvolvimento)"}</strong>
        <button type="button" className="secondary-button browser-worker-refresh" onClick={() => void loadHealth()}>
          <RefreshCw size={14} />
          Atualizar
        </button>
      </div>
      <ul className="browser-worker-checklist">
        {health.checks.map((check) => (
          <li key={check.id} className={check.ok ? "is-ok" : "is-fail"}>
            {check.label}: {check.ok ? "ok" : "com problema"}
            {check.note ? ` (${check.note})` : ""}
          </li>
        ))}
      </ul>
      <p className={`settings-alert ${alertTone}`}>{repairMessage ?? health.message}</p>
      <div className="settings-inline-action">
        {health.repairAvailable ? (
          <button className="secondary-button" type="button" disabled={repairing} onClick={() => void handleRepair()}>
            {repairing ? <LoaderCircle className="is-spinning" size={14} /> : <Wrench size={14} />}
            {repairing ? "Reparando..." : "Reparar instalacao"}
          </button>
        ) : null}
        {isElectron() ? (
          <button
            className="secondary-button"
            type="button"
            onClick={() => void window.senciaElectron?.openAppDataFolder()}
          >
            <FolderOpen size={14} />
            Abrir pasta de dados
          </button>
        ) : null}
      </div>
    </div>
  );
}

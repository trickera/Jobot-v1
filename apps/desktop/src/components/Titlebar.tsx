import { Maximize2, Minus, Moon, Sun, X } from "lucide-react";
import { useEffect, useState } from "react";

type WindowAction = "minimize" | "maximize" | "close";

function controlWindow(action: WindowAction) {
  window.senciaElectron!.windowControl(action);
}

type AppTheme = "dark" | "light";
const THEME_STORAGE_KEY = "sencia-theme";

function documentTheme(): AppTheme {
  return document.documentElement.dataset.theme === "light" ? "light" : "dark";
}

function applyTheme(theme: AppTheme) {
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", theme === "light" ? "#F5F6F8" : "#0A0B0D");
}

export function Titlebar({ online, busy }: { online: boolean; busy?: boolean }) {
  const label = busy ? "Buscando..." : online ? "Pronto" : "Servico offline";
  const [theme, setTheme] = useState<AppTheme>(documentTheme);

  useEffect(() => applyTheme(theme), [theme]);

  function toggleTheme() {
    const next = theme === "dark" ? "light" : "dark";
    document.documentElement.classList.add("is-theme-changing");
    setTheme(next);
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, next);
    } catch {
      // The visual toggle still works when storage is unavailable.
    }
    window.setTimeout(() => document.documentElement.classList.remove("is-theme-changing"), 220);
  }

  return (
    <header className="titlebar">
      <div className="brand" title={label}>
        <span
          className={`status-dot ${online ? "is-online" : ""} ${busy ? "is-busy" : ""}`}
          role="status"
          aria-label={label}
        />
        <span>JoBot</span>
      </div>
      <div className="window-controls">
        <button
          className="theme-toggle"
          type="button"
          aria-label={theme === "dark" ? "Usar tema claro" : "Usar tema escuro"}
          aria-pressed={theme === "light"}
          title={theme === "dark" ? "Usar tema claro" : "Usar tema escuro"}
          onClick={toggleTheme}
        >
          <span className="theme-toggle-icon" aria-hidden="true">
            <Sun className="theme-icon-sun" size={15} />
            <Moon className="theme-icon-moon" size={15} />
          </span>
        </button>
        <button type="button" aria-label="Minimizar" title="Minimizar" onClick={() => void controlWindow("minimize")}>
          <Minus size={16} />
        </button>
        <button type="button" aria-label="Maximizar" title="Maximizar" onClick={() => void controlWindow("maximize")}>
          <Maximize2 size={14} />
        </button>
        <button className="close-button" type="button" aria-label="Fechar" title="Fechar" onClick={() => void controlWindow("close")}>
          <X size={16} />
        </button>
      </div>
    </header>
  );
}

import { useEffect, useState } from "react";
import { SearchProvider, useSearch } from "./SearchContext";
import { Sidebar } from "../components/Sidebar";
import { Titlebar } from "../components/Titlebar";
import { ToastProvider } from "../components/Toast";
import { ApplicationsView, HistoryView, SavedJobsView } from "../features/library/LibraryViews";
import { LogsView } from "../features/logs/LogsView";
import { ResumeStudioView } from "../features/resume/ResumeStudioView";
import { SearchView } from "../features/search/SearchView";
import { SettingsView, type SettingId } from "../features/settings/SettingsView";
import { drainNotifications, loadState } from "../services/api";
import type { JobSummary, ServiceState, ViewId } from "../types";

export function App() {
  return (
    <ToastProvider>
      <SearchProvider>
        <AppShell />
      </SearchProvider>
    </ToastProvider>
  );
}

function AppShell() {
  const { running: searchRunning } = useSearch();
  const [activeView, setActiveView] = useState<ViewId>("search");
  const [settingsSection, setSettingsSection] = useState<SettingId>("ai");
  const [service, setService] = useState<ServiceState | null>(null);
  const [resumeJob, setResumeJob] = useState<JobSummary | null>(null);
  const [resumeJobRequest, setResumeJobRequest] = useState(0);

  useEffect(() => {
    void loadState().then(setService).catch(() => setService(null));
    const timer = window.setInterval(() => {
      void loadState().then(setService).catch(() => setService(null));
    }, 3000);
    return () => window.clearInterval(timer);
  }, []);

  // The radar sweeps in the background; the whole point is that nobody is
  // watching this window when it finds something. The backend queues jobs over
  // the threshold and hands each one over exactly once, so this loop can be
  // dumb: drain, announce, forget.
  useEffect(() => {
    let cancelled = false;
    async function announce() {
      if (!window.senciaElectron) return;
      try {
        const { jobs } = await drainNotifications();
        if (cancelled) return;
        for (const job of jobs) {
          await window.senciaElectron.notifyJob({
            title: `${job.score}% - ${job.title}`,
            body: [job.company, job.location].filter(Boolean).join(" - ") || "Nova vaga encontrada pelo radar.",
            url: job.url,
          });
        }
      } catch {
        // A failed drain is not worth surfacing: the next sweep re-queues nothing
        // it already handed over, and the jobs are on the search screen anyway.
      }
    }
    void announce();
    const timer = window.setInterval(() => void announce(), 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  function openResumeStudioForJob(job: JobSummary) {
    setResumeJob(job);
    setResumeJobRequest((current) => current + 1);
    setActiveView("resume");
  }

  function openSettings(section: SettingId) {
    setSettingsSection(section);
    setActiveView("settings");
  }

  function changeView(view: ViewId) {
    if (view === "settings") setSettingsSection("ai");
    setActiveView(view);
  }

  let content = null;
  if (activeView === "search") {
    content = (
      <SearchView
        onOpenSettings={openSettings}
        onOptimizeResume={openResumeStudioForJob}
        onViewGaps={openResumeStudioForJob}
        onGenerateCoverLetter={openResumeStudioForJob}
      />
    );
  } else if (activeView === "logs") {
    content = <LogsView />;
  } else if (activeView === "settings") {
    content = <SettingsView key={settingsSection} initialSection={settingsSection} />;
  } else if (activeView === "saved") {
    content = <SavedJobsView />;
  } else if (activeView === "applications") {
    content = <ApplicationsView />;
  } else if (activeView === "history") {
    content = <HistoryView />;
  }

  const online = service?.status === "ready" || service?.status === "running" || searchRunning;

  return (
    <div className="app-shell">
      <Titlebar online={online} busy={searchRunning || service?.status === "running"} />
      <div className="app-body">
        <Sidebar activeView={activeView} onChange={changeView} searchRunning={searchRunning || service?.status === "running"} />
        <main>
          <div className="view-stage">
            {activeView !== "resume" ? content : null}
            <div className={activeView === "resume" ? "keep-mounted-view" : "keep-mounted-view view-hidden"} aria-hidden={activeView !== "resume"}>
              <ResumeStudioView job={resumeJob} jobRequest={resumeJobRequest} />
            </div>
          </div>
        </main>
      </div>
    </div>
  );
}

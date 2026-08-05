package server

import (
	"fmt"
	"os"
	"time"
)

type radarStatus struct {
	Enabled    bool   `json:"enabled"`
	Running    bool   `json:"running"`
	NextRun    string `json:"nextRun,omitempty"`
	LastRun    string `json:"lastRun,omitempty"`
	LastStatus string `json:"lastStatus,omitempty"`
}

func (a *api) startRadarLoop() {
	if os.Getenv("SENCIA_RADAR_DISABLED") == "1" {
		return
	}
	go a.radarLoop()
}

func (a *api) radarSnapshot() radarStatus {
	a.radarMu.Lock()
	defer a.radarMu.Unlock()
	return a.radar
}

func (a *api) updateRadarStatus(mutator func(*radarStatus)) {
	a.radarMu.Lock()
	defer a.radarMu.Unlock()
	mutator(&a.radar)
}

func (a *api) radarLoop() {
	var nextRun time.Time
	ticker := time.NewTicker(radarTick())
	defer ticker.Stop()

	for range ticker.C {
		config, err := a.configStore.load()
		if err != nil {
			a.log("error", "[ RADAR ] nao foi possivel carregar config: %v", err)
			continue
		}
		if !config.Toggles["radarMode"] {
			nextRun = time.Time{}
			a.updateRadarStatus(func(status *radarStatus) {
				status.Enabled = false
				status.Running = false
				status.NextRun = ""
			})
			continue
		}
		if configuredSources(config) == 0 {
			a.log("warning", "[ RADAR ] ignorado: nenhuma fonte ativa")
			nextRun = time.Time{}
			a.updateRadarStatus(func(status *radarStatus) {
				status.Enabled = true
				status.Running = false
				status.NextRun = ""
				status.LastStatus = "Nenhuma fonte ativa."
			})
			continue
		}
		interval := radarInterval(config)
		if nextRun.IsZero() {
			nextRun = time.Now()
		}
		a.updateRadarStatus(func(status *radarStatus) {
			status.Enabled = true
			status.NextRun = nextRun.Format(time.RFC3339)
			status.LastStatus = coalesce(status.LastStatus, "Radar aguardando proxima varredura.")
		})
		if a.liveSearch.snapshot().Running {
			nextRun = time.Now().Add(interval)
			a.updateRadarStatus(func(status *radarStatus) {
				status.Running = false
				status.NextRun = nextRun.Format(time.RFC3339)
				status.LastStatus = "Busca manual em andamento; radar reagendado."
			})
			continue
		}
		if time.Now().Before(nextRun) {
			continue
		}
		if !searchRolesConfigured(config) {
			nextRun = time.Now().Add(interval)
			a.log("warning", "[ RADAR ] configure cargos/roles antes de ativar varreduras")
			a.updateRadarStatus(func(status *radarStatus) {
				status.Running = false
				status.NextRun = nextRun.Format(time.RFC3339)
				status.LastStatus = "Cargos/roles nao configurados."
			})
			continue
		}
		run, ok := a.liveSearch.startRun()
		if !ok {
			nextRun = time.Now().Add(interval)
			continue
		}
		a.updateRadarStatus(func(status *radarStatus) {
			status.Running = true
			status.LastRun = time.Now().Format(time.RFC3339)
			status.LastStatus = "Radar executando varredura."
		})
		a.log("info", "[ RADAR ] varredura iniciada; proxima em %s", interval)
		a.runRadarBackground(run, config, interval)
		nextRun = time.Now().Add(interval)
		a.updateRadarStatus(func(status *radarStatus) {
			status.Running = false
			status.NextRun = nextRun.Format(time.RFC3339)
			status.LastStatus = "Ultima varredura concluida."
		})
	}
}

func (a *api) runRadarBackground(run *searchRun, config appConfig, interval time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("radar interrompido por erro interno: %v", r)
			a.liveSearch.finishForRun(run.id, "", err)
			a.updateRadarStatus(func(status *radarStatus) {
				status.Running = false
				status.LastStatus = err.Error()
			})
		}
	}()
	result, err := a.scraper.startSearch(run.ctx, config, func(job jobSummary) {
		a.liveSearch.addJobForRun(run.id, job)
	})
	if err != nil {
		a.liveSearch.finishForRun(run.id, "", err)
		a.updateRadarStatus(func(status *radarStatus) {
			status.Running = false
			status.LastStatus = err.Error()
		})
		a.log("error", "[ RADAR ] falhou: %v", err)
		return
	}
	canceled := run.ctx.Err() != nil
	a.liveSearch.finishWithDiagnosticsForRun(run.id, result.Message, result.Diagnostics, nil)
	if canceled {
		return
	}
	a.log("success", "[ RADAR ] %s; proxima varredura em %s", result.Message, interval)
	a.queueRadarNotifications(config, result.Jobs)
}

// queueRadarNotifications is the whole point of a radar: the user is not looking
// at the app. notificationThreshold has existed in the config and in the Settings
// form since the beginning and nothing has ever read it, so no sweep has ever
// announced anything. Only the radar queues — a manual search puts the results on
// a screen the user is already staring at.
func (a *api) queueRadarNotifications(config appConfig, jobs []jobSummary) {
	threshold := config.Form.NotificationThreshold
	if threshold <= 0 {
		return
	}
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job.Score >= threshold {
			ids = append(ids, job.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	queued, err := a.configStore.markJobsPendingNotification(ids, threshold)
	if err != nil {
		a.log("error", "[ RADAR ] nao foi possivel enfileirar notificacoes: %v", err)
		return
	}
	if queued > 0 {
		a.log("success", "[ RADAR ] %d vaga(s) acima de %d aguardando notificacao", queued, threshold)
	}
}

func radarTick() time.Duration {
	if raw := os.Getenv("SENCIA_RADAR_TICK_SECONDS"); raw != "" {
		if parsed, err := time.ParseDuration(raw + "s"); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 15 * time.Second
}

func radarInterval(config appConfig) time.Duration {
	minutes := config.Form.RadarIntervalMinutes
	if minutes <= 0 {
		minutes = 20
	}
	interval := time.Duration(minutes) * time.Minute
	if interval < time.Minute {
		return time.Minute
	}
	return interval
}

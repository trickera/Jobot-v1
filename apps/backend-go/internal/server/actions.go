package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func (b *logBuffer) add(level string, message string) {
	if b == nil {
		return
	}
	message = strings.TrimRight(message, "\n")
	if message == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	b.entries = append(b.entries, logEntry{
		ID:      b.nextID,
		Time:    time.Now().Format(time.RFC3339),
		Level:   normalizeLogLevel(level),
		Message: message,
	})
	if len(b.entries) > 600 {
		b.entries = append([]logEntry(nil), b.entries[len(b.entries)-600:]...)
	}
}

func (b *logBuffer) list() []logEntry {
	if b == nil {
		return []logEntry{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// Always return a non-nil slice so the API emits `"logs": []` rather than
	// `"logs": null` when the buffer is empty (mirrors the jobs null-safety rule).
	out := make([]logEntry, len(b.entries))
	copy(out, b.entries)
	return out
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "success", "warning", "error", "muted":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "info"
	}
}

func (a *api) log(level string, format string, args ...any) {
	message := format
	if len(args) > 0 {
		message = fmt.Sprintf(format, args...)
	}
	a.logger.Print(message)
	a.logs.add(level, message)
}

func (a *api) getLogs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]logEntry{"logs": a.logs.list()})
}

// drainNotifications hands the renderer the jobs a radar sweep found above the
// user's threshold and marks them delivered. It is a POST because it mutates:
// each job comes back exactly once, and a second call returns an empty list.
func (a *api) drainNotifications(w http.ResponseWriter, _ *http.Request) {
	jobs, err := a.configStore.drainPendingNotifications()
	if err != nil {
		a.log("error", "[ RADAR ] nao foi possivel ler notificacoes pendentes: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Nao foi possivel ler as notificacoes."})
		return
	}
	writeJSON(w, http.StatusOK, map[string][]jobSummary{"jobs": jobs})
}

func (a *api) openURL(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "URL invalida."})
		return
	}
	raw := strings.TrimSpace(payload.URL)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "URL de vaga invalida."})
		return
	}
	if err := openExternalURL(raw); err != nil {
		a.log("error", "[ OPEN ] falha ao abrir navegador: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": "Nao foi possivel abrir no navegador."})
		return
	}
	a.log("success", "[ OPEN ] vaga aberta no navegador: %s", raw)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Vaga aberta no navegador."})
}

func (a *api) jobAction(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var payload struct {
		Action string      `json:"action"`
		Job    *jobSummary `json:"job"`
		JobID  string      `json:"jobId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Acao invalida."})
		return
	}
	job := jobSummary{}
	if payload.Job != nil {
		job = *payload.Job
	}
	if strings.TrimSpace(job.ID) == "" {
		job.ID = payload.JobID
	}
	message, company, err := a.configStore.applyJobAction(payload.Action, job)
	if err != nil {
		a.log("error", "[ ACTION ] %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	switch normalizeJobAction(payload.Action) {
	case "blacklist":
		a.liveSearch.removeCompany(company)
	case "save":
		// Saving is organizational metadata, not a search disposition. Keep the
		// live result visible so the bookmark can be toggled in place.
		a.liveSearch.setJobSaved(job.ID, true)
	case "unsave":
		a.liveSearch.setJobSaved(job.ID, false)
	default:
		a.liveSearch.removeJob(job.ID)
	}
	a.log("success", "[ ACTION ] %s", message)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": message})
}

func openExternalURL(raw string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", raw).Start()
	case "darwin":
		return exec.Command("open", raw).Start()
	default:
		return exec.Command("xdg-open", raw).Start()
	}
}

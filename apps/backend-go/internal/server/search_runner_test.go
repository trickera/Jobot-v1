package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLiveSearchStateReplacesPendingJobWithFinalScore(t *testing.T) {
	var state liveSearchState
	state.addJob(jobSummary{ID: "job-1", Status: statusScoring, ScoringPending: true})
	state.addJob(jobSummary{ID: "job-1", Status: statusApply, Score: 87, ScoreSource: scoreSourceAI})

	snapshot := state.snapshot()
	if len(snapshot.Jobs) != 1 {
		t.Fatalf("expected one upserted job, got %+v", snapshot.Jobs)
	}
	job := snapshot.Jobs[0]
	if job.ScoringPending || job.Score != 87 || job.ScoreSource != scoreSourceAI {
		t.Fatalf("expected the final AI score to replace the preview, got %+v", job)
	}
}

func TestLiveSearchStateRemovesRejectedPreview(t *testing.T) {
	var state liveSearchState
	state.addJob(jobSummary{ID: "job-1", Status: statusScoring, ScoringPending: true})
	state.addJob(jobSummary{ID: "job-1", Remove: true})

	if jobs := state.snapshot().Jobs; len(jobs) != 0 {
		t.Fatalf("expected rejected preview removed, got %+v", jobs)
	}
}

func TestLiveSearchStateKeepsLowScoresOutOfMainResults(t *testing.T) {
	var state liveSearchState
	state.addJob(jobSummary{ID: "job-low", Status: statusScoring, ScoringPending: true})
	state.addJob(jobSummary{ID: "job-low", Status: statusDiscard, Score: 54})
	state.addJob(jobSummary{ID: "job-main", Status: statusApply, Score: 91})

	snapshot := state.snapshot()
	if snapshot.Total != 1 || len(snapshot.Jobs) != 1 || snapshot.Jobs[0].ID != "job-main" {
		t.Fatalf("main results were contaminated by low scores: %+v", snapshot)
	}
	if len(snapshot.LowScoreJobs) != 1 || snapshot.LowScoreJobs[0].ID != "job-low" {
		t.Fatalf("low-score result not transported separately: %+v", snapshot.LowScoreJobs)
	}

	state.addJob(jobSummary{ID: "job-low", Remove: true})
	if low := state.snapshot().LowScoreJobs; len(low) != 0 {
		t.Fatalf("remove event must withdraw a low-score card too: %+v", low)
	}
}

func TestLiveSearchStateUpdatesSavedAtInBothCollections(t *testing.T) {
	var state liveSearchState
	state.addJob(jobSummary{ID: "job-main", Status: statusApply})
	state.addJob(jobSummary{ID: "job-low", Status: statusDiscard})

	for _, id := range []string{"job-main", "job-low"} {
		state.setJobSaved(id, true)
	}
	snapshot := state.snapshot()
	if snapshot.Jobs[0].SavedAt == "" || snapshot.LowScoreJobs[0].SavedAt == "" {
		t.Fatalf("save was not reflected in live collections: %+v", snapshot)
	}

	for _, id := range []string{"job-main", "job-low"} {
		state.setJobSaved(id, false)
	}
	snapshot = state.snapshot()
	if snapshot.Jobs[0].SavedAt != "" || snapshot.LowScoreJobs[0].SavedAt != "" {
		t.Fatalf("unsave was not reflected in live collections: %+v", snapshot)
	}
}

func TestLiveSearchStatusUsesEmptyArraysInsteadOfNull(t *testing.T) {
	payload, err := json.Marshal((&liveSearchState{}).snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(payload, []byte(`"jobs":[]`)) || !bytes.Contains(payload, []byte(`"lowScoreJobs":[]`)) {
		t.Fatalf("empty job collections must be JSON arrays: %s", payload)
	}
}

func TestListJobsUsesEmptyArraysInsteadOfNull(t *testing.T) {
	service := &api{
		logger:      log.New(io.Discard, "", 0),
		configStore: newTestStore(t),
	}
	recorder := httptest.NewRecorder()
	service.listJobs(recorder, httptest.NewRequest("GET", "/api/v1/jobs", nil))

	var response searchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Jobs == nil || response.LowScoreJobs == nil {
		t.Fatalf("empty job collections must be arrays, body=%s", recorder.Body.Bytes())
	}
}

func TestLiveSearchResetCancelsActiveRunAndWaitsForTerminal(t *testing.T) {
	var state liveSearchState
	run, ok := state.startRun()
	if !ok {
		t.Fatal("expected first run to start")
	}
	finished := make(chan struct{})
	go func() {
		<-run.ctx.Done()
		state.finishForRun(run.id, "stale completion", nil)
		close(finished)
	}()

	state.reset()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("reset did not wait for the canceled run to finish")
	}
	if run.ctx.Err() == nil {
		t.Fatal("reset must cancel the active run context")
	}
	if snapshot := state.snapshot(); snapshot.Running || snapshot.Message != "" {
		t.Fatalf("reset must leave idle empty state, got %+v", snapshot)
	}
}

func TestLiveSearchRunIDRejectsLateCallbacksAfterResetAndRestart(t *testing.T) {
	var state liveSearchState
	oldRun, ok := state.startRun()
	if !ok {
		t.Fatal("expected old run to start")
	}
	state.finishForRun(oldRun.id, "old complete", nil)
	state.reset()

	newRun, ok := state.startRun()
	if !ok {
		t.Fatal("expected new run to start")
	}
	if newRun.id == oldRun.id {
		t.Fatalf("run id was reused: old=%d new=%d", oldRun.id, newRun.id)
	}

	state.addJobForRun(oldRun.id, jobSummary{ID: "late-job", Status: statusApply})
	state.finishWithDiagnosticsForRun(oldRun.id, "late finish", searchDiagnostics{}, nil)
	snapshot := state.snapshot()
	if !snapshot.Running || len(snapshot.Jobs) != 0 || snapshot.Message != "Busca iniciada." {
		t.Fatalf("late old-run callback mutated the new run: %+v", snapshot)
	}
}

func TestLiveSearchSecondStartDuringCancellationReturnsConflict(t *testing.T) {
	var state liveSearchState
	run, ok := state.startRun()
	if !ok {
		t.Fatal("expected first run to start")
	}
	resetDone := make(chan struct{})
	go func() {
		state.reset()
		close(resetDone)
	}()
	select {
	case <-run.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("reset did not cancel the active run")
	}
	if _, ok := state.startRun(); ok {
		t.Fatal("second run must conflict while reset is still canceling")
	}
	state.finishForRun(run.id, "canceled", context.Canceled)
	select {
	case <-resetDone:
	case <-time.After(time.Second):
		t.Fatal("reset did not finish after the run reached terminal state")
	}
}

func TestLiveSearchResetHasBoundedWaitForUncooperativeRun(t *testing.T) {
	previousWait := searchResetWait
	searchResetWait = 10 * time.Millisecond
	defer func() { searchResetWait = previousWait }()

	var state liveSearchState
	run, ok := state.startRun()
	if !ok {
		t.Fatal("expected run to start")
	}
	started := time.Now()
	state.reset()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("reset exceeded bounded wait: %s", elapsed)
	}
	if _, ok := state.startRun(); !ok {
		t.Fatal("a new run must be startable after bounded reset")
	}
	state.finishForRun(run.id, "late old completion", nil)
}

func TestLiveSearchTerminalSnapshotSurvivesUntilExplicitReset(t *testing.T) {
	var state liveSearchState
	run, ok := state.startRun()
	if !ok {
		t.Fatal("expected run to start")
	}
	state.addJobForRun(run.id, jobSummary{ID: "terminal-job", Status: statusApply})
	state.finishWithDiagnosticsForRun(run.id, "terminal message", searchDiagnostics{Collected: 1}, nil)
	snapshot := state.snapshot()
	if snapshot.Running || snapshot.Message != "terminal message" || len(snapshot.Jobs) != 1 || snapshot.Diagnostics.Collected != 1 {
		t.Fatalf("terminal snapshot lost state: %+v", snapshot)
	}
	state.reset()
	if snapshot := state.snapshot(); snapshot.Running || len(snapshot.Jobs) != 0 || snapshot.Message != "" {
		t.Fatalf("explicit reset did not clear terminal state: %+v", snapshot)
	}
}

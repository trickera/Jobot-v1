package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// Resume Studio's AI steps run for tens of seconds. Answering them on the
// request goroutine meant the desktop app held a fetch open for the whole
// generation with no timeout of its own, so a slow provider looked like a
// frozen app and any hiccup on the socket lost work that had already been paid
// for. These routes hand the work to a goroutine and hand the client a job id
// instead — the same shape the job search already uses (search_runner.go):
// return immediately, let the client poll.
//
// The work itself is the *existing* synchronous handler, run against a buffered
// ResponseWriter. That keeps one implementation of each operation: the sync
// routes stay live and the async wrapper cannot drift away from them.

const (
	// resumeJobTTL is how long a finished job stays collectable. The client
	// polls within a second of completion; this is slack for a reload, not a
	// results cache (that is llmCache's job).
	resumeJobTTL = 10 * time.Minute

	// resumeJobBudget bounds a detached job. It sits above llmCascadeBudget so
	// the cascade's own deadline fires first and produces a typed AI error,
	// rather than this outer guard producing an opaque one.
	resumeJobBudget = llmCascadeBudget + 30*time.Second
)

type resumeJobState string

const (
	resumeJobRunning resumeJobState = "running"
	resumeJobDone    resumeJobState = "done"
)

// resumeJobStatus is what GET /api/v1/resume/jobs/{id} returns. Result is the
// body the wrapped handler wrote, replayed verbatim, so the client decodes the
// exact same payload the synchronous route would have given it.
type resumeJobStatus struct {
	State  resumeJobState  `json:"state"`
	Status int             `json:"status,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

type resumeJobRecord struct {
	status     resumeJobStatus
	finishedAt time.Time
}

type resumeJobStore struct {
	mu   sync.Mutex
	jobs map[string]resumeJobRecord
}

func newResumeJobStore() *resumeJobStore {
	return &resumeJobStore{jobs: map[string]resumeJobRecord{}}
}

func (s *resumeJobStore) start(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(time.Now())
	s.jobs[id] = resumeJobRecord{status: resumeJobStatus{State: resumeJobRunning}}
}

func (s *resumeJobStore) finish(id string, status int, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = resumeJobRecord{
		status:     resumeJobStatus{State: resumeJobDone, Status: status, Result: json.RawMessage(body)},
		finishedAt: time.Now(),
	}
}

func (s *resumeJobStore) get(id string) (resumeJobStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return resumeJobStatus{}, false
	}
	return record.status, true
}

// evictLocked drops finished jobs past their TTL. A running job is never
// evicted: its client is still waiting for it.
func (s *resumeJobStore) evictLocked(now time.Time) {
	for id, record := range s.jobs {
		if record.status.State == resumeJobDone && now.Sub(record.finishedAt) > resumeJobTTL {
			delete(s.jobs, id)
		}
	}
}

func newResumeJobID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		// A collision here would hand one client another's result, so fall back
		// to something still unique per process rather than to a fixed string.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf)
}

// bufferedResponse captures what a handler writes so it can be replayed to a
// client that is no longer on the other end of the connection.
type bufferedResponse struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{status: http.StatusOK, header: http.Header{}}
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(status int)      { b.status = status }

// resumeAsyncHandlers maps the {op} path segment to the synchronous handler
// that does the work. Only the AI-backed steps are here: diagnose, score and
// export are deterministic and already answer in milliseconds, so putting them
// behind a poll would only add a round-trip.
func (a *api) resumeAsyncHandlers() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"parse":        a.resumeParse,
		"analyze-job":  a.resumeAnalyzeJob,
		"gap":          a.resumeGap,
		"optimize":     a.resumeOptimize,
		"cover-letter": a.resumeCoverLetter,
	}
}

// resumeAsyncStart accepts the same body as the synchronous route it wraps and
// answers 202 with a job id.
func (a *api) resumeAsyncStart(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	op := r.PathValue("op")
	handler, ok := a.resumeAsyncHandlers()[op]
	if !ok {
		writeResumeError(w, resumeErrorFor(http.StatusNotFound, "unknown_operation",
			"Unknown resume operation."))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxResumeUploadBytes*2))
	if err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request",
			"Could not read the request."))
		return
	}

	id := newResumeJobID()
	a.resumeJobs.start(id)

	// The request's own context dies the moment we answer 202, so the detached
	// work gets a fresh one with its own budget.
	jobRequest := r.Clone(context.Background())
	go a.runResumeJob(id, handler, jobRequest, body)

	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": id})
}

func (a *api) runResumeJob(id string, handler http.HandlerFunc, jobRequest *http.Request, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), resumeJobBudget)
	defer cancel()

	defer func() {
		// A panic in an AI handler must fail this one job, not take the backend
		// down and every other job with it.
		if recovered := recover(); recovered != nil {
			a.logger.Printf("[ RESUME ] job %s panicked: %v", id, recovered)
			failure, _ := json.Marshal(map[string]string{
				"code":    "ai_unavailable",
				"message": "The AI call failed unexpectedly. Try again - check the app logs if it keeps happening.",
			})
			a.resumeJobs.finish(id, http.StatusBadGateway, failure)
		}
	}()

	jobRequest = jobRequest.WithContext(ctx)
	jobRequest.Body = io.NopCloser(bytes.NewReader(body))
	jobRequest.ContentLength = int64(len(body))

	recorder := newBufferedResponse()
	handler(recorder, jobRequest)
	a.resumeJobs.finish(id, recorder.status, recorder.body.Bytes())
}

// resumeJobStatusHandler reports a job. A polling client that asks for an id we
// never had (or that has aged out) gets 404 rather than a silent "running",
// so it fails loudly instead of polling forever.
func (a *api) resumeJobStatusHandler(w http.ResponseWriter, r *http.Request) {
	status, ok := a.resumeJobs.get(r.PathValue("id"))
	if !ok {
		writeResumeError(w, resumeErrorFor(http.StatusNotFound, "job_not_found",
			"That operation is no longer available. Run it again."))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

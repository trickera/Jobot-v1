package server

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func seedJob(t *testing.T, store *configStore, id string, score int, status string) {
	t.Helper()
	if err := store.saveSearchResults(appConfig{}, []jobPost{{
		ID:      id,
		Source:  "LinkedIn",
		Title:   "Backend Engineer " + id,
		Company: "Example Corp",
		URL:     "https://example.com/" + id,
		Status:  status,
		Score:   score,
	}}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// The statuses a scraped job actually carries. An earlier version of this file
// seeded status "new" and passed, while the real radar queued nothing at all:
// "new" is only the column default, and statusForScore never writes it. Seeding
// a value production never produces is indistinguishable from the feature
// working, right up until someone runs the app.
const (
	seedApply   = statusApply   // "[APPLY NOW]"
	seedDiscard = statusDiscard // "[DISCARD]"
)

// A sweep announces the jobs it just brought in above the threshold, and nothing
// else: not the ones below it, and not the ones the user already dealt with.
func TestRadarQueuesOnlyJobsOverTheThreshold(t *testing.T) {
	store := newTestStore(t)

	seedJob(t, store, "high", 92, seedApply)
	seedJob(t, store, "low", 40, seedDiscard)
	seedJob(t, store, "applied-already", 95, statusApplied)
	seedJob(t, store, "dismissed-already", 96, statusDismissed)

	queued, err := store.markJobsPendingNotification(
		[]string{"high", "low", "applied-already", "dismissed-already"}, 85)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if queued != 1 {
		t.Fatalf("expected 1 job queued, got %d", queued)
	}

	jobs, err := store.drainPendingNotifications()
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "high" {
		t.Fatalf("expected only the 92-point job, got %+v", jobs)
	}
}

// The radar re-scrapes the same boards every twenty minutes and finds the same
// job again. Announcing it on every sweep would be worse than never announcing
// it, so a delivered job must never come back.
func TestRadarAnnouncesAJobExactlyOnce(t *testing.T) {
	store := newTestStore(t)
	seedJob(t, store, "job-1", 90, seedApply)

	if _, err := store.markJobsPendingNotification([]string{"job-1"}, 85); err != nil {
		t.Fatalf("mark: %v", err)
	}
	first, err := store.drainPendingNotifications()
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("expected the job on the first drain, got %d", len(first))
	}

	// A second drain, as the 5-second poll does.
	second, err := store.drainPendingNotifications()
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("the same job was handed over twice: %+v", second)
	}

	// And the next sweep re-finds it, exactly as the real radar does.
	queued, err := store.markJobsPendingNotification([]string{"job-1"}, 85)
	if err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	if queued != 0 {
		t.Fatalf("a re-found job was queued again (%d) - the user would be notified every sweep", queued)
	}
}

// The one that would hit every existing install. Before this column existed,
// every job in the table has notified_at NULL, which reads as "never announced".
// Without the backfill, the first sweep after the upgrade announces the entire
// job history at once. This builds a database on the OLD schema, fills it, and
// then runs the real migration over it.
func TestUpgradingAnExistingDatabaseDoesNotAnnounceTheWholeHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	t.Setenv("SENCIA_DB_PATH", path)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// The jobs table as it stood before notification support: no notified_at,
	// no notify_pending.
	if _, err := db.Exec(`CREATE TABLE jobs (
		id TEXT PRIMARY KEY,
		source_id TEXT,
		title TEXT NOT NULL,
		company TEXT,
		location TEXT,
		url TEXT,
		status TEXT NOT NULL DEFAULT 'new',
		score INTEGER NOT NULL DEFAULT 0,
		raw_json TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("old schema: %v", err)
	}
	for _, id := range []string{"old-1", "old-2", "old-3"} {
		if _, err := db.Exec(
			`INSERT INTO jobs (id, title, status, score, raw_json, created_at, updated_at)
			 VALUES (?, 'Old Job', 'new', 97, '{}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			id,
		); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	db.Close()

	// The real migration, over the real old database.
	store := newConfigStore()
	migrated, err := store.open()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer migrated.Close()

	var unannounced int
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM jobs WHERE notified_at IS NULL`).Scan(&unannounced); err != nil {
		t.Fatalf("count: %v", err)
	}
	if unannounced != 0 {
		t.Fatalf("%d pre-existing job(s) still look unannounced - the first sweep would notify about all of them", unannounced)
	}

	// And a sweep that re-finds them stays quiet.
	queued, err := store.markJobsPendingNotification([]string{"old-1", "old-2", "old-3"}, 85)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if queued != 0 {
		t.Fatalf("upgrading queued %d historical job(s) for notification", queued)
	}
}

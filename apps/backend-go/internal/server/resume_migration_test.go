package server

import (
	"testing"

	_ "modernc.org/sqlite"
)

// The v2 migration must be idempotent (opening the store twice must not
// error) and must create all six Resume Studio tables.
func TestMigrateCreatesResumeTables(t *testing.T) {
	store := newTestStore(t)

	db, err := store.open()
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	db.Close()

	// Re-opening (re-running migrate) against the same file must not error.
	db, err = store.open()
	if err != nil {
		t.Fatalf("second open (idempotency): %v", err)
	}
	defer db.Close()

	tables := []string{
		"resume_documents",
		"resume_templates",
		"resume_versions",
		"resume_analyses",
		"job_resume_matches",
		"cover_letters",
	}
	for _, table := range tables {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("expected table %s to exist: %v", table, err)
		}
	}

	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != 7 {
		t.Fatalf("expected schema_migrations at version 7, got %d", version)
	}
	var scoreSourceColumn string
	if err := db.QueryRow(`SELECT name FROM pragma_table_info('jobs') WHERE name = 'score_source'`).Scan(&scoreSourceColumn); err != nil {
		t.Fatalf("expected jobs.score_source migration: %v", err)
	}
	var savedAtColumn string
	if err := db.QueryRow(`SELECT name FROM pragma_table_info('jobs') WHERE name = 'saved_at'`).Scan(&savedAtColumn); err != nil {
		t.Fatalf("expected jobs.saved_at migration: %v", err)
	}
}

func TestMigrateResumeTablesEnforceForeignKeys(t *testing.T) {
	store := newTestStore(t)
	db, err := store.open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	now := "2026-07-05T00:00:00Z"
	_, err = db.Exec(
		`INSERT INTO resume_documents (id, name, source_file, source_format, canonical_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"resume:doc:1", "Base Resume", "", "", "{}", now, now,
	)
	if err != nil {
		t.Fatalf("insert resume_documents: %v", err)
	}

	_, err = db.Exec(
		`INSERT INTO resume_versions (id, document_id, job_id, canonical_json, diff_json, template_id, ats_score, hr_score, created_at)
		 VALUES (?, ?, NULL, ?, ?, NULL, 0, 0, ?)`,
		"resume:ver:1", "resume:doc:1", "{}", "[]", now,
	)
	if err != nil {
		t.Fatalf("insert resume_versions: %v", err)
	}

	// Deleting the parent document must cascade to the version.
	if _, err := db.Exec(`DELETE FROM resume_documents WHERE id = ?`, "resume:doc:1"); err != nil {
		t.Fatalf("delete resume_documents: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM resume_versions WHERE id = ?`, "resume:ver:1").Scan(&count); err != nil {
		t.Fatalf("count resume_versions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected ON DELETE CASCADE to remove the version, found %d rows", count)
	}
}

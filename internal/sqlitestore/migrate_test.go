package sqlitestore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

// version1 is the schema written by kanjo before task markers.
const version1 = `
CREATE TABLE usage(
  source TEXT NOT NULL, session TEXT NOT NULL, day TEXT NOT NULL, task TEXT NOT NULL,
  model TEXT NOT NULL, long_context INTEGER NOT NULL,
  messages INTEGER NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
  reasoning INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL,
  cache_read INTEGER NOT NULL, cost_reported_usd REAL,
  PRIMARY KEY(source, session, day, task, model, long_context));
CREATE TABLE sessions(
  source TEXT NOT NULL, session TEXT NOT NULL, task_source TEXT NOT NULL, labels_seen INTEGER NOT NULL,
  PRIMARY KEY(source, session));
CREATE TABLE snapshots(
  at TEXT NOT NULL, sessions INTEGER NOT NULL, rows INTEGER NOT NULL, skipped INTEGER NOT NULL);
INSERT INTO usage VALUES('claude','s-1','2026-11-03','ABC-1','m',0,2,3,4,0,0,0,7,NULL);
INSERT INTO sessions VALUES('claude','s-1','heuristic',1);
PRAGMA user_version = 1;
`

func writeVersion1(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(version1); err != nil {
		t.Fatal(err)
	}
	return dir
}

func userVersion(t *testing.T, dir string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigratesVersion1KeepingRows(t *testing.T) {
	dir := writeVersion1(t)
	s := open(t, dir)
	rows, err := s.Rows(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Task != "ABC-1" || rows[0].Messages != 2 || rows[0].CacheRead != 7 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
	if v := userVersion(t, dir); v != 2 {
		t.Errorf("user_version = %d, want 2", v)
	}
	s.Close()
	again := open(t, dir) // a second open of a migrated database is a no-op
	if rows, err := again.Rows(context.Background()); err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
}

func TestOpenReadOnlyReadsVersion1WithoutMigrating(t *testing.T) {
	dir := writeVersion1(t)
	ro, err := sqlitestore.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	rows, err := ro.Rows(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Messages != 2 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
	if ss, err := ro.Sessions(context.Background()); err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %+v, err %v", ss, err)
	}
	if v := userVersion(t, dir); v != 1 {
		t.Errorf("user_version = %d, a read must not migrate", v)
	}
}

func TestReadOnlyJournalOfVersion1IsEmpty(t *testing.T) {
	dir := writeVersion1(t)
	ro, err := sqlitestore.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if ms, err := ro.Markers(context.Background()); err != nil || len(ms) != 0 {
		t.Fatalf("markers = %+v, err %v", ms, err)
	}
}

func TestFreshDatabaseHasTheMarkersTable(t *testing.T) {
	dir := t.TempDir()
	open(t, dir)
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('markers')").Scan(&n); err != nil || n != 5 {
		t.Fatalf("markers columns = %d, err %v", n, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('usage') WHERE name = 'marked'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("usage.marked present = %d, err %v", n, err)
	}
}

func TestConcurrentOpensOfAVersion1DatabaseAllSucceed(t *testing.T) {
	dir := writeVersion1(t)
	errs := make(chan error, 8)
	for i := 0; i < cap(errs); i++ {
		go func() {
			s, err := sqlitestore.Open(dir)
			if err == nil {
				s.Close()
			}
			errs <- err
		}()
	}
	for i := 0; i < cap(errs); i++ {
		if err := <-errs; err != nil {
			t.Errorf("Open: %v", err)
		}
	}
	if v := userVersion(t, dir); v != 2 {
		t.Errorf("user_version = %d, want 2", v)
	}
}

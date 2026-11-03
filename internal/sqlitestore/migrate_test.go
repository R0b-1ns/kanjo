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
	if v := userVersion(t, dir); v != 3 {
		t.Errorf("user_version = %d, want 3", v)
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
	if v := userVersion(t, dir); v != 3 {
		t.Errorf("user_version = %d, want 3", v)
	}
}

func TestConcurrentOpensOfAFreshFolderAllSucceed(t *testing.T) {
	for round := 0; round < 10; round++ {
		dir := filepath.Join(t.TempDir(), "data")
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
				t.Fatalf("round %d: Open: %v", round, err)
			}
		}
	}
}

// version2 is the schema written by kanjo before the run journal.
const version2 = version1Tables + `
INSERT INTO usage VALUES('claude','s-1','2026-11-03','ABC-1','m',0,2,3,4,0,0,0,7,NULL,1);
INSERT INTO sessions VALUES('claude','s-1','heuristic',1);
CREATE TABLE markers(
  id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('start','end')), session TEXT NOT NULL, task TEXT,
  CHECK((kind = 'start') = (task IS NOT NULL)));
INSERT INTO markers(at, kind, session, task) VALUES('2026-11-03T10:00:00.000Z','start','','ABC-1');
PRAGMA user_version = 2;
`

const version1Tables = `
CREATE TABLE usage(
  source TEXT NOT NULL, session TEXT NOT NULL, day TEXT NOT NULL, task TEXT NOT NULL,
  model TEXT NOT NULL, long_context INTEGER NOT NULL,
  messages INTEGER NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
  reasoning INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL,
  cache_read INTEGER NOT NULL, cost_reported_usd REAL, marked INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(source, session, day, task, model, long_context));
CREATE TABLE sessions(
  source TEXT NOT NULL, session TEXT NOT NULL, task_source TEXT NOT NULL, labels_seen INTEGER NOT NULL,
  PRIMARY KEY(source, session));
CREATE TABLE snapshots(
  at TEXT NOT NULL, sessions INTEGER NOT NULL, rows INTEGER NOT NULL, skipped INTEGER NOT NULL);
`

func writeVersion2(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(version2); err != nil {
		t.Fatal(err)
	}
	return dir
}

func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigratesVersion2KeepingEverything(t *testing.T) {
	dir := writeVersion2(t)
	s := open(t, dir)
	rows, err := s.Rows(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Marked != 1 || rows[0].CacheRead != 7 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
	if ms, err := s.Markers(context.Background()); err != nil || len(ms) != 1 || ms[0].Task != "ABC-1" {
		t.Fatalf("markers = %+v, err %v", ms, err)
	}
	if v := userVersion(t, dir); v != 3 {
		t.Errorf("user_version = %d, want 3", v)
	}
	var n int
	for _, table := range []string{"runs", "leads"} {
		if err := rawDB(t, dir).QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
}

func TestFreshDatabaseHasTheRunTables(t *testing.T) {
	dir := t.TempDir()
	open(t, dir)
	db := rawDB(t, dir)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('runs')").Scan(&n); err != nil || n != 17 {
		t.Fatalf("runs columns = %d, err %v", n, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('leads')").Scan(&n); err != nil || n != 4 {
		t.Fatalf("leads columns = %d, err %v", n, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_index_list('runs') WHERE name = 'runs_task'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("runs_task index = %d, err %v", n, err)
	}
}

func TestOpenReadOnlyReadsVersion2WithoutMigrating(t *testing.T) {
	dir := writeVersion2(t)
	ro, err := sqlitestore.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if rows, err := ro.Rows(context.Background()); err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
	if v := userVersion(t, dir); v != 2 {
		t.Errorf("user_version = %d, a read must not migrate", v)
	}
	var n int
	if err := rawDB(t, dir).QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'runs'").Scan(&n); err != nil || n != 0 {
		t.Errorf("runs table created by a read: %d, err %v", n, err)
	}
}

const runInsert = "INSERT INTO runs(session, task, adapter, model, started_at, finished_at, verdict, base, branch, dir) VALUES(?,?,?,?,?,?,?,?,?,?)"

func TestRunsCheckConstraintsRefuseABadVerdict(t *testing.T) {
	dir := t.TempDir()
	open(t, dir)
	db := rawDB(t, dir)
	base := "0123456789abcdef0123456789abcdef01234567"
	bad := [][]any{
		{"s1", "-", "script", "m", "2026-11-03T10:00:00.000Z", nil, "bogus", base, "delegate/s1", "worktrees/s1"},
		{"s2", "-", "script", "m", "2026-11-03T10:00:00.000Z", "2026-11-03T10:00:01.000Z", "running", base, "delegate/s2", "worktrees/s2"},
		{"s3", "-", "script", "m", "2026-11-03T10:00:00.000Z", nil, "failed", base, "delegate/s3", "worktrees/s3"},
	}
	for i, args := range bad {
		if _, err := db.Exec(runInsert, args...); err == nil {
			t.Errorf("bad row %d accepted", i)
		}
	}
	if _, err := db.Exec(runInsert, "ok1", "-", "script", "m", "2026-11-03T10:00:00.000Z", nil, "running", base, "delegate/ok1", "worktrees/ok1"); err != nil {
		t.Fatalf("good row refused: %v", err)
	}
}

func TestLeadsSessionIsUnique(t *testing.T) {
	dir := t.TempDir()
	open(t, dir)
	db := rawDB(t, dir)
	base := "0123456789abcdef0123456789abcdef01234567"
	if _, err := db.Exec(runInsert, "s1", "-", "script", "m", "2026-11-03T10:00:00.000Z", nil, "running", base, "delegate/s1", "worktrees/s1"); err != nil {
		t.Fatal(err)
	}
	ins := "INSERT INTO leads(at, session, verdict) VALUES('2026-11-03T11:00:00.000Z', ?, ?)"
	if _, err := db.Exec(ins, "s1", "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ins, "s1", "rejected"); err == nil {
		t.Error("second lead for one run accepted")
	}
	if _, err := db.Exec(ins, "s1", "maybe"); err == nil {
		t.Error("bad lead verdict accepted")
	}
}

package sqlitestore_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

func TestRunJournalContract(t *testing.T) {
	kanjotest.RunJournalContract(t, func(t *testing.T) core.RunJournal { return open(t, t.TempDir()) })
}

func TestHostileRunValuesStayData(t *testing.T) {
	s := open(t, t.TempDir())
	r := kanjotest.SampleRun(1)
	r.Task = "'; DROP TABLE runs;--"
	if err := s.Begin(context.Background(), r); err == nil {
		t.Fatal("hostile label accepted")
	}
	if recs, err := s.Runs(context.Background()); err != nil || len(recs) != 0 {
		t.Fatalf("runs = %+v, err %v", recs, err)
	}
}

func insertRaw(t *testing.T, db *sql.DB, session, task, branch, dir, verdict string) {
	t.Helper()
	var finished any
	if verdict != "running" {
		finished = "2026-11-03T10:00:01.000Z"
	}
	if _, err := db.Exec(runInsert, session, task, "script", "m", "2026-11-03T10:00:00.000Z", finished, verdict,
		"0123456789abcdef0123456789abcdef01234567", branch, dir); err != nil {
		t.Fatalf("raw insert: %v", err)
	}
}

func TestTamperedRunRowFailsTheRead(t *testing.T) {
	const canary = "CANARY-77"
	cases := map[string][5]string{
		"branch":  {"ses_1", "-", "delegate/other", "worktrees/ses_1", "running"},
		"dir":     {"ses_1", "-", "delegate/ses_1", "../x", "running"},
		"session": {"-bad", "-", "delegate/-bad", "worktrees/-bad", "running"},
		"task":    {"ses_1", canary + "/../x", "delegate/ses_1", "worktrees/ses_1", "running"},
	}
	for name, c := range cases {
		dir := t.TempDir()
		s := open(t, dir)
		insertRaw(t, rawDB(t, dir), c[0], c[1], c[2], c[3], c[4])
		_, err := s.Runs(context.Background())
		if err == nil {
			t.Errorf("%s: tampered row read without error", name)
			continue
		}
		if strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "../x") {
			t.Errorf("%s: error repeats a value: %v", name, err)
		}
	}
}

func TestRunsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := sqlitestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := kanjotest.SampleRun(1)
	ctx := context.Background()
	if err := s.Begin(ctx, r); err != nil {
		t.Fatal(err)
	}
	s.Created(ctx, r.Session)
	res := kanjotest.SampleResult(core.RunTimeout)
	if err := s.Finish(ctx, r.Session, res); err != nil {
		t.Fatal(err)
	}
	s.Close()
	again := open(t, dir)
	recs, err := again.Runs(ctx)
	if err != nil || len(recs) != 1 || recs[0].Run != r || recs[0].RunResult != res || !recs[0].Created {
		t.Fatalf("runs = %+v, err %v", recs, err)
	}
}

func TestRunsOfAVersion2DatabaseAreEmpty(t *testing.T) {
	dir := writeVersion2(t)
	ro, err := sqlitestore.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if recs, err := ro.Runs(context.Background()); err != nil || len(recs) != 0 {
		t.Fatalf("runs = %+v, err %v", recs, err)
	}
}

func TestReadOnlyStoreRefusesToBegin(t *testing.T) {
	dir := t.TempDir()
	open(t, dir).Close()
	ro, err := sqlitestore.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if err := ro.Begin(context.Background(), kanjotest.SampleRun(1)); err == nil {
		t.Fatal("Begin on a read-only store succeeded")
	}
	var n int
	if err := rawDB(t, dir).QueryRow("SELECT COUNT(*) FROM runs").Scan(&n); err != nil || n != 0 {
		t.Fatalf("runs = %d, err %v", n, err)
	}
}

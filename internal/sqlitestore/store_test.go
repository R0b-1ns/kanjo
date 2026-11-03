package sqlitestore_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

func open(t *testing.T, dir string) *sqlitestore.Store {
	t.Helper()
	s, err := sqlitestore.Open(dir)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func oneRow(task string) core.Batch {
	return core.Batch{
		Rows: []core.UsageRow{{
			Key:      core.Key{Source: "claude", Session: "s-1", Day: "2026-11-03", Task: task, Model: "m"},
			Messages: 1, Tokens: core.Tokens{Input: 3, CacheRead: 7},
		}},
		Sessions: []core.SessionInfo{{Source: "claude", Session: "s-1", TaskSource: "none"}},
	}
}

var logAt = core.SnapshotLog{At: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), Sessions: 1, Rows: 1}

func TestStoreContract(t *testing.T) {
	kanjotest.StoreContract(t, func(t *testing.T) core.Store { return open(t, t.TempDir()) })
}

func TestOpenCreatesPrivateDirAndGitignore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	open(t, dir)
	di, err := os.Stat(dir)
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, err %v", di.Mode().Perm(), err)
	}
	fi, err := os.Stat(filepath.Join(dir, "kanjo.db"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("db mode = %v, err %v", fi.Mode().Perm(), err)
	}
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || string(gi) != "*\n" {
		t.Fatalf(".gitignore = %q, err %v", gi, err)
	}
}

func TestOpenKeepsExistingGitignore(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(custom, []byte("*\n# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	open(t, dir)
	if got, _ := os.ReadFile(custom); string(got) != "*\n# mine\n" {
		t.Fatalf(".gitignore overwritten: %q", got)
	}
}

func TestOpenUnwritableDirFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o700) })
	dir := filepath.Join(parent, "data")
	_, err := sqlitestore.Open(dir)
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want it to name %s", err, dir)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "kanjo.db")); statErr == nil {
		t.Fatal("no partial database may be left behind")
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := sqlitestore.Open(dir); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v, want a newer-schema error", err)
	}
}

func TestReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.Merge(context.Background(), oneRow("ABC-1"), logAt); err != nil {
		t.Fatal(err)
	}
	s.Close()
	rows, err := open(t, dir).Rows(context.Background())
	if err != nil || len(rows) != 1 || rows[0].CacheRead != 7 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
}

func TestHostileValuesStayData(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	hostile := "x'); DROP TABLE usage;--"
	if err := s.Merge(context.Background(), oneRow(hostile), logAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rows(context.Background()); err == nil {
		t.Fatal("a stored row that fails validation must be an error on read")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var task string
	if err := db.QueryRow("SELECT task FROM usage").Scan(&task); err != nil || task != hostile {
		t.Fatalf("task = %q, err %v: usage table must still exist with the literal value", task, err)
	}
}

func TestClosedStoreReturnsErrors(t *testing.T) {
	s, err := sqlitestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	ctx := context.Background()
	if err := s.Merge(ctx, oneRow("ABC-1"), logAt); err == nil {
		t.Error("Merge on a closed store must fail")
	}
	if _, err := s.Rows(ctx); err == nil {
		t.Error("Rows on a closed store must fail")
	}
	if _, err := s.Sessions(ctx); err == nil {
		t.Error("Sessions on a closed store must fail")
	}
}

func TestOpenSetsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	open(t, dir)
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 1 {
		t.Fatalf("user_version = %d, err %v; want 1", v, err)
	}
}

func TestOpenReadOnlyReadsButNeverWrites(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.Merge(context.Background(), oneRow("ABC-1"), logAt); err != nil {
		t.Fatal(err)
	}
	s.Close()
	ro, err := sqlitestore.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if rows, err := ro.Rows(context.Background()); err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, err %v", rows, err)
	}
	if err := ro.Merge(context.Background(), oneRow("ABC-1"), logAt); err == nil {
		t.Fatal("a read-only store must refuse to write")
	}
}

func TestOpenReadOnlyNeverCreatesAnything(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if _, err := sqlitestore.OpenReadOnly(dir); err == nil {
		t.Fatal("expected an error for a missing database")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("OpenReadOnly created the folder")
	}
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "kanjo.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlitestore.OpenReadOnly(empty); err == nil {
		t.Fatal("an empty file is not a kanjo database")
	}
}

func TestOpenReadOnlyRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := sqlitestore.OpenReadOnly(dir); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v, want a newer-schema error", err)
	}
}

func TestOpenRefusesAGitignoreThatDoesNotIgnoreEverything(t *testing.T) {
	// A committed .gitignore in the data folder could make the database trackable.
	for name, content := range map[string]string{"other rules": "mine\n", "negation": "*\n!kanjo.db\n", "empty": ""} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlitestore.Open(dir); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: err = %v, want a refusal naming %s", name, err, path)
		}
	}
}

func TestOpenRefusesSymlinkedFiles(t *testing.T) {
	outside := t.TempDir()
	for name, link := range map[string]string{"database": "kanjo.db", "gitignore": ".gitignore"} {
		dir := t.TempDir()
		target := filepath.Join(outside, name+".target")
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlitestore.Open(dir); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("%s: err = %v, want a symbolic link refusal", name, err)
		}
		if _, err := os.Stat(target); err == nil {
			t.Errorf("%s: the link target was created or written", name)
		}
	}
	dir := t.TempDir()
	real := filepath.Join(outside, "real.db")
	s := open(t, outside)
	s.Close()
	if err := os.Rename(filepath.Join(outside, "kanjo.db"), real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "kanjo.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlitestore.OpenReadOnly(dir); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("read-only: err = %v, want a symbolic link refusal", err)
	}
}

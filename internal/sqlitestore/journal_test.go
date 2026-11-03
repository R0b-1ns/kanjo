package sqlitestore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

func TestJournalContract(t *testing.T) {
	kanjotest.JournalContract(t, func(t *testing.T) core.Journal { return open(t, t.TempDir()) })
}

func TestJournalSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	at := time.Date(2026, 11, 3, 10, 0, 0, 123_000_000, time.UTC)
	err := s.Update(context.Background(), "", func([]core.Marker, int) (*core.Marker, error) {
		return &core.Marker{At: at, Kind: core.MarkStart, Task: "ABC-1"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	ms, err := open(t, dir).Markers(context.Background())
	if err != nil || len(ms) != 1 || !ms[0].At.Equal(at) || ms[0].Task != "ABC-1" || ms[0].Session != "" {
		t.Fatalf("markers = %+v, err %v", ms, err)
	}
}

func TestHostileMarkerValuesStayData(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	bad := "'; DROP TABLE markers;--"
	err := s.Update(context.Background(), "", func([]core.Marker, int) (*core.Marker, error) {
		return &core.Marker{At: time.Now(), Kind: core.MarkStart, Task: bad}, nil
	})
	if err == nil {
		t.Fatal("a hostile label must be refused")
	}
	if ms, err := s.Markers(context.Background()); err != nil || len(ms) != 0 {
		t.Fatalf("markers = %+v, err %v", ms, err)
	}
}

func TestTamperedMarkerRowFailsTheRead(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO markers(at, kind, session, task) VALUES('2026-11-03T10:00:00.000Z','start','','../etc')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = open(t, dir).Markers(context.Background())
	if err == nil || strings.Contains(err.Error(), "etc") {
		t.Fatalf("err = %v, want an error without the value", err)
	}
}

func TestTamperedMarkerTimeFailsTheRead(t *testing.T) {
	dir := t.TempDir()
	open(t, dir).Close()
	db, _ := sql.Open("sqlite", filepath.Join(dir, "kanjo.db"))
	if _, err := db.Exec(`INSERT INTO markers(at, kind, session, task) VALUES('yesterday','end','',NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := open(t, dir).Markers(context.Background()); err == nil {
		t.Fatal("want an error")
	}
}

func TestReadOnlyStoreRefusesToAppend(t *testing.T) {
	dir := t.TempDir()
	open(t, dir).Close()
	ro, err := sqlitestore.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	err = ro.Update(context.Background(), "", func([]core.Marker, int) (*core.Marker, error) {
		return &core.Marker{At: time.Now(), Kind: core.MarkStart, Task: "ABC-1"}, nil
	})
	if err == nil {
		t.Fatal("a read-only store must refuse to append")
	}
	if ms, err := ro.Markers(context.Background()); err != nil || len(ms) != 0 {
		t.Fatalf("markers = %+v, err %v", ms, err)
	}
}

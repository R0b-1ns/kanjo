package cli

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
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

// addMarker appends a marker straight to the world's journal.
func (w *world) addMarker(session string, kind core.MarkerKind, at, task string) {
	w.t.Helper()
	ts, err := time.Parse(time.RFC3339, at)
	if err != nil {
		w.t.Fatal(err)
	}
	s, err := sqlitestore.Open(w.data)
	if err != nil {
		w.t.Fatal(err)
	}
	defer s.Close()
	err = s.Update(context.Background(), session, func([]core.Marker, int) (*core.Marker, error) {
		return &core.Marker{At: ts, Kind: kind, Session: session, Task: task}, nil
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) rows() []core.UsageRow {
	w.t.Helper()
	s, err := sqlitestore.OpenReadOnly(w.data)
	if err != nil {
		w.t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Rows(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return rows
}

func sessionTotals(rows []core.UsageRow, session string) (messages, marked int64, tasks map[string]int64) {
	tasks = map[string]int64{}
	for _, r := range rows {
		if r.Session == session {
			messages += r.Messages
			marked += r.Marked
			tasks[r.Task] += r.Messages
		}
	}
	return
}

const midDay = "2026-11-03T07:45:00Z" // between s-one's two opus messages of 11-03

func TestSnapshotWithoutMarkersIsUnchanged(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	for _, r := range w.rows() {
		if r.Marked != 0 {
			t.Fatalf("row %+v: marked without a marker", r.Key)
		}
	}
}

func TestSnapshotAttributesByMarkers(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	before, _, _ := sessionTotals(w.rows(), "s-one")
	w.addMarker("", core.MarkStart, midDay, "MARK-1")
	w.must("snapshot")
	messages, marked, tasks := sessionTotals(w.rows(), "s-one")
	if messages != before || marked == 0 || tasks["MARK-1"] != marked || tasks["ABC-12"] != messages-marked {
		t.Fatalf("messages %d (was %d), marked %d, tasks %v", messages, before, marked, tasks)
	}
}

func TestSnapshotWithMarkersIsIdempotent(t *testing.T) {
	w := newWorld(t)
	w.addMarker("", core.MarkStart, midDay, "MARK-1")
	w.must("snapshot")
	first := w.rows()
	w.must("snapshot")
	second := w.rows()
	if len(first) != len(second) {
		t.Fatalf("rows %d then %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("row %d changed: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestMarkerAddedLaterReattributesTheMessagesStillInTheTranscripts(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	w.addMarker("s-one", core.MarkStart, "2026-11-01T00:00:00Z", "LATE-1")
	w.must("snapshot")
	messages, marked, tasks := sessionTotals(w.rows(), "s-one")
	if marked != messages || tasks["LATE-1"] != messages || len(tasks) != 1 {
		t.Fatalf("messages %d marked %d tasks %v: every stored row of the session must move to the new task", messages, marked, tasks)
	}
}

func TestMarkerAddedAfterAPartialPurgeKeepsTheStoredSlot(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	transcript := filepath.Join(w.home, ".claude", "projects", "-home-dev-demo", "s-one.jsonl")
	data, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, `"id":"m6"`) && !strings.Contains(line, `"id":"m10"`) {
			kept = append(kept, line)
		}
	}
	write(t, transcript, strings.Join(kept, "\n"))
	w.addMarker("s-one", core.MarkStart, "2026-11-01T00:00:00Z", "LATE-1")
	w.must("snapshot")
	_, _, tasks := sessionTotals(w.rows(), "s-one")
	if tasks["ABC-12"] == 0 {
		t.Fatalf("tasks %v: the purged day must keep its stored attribution", tasks)
	}
	if tasks["LATE-1"] == 0 {
		t.Fatalf("tasks %v: the days still complete in the transcripts must follow the marker", tasks)
	}
}

func TestSnapshotRefusesATamperedMarkerRow(t *testing.T) {
	w := newWorld(t)
	w.addMarker("", core.MarkStart, midDay, "MARK-1")
	s, err := sqlitestore.Open(w.data)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	rawExec(t, filepath.Join(w.data, "kanjo.db"), `INSERT INTO markers(at, kind, session, task) VALUES('2026-11-03T08:00:00.000Z','start','','../etc')`)
	code, _, errb := w.run("snapshot")
	if code != 1 || strings.Contains(errb, "etc") {
		t.Fatalf("code %d stderr %q", code, errb)
	}
	if rows := w.rows(); len(rows) != 0 {
		t.Fatalf("rows = %d, want none written", len(rows))
	}
}

func rawExec(t *testing.T, dbPath, stmt string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(stmt); err != nil {
		t.Fatal(err)
	}
}

func (w *world) markers() []core.Marker {
	w.t.Helper()
	s, err := sqlitestore.OpenReadOnly(w.data)
	if err != nil {
		w.t.Fatal(err)
	}
	defer s.Close()
	ms, err := s.Markers(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return ms
}

package claudecode

import (
	"regexp"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

func startAt(id int64, ts, session, task string) core.Marker {
	at, _ := time.Parse(time.RFC3339, ts)
	return core.Marker{ID: id, At: at, Kind: core.MarkStart, Session: session, Task: task}
}

func endAt(id int64, ts, session string) core.Marker {
	at, _ := time.Parse(time.RFC3339, ts)
	return core.Marker{ID: id, At: at, Kind: core.MarkEnd, Session: session}
}

func withMarkers(t *testing.T, s Source, ms ...core.Marker) Source {
	t.Helper()
	tl, err := core.NewTimeline(ms)
	if err != nil {
		t.Fatal(err)
	}
	s.Markers = tl
	return s
}

// byTask returns messages and marked counts per session and task.
func byTask(b core.Batch) map[[2]string][2]int64 {
	out := map[[2]string][2]int64{}
	for _, r := range b.Rows {
		k := [2]string{r.Session, r.Task}
		v := out[k]
		out[k] = [2]int64{v[0] + r.Messages, v[1] + r.Marked}
	}
	return out
}

func userSays(text string) string {
	return `{"type":"user","message":{"content":"` + text + `"}}`
}

func TestMarkerOverridesMapAndHeuristic(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s1.jsonl", userSays("work on ABC-1"),
		asst("2026-11-03T10:00:00Z", "m1", "m", 1), asst("2026-11-03T10:10:00Z", "m2", "m", 1))
	src := tempSource(root)
	src.TaskPattern = regexp.MustCompile(`\b[A-Z]+-[0-9]+\b`)
	s := withMarkers(t, src, startAt(1, "2026-11-03T10:05:00Z", "", "MARK-1"))
	got := byTask(readOK(t, s))
	want := map[[2]string][2]int64{{"s1", "ABC-1"}: {1, 0}, {"s1", "MARK-1"}: {1, 1}}
	if len(got) != 2 || got[[2]string{"s1", "ABC-1"}] != want[[2]string{"s1", "ABC-1"}] || got[[2]string{"s1", "MARK-1"}] != want[[2]string{"s1", "MARK-1"}] {
		t.Fatalf("got %v, want %v", got, want)
	}
	s.TaskMap = map[string]string{"s1": "MAP-1"}
	got = byTask(readOK(t, s))
	if got[[2]string{"s1", "MAP-1"}] != [2]int64{1, 0} || got[[2]string{"s1", "MARK-1"}] != [2]int64{1, 1} {
		t.Fatalf("with a map: %v", got)
	}
}

func TestMessageBeforeTheFirstMarkerUsesTheFallback(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s1.jsonl", asst("2026-11-03T09:00:00Z", "m1", "m", 1))
	s := withMarkers(t, tempSource(root), startAt(1, "2026-11-03T10:00:00Z", "", "MARK-1"))
	if got := byTask(readOK(t, s)); got[[2]string{"s1", "-"}] != [2]int64{1, 0} {
		t.Fatalf("got %v", got)
	}
}

func TestMarkerBoundariesAreStartInclusiveEndExclusive(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s1.jsonl",
		asst("2026-11-03T10:00:00Z", "m1", "m", 1), // at the start
		asst("2026-11-03T10:30:00Z", "m2", "m", 1)) // at the end
	s := withMarkers(t, tempSource(root), startAt(1, "2026-11-03T10:00:00Z", "", "MARK-1"), endAt(2, "2026-11-03T10:30:00Z", ""))
	got := byTask(readOK(t, s))
	if got[[2]string{"s1", "MARK-1"}] != [2]int64{1, 1} || got[[2]string{"s1", "-"}] != [2]int64{1, 0} {
		t.Fatalf("got %v", got)
	}
}

func TestMarkerSplitsOneSessionAcrossTwoTasks(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s1.jsonl",
		asst("2026-11-03T10:00:00Z", "m1", "m", 1), asst("2026-11-03T10:01:00Z", "m2", "m", 1),
		asst("2026-11-03T10:20:00Z", "m3", "m", 1))
	s := withMarkers(t, tempSource(root),
		startAt(1, "2026-11-03T09:59:00Z", "", "A-1"), startAt(2, "2026-11-03T10:10:00Z", "", "B-2"))
	b := readOK(t, s)
	got := byTask(b)
	if got[[2]string{"s1", "A-1"}] != [2]int64{2, 2} || got[[2]string{"s1", "B-2"}] != [2]int64{1, 1} || len(b.Rows) != 2 {
		t.Fatalf("got %v, rows %+v", got, b.Rows)
	}
	for _, r := range b.Rows {
		if err := r.Validate(); err != nil {
			t.Fatalf("row %+v: %v", r, err)
		}
	}
}

func TestSessionBoundMarkerOnlyAppliesToItsSession(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s1.jsonl", asst("2026-11-03T10:00:00Z", "m1", "m", 1))
	write(t, root, "-p/s2.jsonl", asst("2026-11-03T10:00:00Z", "m2", "m", 1))
	s := withMarkers(t, tempSource(root),
		startAt(1, "2026-11-03T09:00:00Z", "", "GLOBAL-1"), startAt(2, "2026-11-03T09:00:00Z", "s1", "OWN-1"))
	got := byTask(readOK(t, s))
	if got[[2]string{"s1", "OWN-1"}] != [2]int64{1, 1} || got[[2]string{"s2", "GLOBAL-1"}] != [2]int64{1, 1} {
		t.Fatalf("got %v", got)
	}
}

func TestSubAgentMessagesFollowTheSessionMarkers(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s1.jsonl", asst("2026-11-03T10:00:00Z", "m1", "m", 1))
	write(t, root, "-p/s1/subagents/a.jsonl", asst("2026-11-03T10:01:00Z", "m2", "m", 1))
	s := withMarkers(t, tempSource(root), startAt(1, "2026-11-03T09:00:00Z", "s1", "OWN-1"))
	if got := byTask(readOK(t, s)); got[[2]string{"s1", "OWN-1"}] != [2]int64{2, 2} {
		t.Fatalf("got %v", got)
	}
}

func TestNoMarkersGivesTheSameRowsAsBefore(t *testing.T) {
	b := readOK(t, fixtureSource())
	for _, r := range b.Rows {
		if r.Marked != 0 {
			t.Fatalf("row %+v: marked without a marker", r)
		}
	}
}

func TestMarkerRowsKeepTheContractAndMergeIdempotently(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s1.jsonl", userSays("ABC-1"), asst("2026-11-03T10:00:00Z", "m1", "m", 1), asst("2026-11-03T11:00:00Z", "m2", "m", 1))
	s := withMarkers(t, tempSource(root), startAt(1, "2026-11-03T10:30:00Z", "", "MARK-1"))
	first, second := readOK(t, s), readOK(t, s)
	if len(first.Rows) != len(second.Rows) {
		t.Fatal("two reads differ")
	}
	for i := range first.Rows {
		if first.Rows[i] != second.Rows[i] {
			t.Fatalf("row %d differs: %+v vs %+v", i, first.Rows[i], second.Rows[i])
		}
	}
}

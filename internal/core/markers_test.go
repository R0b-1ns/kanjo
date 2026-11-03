package core

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func start(id int64, sec int, session, task string) Marker {
	return Marker{ID: id, At: at(sec), Kind: MarkStart, Session: session, Task: task}
}

func end(id int64, sec int, session string) Marker {
	return Marker{ID: id, At: at(sec), Kind: MarkEnd, Session: session}
}

func TestMarkerValidate(t *testing.T) {
	cases := []struct {
		name string
		m    Marker
		ok   bool
	}{
		{"global start", start(1, 0, "", "ABC-1"), true},
		{"session end", end(1, 0, "s1"), true},
		{"task dash on start", start(1, 0, "", "-"), false},
		{"traversal label", start(1, 0, "", "../x"), false},
		{"newline label", start(1, 0, "", "ABC\n1"), false},
		{"task on an end", Marker{At: t0, Kind: MarkEnd, Task: "ABC-1"}, false},
		{"missing task on a start", Marker{At: t0, Kind: MarkStart}, false},
		{"leading dash session", start(1, 0, "-x", "ABC-1"), false},
		{"zero time", Marker{Kind: MarkEnd}, false},
		{"unknown kind", Marker{At: t0, Kind: "pause"}, false},
	}
	for _, c := range cases {
		if err := c.m.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
		}
	}
}

func TestMarkerErrorsNeverEchoTheValue(t *testing.T) {
	err := start(1, 0, "", "secret\x1b[31m").Validate()
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("err = %v", err)
	}
}

func timeline(t *testing.T, ms ...Marker) Timeline {
	t.Helper()
	tl, err := NewTimeline(ms)
	if err != nil {
		t.Fatalf("NewTimeline: %v", err)
	}
	return tl
}

func wantTask(t *testing.T, tl Timeline, session string, sec int, want string) {
	t.Helper()
	got, ok := tl.TaskAt(session, at(sec))
	if want == "" {
		if ok {
			t.Errorf("TaskAt(%q, +%ds) = %q, want none", session, sec, got)
		}
		return
	}
	if !ok || got != want {
		t.Errorf("TaskAt(%q, +%ds) = %q, %v, want %q", session, sec, got, ok, want)
	}
}

func TestTaskAtInsideInterval(t *testing.T) {
	tl := timeline(t, start(1, 10, "", "A-1"), end(2, 20, ""))
	wantTask(t, tl, "s", 15, "A-1")
}

func TestTaskAtStartInstantBelongsToTheNewTask(t *testing.T) {
	tl := timeline(t, start(1, 10, "", "A-1"))
	wantTask(t, tl, "s", 10, "A-1")
	wantTask(t, tl, "s", 9, "")
}

func TestTaskAtEndInstantIsOutside(t *testing.T) {
	tl := timeline(t, start(1, 10, "", "A-1"), end(2, 20, ""))
	wantTask(t, tl, "s", 20, "")
}

func TestTaskAtOpenStartIsStillActive(t *testing.T) {
	tl := timeline(t, start(1, 10, "", "A-1"))
	wantTask(t, tl, "s", 100000, "A-1")
}

func TestTaskAtBeforeFirstMarker(t *testing.T) {
	wantTask(t, timeline(t, start(1, 10, "", "A-1")), "s", 0, "")
	wantTask(t, timeline(t), "s", 0, "")
}

func TestTwoStartsLastWins(t *testing.T) {
	tl := timeline(t, start(1, 10, "", "A-1"), start(2, 20, "", "B-2"))
	wantTask(t, tl, "s", 15, "A-1")
	wantTask(t, tl, "s", 25, "B-2")
}

func TestSessionMarkerBeatsGlobal(t *testing.T) {
	tl := timeline(t, start(1, 0, "", "A-1"), start(2, 10, "s", "B-2"))
	wantTask(t, tl, "s", 15, "B-2")
	wantTask(t, tl, "s", 5, "A-1")
}

func TestSessionEndFallsBackToGlobal(t *testing.T) {
	tl := timeline(t, start(1, 0, "", "A-1"), start(2, 10, "s", "B-2"), end(3, 20, "s"))
	wantTask(t, tl, "s", 25, "A-1")
}

func TestSessionMarkerOnlyAffectsItsSession(t *testing.T) {
	tl := timeline(t, start(1, 0, "s", "B-2"))
	wantTask(t, tl, "other", 5, "")
}

func TestNewTimelineSortsUnorderedInput(t *testing.T) {
	tl := timeline(t, end(2, 20, ""), start(1, 10, "", "A-1"))
	wantTask(t, tl, "s", 15, "A-1")
	wantTask(t, tl, "s", 25, "")
}

func TestNewTimelineRejectsAnInvalidEvent(t *testing.T) {
	if _, err := NewTimeline([]Marker{start(1, 0, "", "../x")}); err == nil {
		t.Error("want an error")
	}
}

func TestFutureEventDoesNotAffectEarlierMessages(t *testing.T) {
	tl := timeline(t, start(1, 3600, "", "A-1"))
	wantTask(t, tl, "s", 10, "")
}

func TestSameInstantFollowsTheId(t *testing.T) {
	tl := timeline(t, start(2, 10, "", "B-2"), start(1, 10, "", "A-1"))
	wantTask(t, tl, "s", 10, "B-2")
}

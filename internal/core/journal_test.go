package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

// memJournal is the smallest Journal: a slice, no locking.
type memJournal struct{ events []Marker }

func (j *memJournal) Update(_ context.Context, session string, fn func([]Marker, int) (*Marker, error)) error {
	var scope []Marker
	for _, m := range j.events {
		if m.Session == session {
			scope = append(scope, m)
		}
	}
	m, err := fn(scope, len(j.events))
	if err != nil || m == nil {
		return err
	}
	m.ID = int64(len(j.events) + 1)
	j.events = append(j.events, *m)
	return nil
}

func (j *memJournal) Markers(context.Context) ([]Marker, error) { return j.events, nil }

type clockAt struct{ t *time.Time }

func (c clockAt) Now() time.Time { return *c.t }

func newJournal(sec int) (*memJournal, clockAt, *time.Time) {
	now := at(sec)
	return &memJournal{}, clockAt{&now}, &now
}

var bg = context.Background()

func TestStartAppendsAStart(t *testing.T) {
	j, clk, _ := newJournal(5)
	m, started, err := StartTask(bg, j, clk, "", "A-1", 10)
	if err != nil || !started || m.Kind != MarkStart || m.Task != "A-1" || !m.At.Equal(at(5)) {
		t.Fatalf("got %+v %v %v", m, started, err)
	}
	if len(j.events) != 1 {
		t.Errorf("events = %d", len(j.events))
	}
}

func TestStartTruncatesToMilliseconds(t *testing.T) {
	j, clk, now := newJournal(0)
	*now = t0.Add(1234567 * time.Nanosecond)
	m, _, _ := StartTask(bg, j, clk, "", "A-1", 10)
	if !m.At.Equal(t0.Add(1 * time.Millisecond)) {
		t.Errorf("at = %v", m.At)
	}
}

func TestStartOfTheActiveTaskWritesNothing(t *testing.T) {
	j, clk, now := newJournal(5)
	StartTask(bg, j, clk, "", "A-1", 10)
	*now = at(9)
	_, started, err := StartTask(bg, j, clk, "", "A-1", 10)
	if err != nil || started || len(j.events) != 1 {
		t.Errorf("started = %v, err = %v, events = %d", started, err, len(j.events))
	}
}

func TestStartSupersedesAnotherActiveTask(t *testing.T) {
	j, clk, now := newJournal(5)
	StartTask(bg, j, clk, "", "A-1", 10)
	*now = at(9)
	if _, started, err := StartTask(bg, j, clk, "", "B-2", 10); err != nil || !started {
		t.Fatalf("started = %v, err = %v", started, err)
	}
	tl, _ := NewTimeline(j.events)
	wantTask(t, tl, "s", 7, "A-1")
	wantTask(t, tl, "s", 9, "B-2")
}

func TestEndAppendsAnEnd(t *testing.T) {
	j, clk, now := newJournal(5)
	StartTask(bg, j, clk, "", "A-1", 10)
	*now = at(9)
	m, err := EndTask(bg, j, clk, "", 10)
	if err != nil || m.Kind != MarkEnd {
		t.Fatalf("got %+v %v", m, err)
	}
}

func TestEndWithNothingActiveIsRefused(t *testing.T) {
	j, clk, _ := newJournal(5)
	if _, err := EndTask(bg, j, clk, "", 10); !errors.Is(err, ErrNothingToEnd) {
		t.Errorf("err = %v", err)
	}
	StartTask(bg, j, clk, "", "A-1", 10)
	EndTask(bg, j, clk, "", 10)
	if _, err := EndTask(bg, j, clk, "", 10); !errors.Is(err, ErrNothingToEnd) {
		t.Errorf("after an end: err = %v", err)
	}
}

func TestEndOfSessionWhileOnlyGlobalActiveIsRefused(t *testing.T) {
	j, clk, _ := newJournal(5)
	StartTask(bg, j, clk, "", "A-1", 10)
	if _, err := EndTask(bg, j, clk, "s1", 10); !errors.Is(err, ErrNothingToEnd) {
		t.Errorf("err = %v", err)
	}
}

func TestStartRefusedWhenClockIsBehind(t *testing.T) {
	j, clk, now := newJournal(50)
	StartTask(bg, j, clk, "", "A-1", 10)
	*now = at(40)
	_, _, err := StartTask(bg, j, clk, "", "B-2", 10)
	if !errors.Is(err, ErrClockBehind) || len(j.events) != 1 {
		t.Errorf("err = %v, events = %d", err, len(j.events))
	}
}

func TestEndRefusedWhenClockIsBehind(t *testing.T) {
	j, clk, now := newJournal(50)
	StartTask(bg, j, clk, "", "A-1", 10)
	*now = at(40)
	if _, err := EndTask(bg, j, clk, "", 10); !errors.Is(err, ErrClockBehind) {
		t.Errorf("err = %v", err)
	}
}

func TestClockBehindIsPerScope(t *testing.T) {
	j, clk, now := newJournal(50)
	StartTask(bg, j, clk, "", "A-1", 10)
	*now = at(40)
	if _, _, err := StartTask(bg, j, clk, "s1", "B-2", 10); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestStartRefusedAtTheCap(t *testing.T) {
	j, clk, now := newJournal(1)
	StartTask(bg, j, clk, "", "A-1", 2)
	*now = at(2)
	StartTask(bg, j, clk, "", "B-2", 2)
	*now = at(3)
	if _, _, err := StartTask(bg, j, clk, "", "C-3", 2); !errors.Is(err, ErrJournalFull) {
		t.Errorf("start: err = %v", err)
	}
	if _, err := EndTask(bg, j, clk, "", 2); !errors.Is(err, ErrJournalFull) {
		t.Errorf("end: err = %v", err)
	}
}

func TestStartRejectsAnInvalidLabelBeforeTouchingTheJournal(t *testing.T) {
	j := &failingJournal{}
	clk := clockAt{&t0}
	if _, started, err := StartTask(bg, j, clk, "", "../x", 10); err == nil || started || j.called {
		t.Errorf("err = %v, started = %v, called = %v", err, started, j.called)
	}
	if _, _, err := StartTask(bg, j, clk, "-x", "A-1", 10); err == nil || j.called {
		t.Errorf("session: err = %v, called = %v", err, j.called)
	}
}

type failingJournal struct{ called bool }

func (f *failingJournal) Update(context.Context, string, func([]Marker, int) (*Marker, error)) error {
	f.called = true
	return nil
}
func (f *failingJournal) Markers(context.Context) ([]Marker, error) { return nil, nil }

func TestActiveAtIgnoresFutureEvents(t *testing.T) {
	tl := timeline(t, start(1, 3600, "", "A-1"))
	if _, ok := tl.ActiveAt("", at(0)); ok {
		t.Error("a future start must not be active")
	}
}

func TestActiveAtOfASessionFallsBackToGlobal(t *testing.T) {
	tl := timeline(t, start(1, 0, "", "A-1"))
	m, ok := tl.ActiveAt("s1", at(5))
	if !ok || m.Task != "A-1" || m.Session != "" {
		t.Errorf("got %+v %v", m, ok)
	}
}

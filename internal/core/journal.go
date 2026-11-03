package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrClockBehind means the clock is earlier than the scope's last marker.
	ErrClockBehind = errors.New("the clock is behind the last marker")
	// ErrNothingToEnd means no task is active in the scope.
	ErrNothingToEnd = errors.New("no active task to end")
	// ErrJournalFull means the journal holds the configured maximum of events.
	ErrJournalFull = errors.New("the marker journal is full (markers.max_events)")
)

// Journal is the append-only store of markers.
type Journal interface {
	// Update runs fn on the events of one scope (session "" is the global
	// scope), ordered by time then id, and on the number of events in the whole
	// journal, inside an exclusive transaction. A non-nil Marker returned by
	// fn is validated and appended with the next id; an error from fn writes
	// nothing and is returned as is.
	Update(ctx context.Context, session string, fn func(scope []Marker, total int) (*Marker, error)) error
	// Markers returns every event, ordered by time then id.
	Markers(ctx context.Context) ([]Marker, error)
}

// StartTask records that task starts now in the scope. started is false when
// the scope's active task already is the task: nothing is written then.
func StartTask(ctx context.Context, j Journal, clk Clock, session, task string, maxEvents int) (m Marker, started bool, err error) {
	now := clk.Now().UTC().Truncate(time.Millisecond)
	next := Marker{At: now, Kind: MarkStart, Session: session, Task: task}
	if err := next.Validate(); err != nil {
		return Marker{}, false, err
	}
	err = j.Update(ctx, session, func(scope []Marker, total int) (*Marker, error) {
		if last := lastOf(scope); last.Kind == MarkStart && last.Task == task {
			m = last
			return nil, nil
		}
		if err := admit(scope, total, now, maxEvents); err != nil {
			return nil, err
		}
		started, m = true, next
		return &next, nil
	})
	return m, started, err
}

// EndTask records that the scope's active task ends now.
func EndTask(ctx context.Context, j Journal, clk Clock, session string, maxEvents int) (m Marker, err error) {
	now := clk.Now().UTC().Truncate(time.Millisecond)
	next := Marker{At: now, Kind: MarkEnd, Session: session}
	if err := next.Validate(); err != nil {
		return Marker{}, err
	}
	err = j.Update(ctx, session, func(scope []Marker, total int) (*Marker, error) {
		if lastOf(scope).Kind != MarkStart {
			return nil, ErrNothingToEnd
		}
		if err := admit(scope, total, now, maxEvents); err != nil {
			return nil, err
		}
		m = next
		return &next, nil
	})
	return m, err
}

// admit checks the clock against the scope's last event and the journal cap.
func admit(scope []Marker, total int, now time.Time, maxEvents int) error {
	if last := lastOf(scope); now.Before(last.At) { // the zero marker of an empty scope is in year 1
		return fmt.Errorf("%w (%s)", ErrClockBehind, last.At.UTC().Format(MarkerTimeLayout))
	}
	if total >= maxEvents {
		return ErrJournalFull
	}
	return nil
}

// lastOf is the scope's latest event, or the zero Marker (no kind, year 1).
func lastOf(scope []Marker) Marker {
	if len(scope) == 0 {
		return Marker{}
	}
	return scope[len(scope)-1]
}

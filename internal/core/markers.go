package core

import (
	"errors"
	"slices"
	"sort"
	"time"
)

// MarkerTimeLayout is how a marker time is stored: UTC, millisecond precision.
const MarkerTimeLayout = "2006-01-02T15:04:05.000Z"

// MarkerKind says whether a marker starts or ends a task.
type MarkerKind string

const (
	MarkStart MarkerKind = "start"
	MarkEnd   MarkerKind = "end"
)

// Marker is one event of the journal (spec section 15). Session "" is the
// global scope. Task is set on a start and empty on an end.
type Marker struct {
	ID      int64
	At      time.Time
	Kind    MarkerKind
	Session string
	Task    string
}

// Validate checks the closed rules of a marker. Errors never echo values.
func (m Marker) Validate() error {
	switch {
	case m.At.IsZero():
		return errors.New("marker has no time")
	case m.Session != "" && !ValidSession(m.Session):
		return errors.New("invalid session id")
	case m.Kind == MarkStart && (m.Task == NoTask || !ValidTask(m.Task)):
		return errors.New("invalid task label")
	case m.Kind == MarkEnd && m.Task != "":
		return errors.New("an end carries no task")
	case m.Kind != MarkStart && m.Kind != MarkEnd:
		return errors.New("invalid marker kind")
	}
	return nil
}

// before orders markers by time, then id.
func (m Marker) before(o Marker) bool {
	if !m.At.Equal(o.At) {
		return m.At.Before(o.At)
	}
	return m.ID < o.ID
}

// Timeline answers which task a marker assigns to an instant.
type Timeline struct{ scopes map[string][]Marker }

// NewTimeline validates the markers and groups them by scope, ordered by time
// then id.
func NewTimeline(ms []Marker) (Timeline, error) {
	tl := Timeline{scopes: map[string][]Marker{}}
	for _, m := range ms {
		if err := m.Validate(); err != nil {
			return Timeline{}, err
		}
		tl.scopes[m.Session] = append(tl.scopes[m.Session], m)
	}
	for _, s := range tl.scopes {
		slices.SortFunc(s, func(a, b Marker) int {
			switch {
			case a.before(b):
				return -1
			case b.before(a):
				return 1
			}
			return 0
		})
	}
	return tl, nil
}

// TaskAt returns the task of the session's own timeline at the instant, else
// the global one; ok is false when neither has a task running.
func (t Timeline) TaskAt(session string, at time.Time) (task string, ok bool) {
	if session != "" {
		if task, ok = t.scopeTaskAt(session, at); ok {
			return task, true
		}
	}
	return t.scopeTaskAt("", at)
}

// scopeTaskAt looks at the latest event of one scope at or before the instant.
func (t Timeline) scopeTaskAt(scope string, at time.Time) (string, bool) {
	s := t.scopes[scope]
	i := sort.Search(len(s), func(i int) bool { return s[i].At.After(at) })
	if i == 0 || s[i-1].Kind != MarkStart {
		return "", false
	}
	return s[i-1].Task, true
}

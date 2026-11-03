package core

import (
	"cmp"
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
	}
	return m.validateKind()
}

func (m Marker) validateKind() error {
	switch m.Kind {
	case MarkStart:
		if m.Task == NoTask || !ValidTask(m.Task) {
			return errors.New("invalid task label")
		}
	case MarkEnd:
		if m.Task != "" {
			return errors.New("an end carries no task")
		}
	default:
		return errors.New("invalid marker kind")
	}
	return nil
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
			return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.ID, b.ID))
		})
	}
	return tl, nil
}

// TaskAt returns the task of the session's own timeline at the instant, else
// the global one; ok is false when neither has a task running.
func (t Timeline) TaskAt(session string, at time.Time) (task string, ok bool) {
	m, ok := t.ActiveAt(session, at)
	return m.Task, ok
}

// ActiveAt returns the start marker that assigns a task at the instant: the
// session's own, else the global one.
func (t Timeline) ActiveAt(session string, at time.Time) (Marker, bool) {
	if session != "" {
		if m, ok := t.scopeActiveAt(session, at); ok {
			return m, true
		}
	}
	return t.scopeActiveAt("", at)
}

// scopeActiveAt looks at the latest event of one scope at or before the instant.
func (t Timeline) scopeActiveAt(scope string, at time.Time) (Marker, bool) {
	s := t.scopes[scope]
	i := sort.Search(len(s), func(i int) bool { return s[i].At.After(at) })
	if i == 0 || s[i-1].Kind != MarkStart {
		return Marker{}, false
	}
	return s[i-1], true
}

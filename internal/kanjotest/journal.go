package kanjotest

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// MemJournal is an in-memory core.Journal.
type MemJournal struct {
	mu     sync.Mutex
	events []core.Marker
	nextID int64
}

// NewMemJournal returns an empty journal.
func NewMemJournal() *MemJournal { return &MemJournal{nextID: 1} }

// Update implements core.Journal.
func (j *MemJournal) Update(_ context.Context, session string, fn func([]core.Marker, int) (*core.Marker, error)) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	var scope []core.Marker
	for _, m := range sorted(j.events) {
		if m.Session == session {
			scope = append(scope, m)
		}
	}
	m, err := fn(scope, len(j.events))
	if err != nil || m == nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	m.ID = j.nextID
	j.nextID++
	j.events = append(j.events, *m)
	return nil
}

// Markers implements core.Journal.
func (j *MemJournal) Markers(context.Context) ([]core.Marker, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return sorted(j.events), nil
}

func sorted(ms []core.Marker) []core.Marker {
	out := slices.Clone(ms)
	sort.Slice(out, func(i, k int) bool {
		if !out[i].At.Equal(out[k].At) {
			return out[i].At.Before(out[k].At)
		}
		return out[i].ID < out[k].ID
	})
	return out
}

var jt0 = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func mk(sec int, kind core.MarkerKind, session, task string) *core.Marker {
	return &core.Marker{At: jt0.Add(time.Duration(sec) * time.Second), Kind: kind, Session: session, Task: task}
}

func appendOne(t *testing.T, j core.Journal, session string, m *core.Marker) {
	t.Helper()
	if err := j.Update(context.Background(), session, func([]core.Marker, int) (*core.Marker, error) { return m, nil }); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func allOf(t *testing.T, j core.Journal) []core.Marker {
	t.Helper()
	ms, err := j.Markers(context.Background())
	if err != nil {
		t.Fatalf("Markers: %v", err)
	}
	return ms
}

var journalCases = []contractCase[core.Journal]{
	{"an empty journal has no events", func(t *testing.T, j core.Journal) {
		if ms := allOf(t, j); len(ms) != 0 {
			t.Fatalf("events = %v", ms)
		}
	}},
	{"events come back ordered by time then id", func(t *testing.T, j core.Journal) {
		appendOne(t, j, "", mk(20, core.MarkEnd, "", ""))
		appendOne(t, j, "", mk(10, core.MarkStart, "", "A-1"))
		appendOne(t, j, "", mk(10, core.MarkStart, "", "B-2"))
		ms := allOf(t, j)
		if len(ms) != 3 || ms[0].Task != "A-1" || ms[1].Task != "B-2" || ms[2].Kind != core.MarkEnd {
			t.Fatalf("events = %+v", ms)
		}
	}},
	{"an event round-trips", func(t *testing.T, j core.Journal) {
		appendOne(t, j, "s-1", mk(5, core.MarkStart, "s-1", "ABC-1"))
		ms := allOf(t, j)
		if len(ms) != 1 || ms[0].Session != "s-1" || ms[0].Task != "ABC-1" || ms[0].Kind != core.MarkStart || !ms[0].At.Equal(jt0.Add(5*time.Second)) || ms[0].ID == 0 {
			t.Fatalf("events = %+v", ms)
		}
	}},
	{"an error from fn writes nothing", func(t *testing.T, j core.Journal) {
		want := fmt.Errorf("boom")
		err := j.Update(context.Background(), "", func([]core.Marker, int) (*core.Marker, error) {
			return mk(1, core.MarkStart, "", "A-1"), want
		})
		if err != want || len(allOf(t, j)) != 0 {
			t.Fatalf("err = %v, events = %v", err, allOf(t, j))
		}
	}},
	{"nil from fn writes nothing", func(t *testing.T, j core.Journal) {
		appendOne(t, j, "", nil)
		if len(allOf(t, j)) != 0 {
			t.Fatal("want no event")
		}
	}},
	{"an invalid event is refused", func(t *testing.T, j core.Journal) {
		err := j.Update(context.Background(), "", func([]core.Marker, int) (*core.Marker, error) {
			return mk(1, core.MarkStart, "", "../x"), nil
		})
		if err == nil || len(allOf(t, j)) != 0 {
			t.Fatalf("err = %v, events = %v", err, allOf(t, j))
		}
	}},
	{"ids are strictly increasing", func(t *testing.T, j core.Journal) {
		for i := 0; i < 3; i++ {
			appendOne(t, j, "", mk(i, core.MarkStart, "", "A-1"))
		}
		ms := allOf(t, j)
		if ms[0].ID >= ms[1].ID || ms[1].ID >= ms[2].ID {
			t.Fatalf("ids = %d %d %d", ms[0].ID, ms[1].ID, ms[2].ID)
		}
	}},
	{"fn sees its scope and the journal total", func(t *testing.T, j core.Journal) {
		appendOne(t, j, "", mk(1, core.MarkStart, "", "A-1"))
		appendOne(t, j, "s-1", mk(2, core.MarkStart, "s-1", "B-2"))
		appendOne(t, j, "s-1", mk(3, core.MarkEnd, "s-1", ""))
		var gotScope []core.Marker
		var gotTotal int
		j.Update(context.Background(), "s-1", func(s []core.Marker, total int) (*core.Marker, error) {
			gotScope, gotTotal = s, total
			return nil, nil
		})
		if len(gotScope) != 2 || gotScope[0].Task != "B-2" || gotScope[1].Kind != core.MarkEnd || gotTotal != 3 {
			t.Fatalf("scope = %+v, total = %d", gotScope, gotTotal)
		}
	}},
	{"concurrent updates are all kept and serialized", func(t *testing.T, j core.Journal) {
		const n = 16
		var mu sync.Mutex
		var seen []int
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := j.Update(context.Background(), "", func(s []core.Marker, total int) (*core.Marker, error) {
					mu.Lock()
					seen = append(seen, total)
					mu.Unlock()
					return mk(0, core.MarkStart, "", "A-1"), nil
				})
				if err != nil {
					t.Errorf("Update: %v", err)
				}
			}()
		}
		wg.Wait()
		slices.Sort(seen)
		for i, total := range seen {
			if total != i {
				t.Fatalf("totals seen = %v, want 0..%d each once", seen, n-1)
			}
		}
		if got := len(allOf(t, j)); got != n {
			t.Fatalf("events = %d, want %d", got, n)
		}
	}},
}

// JournalContract checks the behavior every core.Journal must have. open
// must return a fresh, empty journal.
func JournalContract(t *testing.T, open func(t *testing.T) core.Journal) {
	runCases(t, journalCases, open)
}

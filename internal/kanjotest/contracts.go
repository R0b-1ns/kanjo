package kanjotest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

func row(session, day, task, model string, in int64, cost *float64) core.UsageRow {
	return core.UsageRow{
		Key:             core.Key{Source: "claude", Session: session, Day: day, Task: task, Model: model},
		Messages:        1,
		Tokens:          core.Tokens{Input: in},
		CostReportedUSD: cost,
	}
}

func sess(session string) core.SessionInfo {
	return core.SessionInfo{Source: "claude", Session: session, TaskSource: "none"}
}

func batch(rows []core.UsageRow, sessions ...string) core.Batch {
	b := core.Batch{Rows: rows}
	for _, s := range sessions {
		b.Sessions = append(b.Sessions, sess(s))
	}
	return b
}

var storeLog = core.SnapshotLog{At: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)}

func mustReplace(t *testing.T, s core.Store, b core.Batch) {
	t.Helper()
	if err := s.Merge(context.Background(), b, storeLog); err != nil {
		t.Fatalf("Merge: %v", err)
	}
}

func rowsOf(t *testing.T, s core.Store) []core.UsageRow {
	t.Helper()
	rows, err := s.Rows(context.Background())
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	SortRows(rows)
	return rows
}

func sessionsOf(t *testing.T, s core.Store) []core.SessionInfo {
	t.Helper()
	ss, err := s.Sessions(context.Background())
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	return ss
}

func wantRows(t *testing.T, s core.Store, want []core.UsageRow) {
	t.Helper()
	got := rowsOf(t, s)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

var storeCases = []struct {
	name string
	run  func(t *testing.T, s core.Store)
}{
	{"empty store has no rows or sessions", func(t *testing.T, s core.Store) {
		wantRows(t, s, []core.UsageRow{})
		if ss := sessionsOf(t, s); len(ss) != 0 {
			t.Fatalf("sessions = %v", ss)
		}
	}},
	{"replace then read round-trips", func(t *testing.T, s core.Store) {
		want := []core.UsageRow{row("s-1", "2026-11-03", "ABC-1", "m", 5, nil), row("s-1", "2026-11-04", "ABC-1", "m", 6, nil)}
		mustReplace(t, s, batch(want, "s-1"))
		wantRows(t, s, want)
		if ss := sessionsOf(t, s); len(ss) != 1 || ss[0] != sess("s-1") {
			t.Fatalf("sessions = %v", ss)
		}
	}},
	{"a new task on the same day and model replaces the old row", func(t *testing.T, s core.Store) {
		mustReplace(t, s, batch([]core.UsageRow{row("s-1", "2026-11-03", "ABC-1", "m", 5, nil)}, "s-1"))
		again := []core.UsageRow{row("s-1", "2026-11-03", "XYZ-2", "m", 5, nil)}
		mustReplace(t, s, batch(again, "s-1"))
		wantRows(t, s, again)
	}},
	{"rows the source no longer has are kept", func(t *testing.T, s core.Store) {
		gone, stale := row("s-1", "2026-11-03", "ABC-1", "m", 5, nil), row("s-1", "2026-11-04", "ABC-1", "m", 6, nil)
		mustReplace(t, s, batch([]core.UsageRow{gone, stale}, "s-1"))
		fresh := row("s-1", "2026-11-04", "ABC-1", "m", 9, nil)
		mustReplace(t, s, batch([]core.UsageRow{fresh}, "s-1"))
		wantRows(t, s, []core.UsageRow{gone, fresh})
	}},
	{"untouched sessions are kept", func(t *testing.T, s core.Store) {
		old := row("s-old", "2026-10-01", "ABC-1", "m", 1, nil)
		fresh := row("s-new", "2026-11-03", "ABC-1", "m", 2, nil)
		mustReplace(t, s, batch([]core.UsageRow{old}, "s-old"))
		mustReplace(t, s, batch([]core.UsageRow{fresh}, "s-new"))
		wantRows(t, s, []core.UsageRow{fresh, old})
	}},
	{"replace twice is idempotent", func(t *testing.T, s core.Store) {
		want := []core.UsageRow{row("s-1", "2026-11-03", "ABC-1", "m", 5, nil)}
		mustReplace(t, s, batch(want, "s-1"))
		mustReplace(t, s, batch(want, "s-1"))
		wantRows(t, s, want)
	}},
	{"reported cost round-trips, nil stays nil", func(t *testing.T, s core.Store) {
		c := 0.25
		free, paid := row("s-1", "2026-11-03", "ABC-1", "free", 1, nil), row("s-1", "2026-11-03", "ABC-1", "paid", 1, &c)
		mustReplace(t, s, batch([]core.UsageRow{free, paid}, "s-1"))
		wantRows(t, s, []core.UsageRow{free, paid})
	}},
	{"odd but valid labels round-trip", func(t *testing.T, s core.Store) {
		want := []core.UsageRow{row("s-1", "2026-11-03", "a.b-c/d", "m", 1, nil)}
		mustReplace(t, s, batch(want, "s-1"))
		wantRows(t, s, want)
	}},
}

// StoreContract checks the behavior every core.Store must have. open must
// return a fresh, empty store.
func StoreContract(t *testing.T, open func(t *testing.T) core.Store) {
	for _, c := range storeCases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

// PriceBookContract checks a core.PriceBook; known is a model it prices.
func PriceBookContract(t *testing.T, pb core.PriceBook, known string) {
	t.Helper()
	p, ok := pb.Lookup(known, false)
	if !ok {
		t.Fatalf("%s must be priced", known)
	}
	for _, v := range []float64{p.Input, p.Output, p.CacheWrite5m, p.CacheWrite1h, p.CacheRead} {
		if v < 0 {
			t.Fatalf("negative price in %+v", p)
		}
	}
	if _, ok := pb.Lookup("no-such-model-xyz", false); ok {
		t.Fatal("unknown model must be unpriced")
	}
	if pb.LongContext(known, 0) {
		t.Fatal("an empty prompt is never long context")
	}
	if pb.LongContext("no-such-model-xyz", 1<<40) {
		t.Fatal("unknown model has no long-context tier")
	}
}

// UsageSourceContract checks that a source only yields valid, consistent data.
func UsageSourceContract(t *testing.T, src core.UsageSource) {
	t.Helper()
	b, err := src.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if b.Skipped < 0 {
		t.Fatalf("Skipped = %d", b.Skipped)
	}
	listed := checkSessions(t, b.Sessions)
	for _, r := range b.Rows {
		if err := r.Validate(); err != nil {
			t.Fatalf("row %+v: %v", r.Key, err)
		}
		if !listed[sessionKey{r.Source, r.Session}] {
			t.Fatalf("row of session %q is not listed in Sessions", r.Session)
		}
	}
}

func checkSessions(t *testing.T, sessions []core.SessionInfo) map[sessionKey]bool {
	t.Helper()
	listed := map[sessionKey]bool{}
	for _, s := range sessions {
		if err := s.Validate(); err != nil {
			t.Fatalf("session %+v: %v", s, err)
		}
		listed[sessionKey{s.Source, s.Session}] = true
	}
	return listed
}

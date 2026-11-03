// Package kanjotest holds the fakes and the contract suites of the core ports.
// It is only used by tests.
package kanjotest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

type sessionKey struct{ source, session string }

// MemStore is an in-memory core.Store.
type MemStore struct {
	mu       sync.Mutex
	rows     map[core.Key]core.UsageRow
	sessions map[sessionKey]core.SessionInfo
	logs     []core.SnapshotLog
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{rows: map[core.Key]core.UsageRow{}, sessions: map[sessionKey]core.SessionInfo{}}
}

// ReplaceSessions implements core.Store.
func (m *MemStore) ReplaceSessions(_ context.Context, b core.Batch, log core.SnapshotLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	replaced := map[sessionKey]bool{}
	for _, s := range b.Sessions {
		replaced[sessionKey{s.Source, s.Session}] = true
	}
	for k := range m.rows {
		if replaced[sessionKey{k.Source, k.Session}] {
			delete(m.rows, k)
		}
	}
	for _, r := range b.Rows {
		m.rows[r.Key] = copyRow(r)
	}
	for _, s := range b.Sessions {
		m.sessions[sessionKey{s.Source, s.Session}] = s
	}
	m.logs = append(m.logs, log)
	return nil
}

// Rows implements core.Store; rows come back sorted by key.
func (m *MemStore) Rows(context.Context) ([]core.UsageRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]core.UsageRow, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, copyRow(r))
	}
	SortRows(out)
	return out, nil
}

// Sessions implements core.Store; sessions come back sorted.
func (m *MemStore) Sessions(context.Context) ([]core.SessionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]core.SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	return out, nil
}

// Logs returns the snapshot logs in order.
func (m *MemStore) Logs() []core.SnapshotLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]core.SnapshotLog(nil), m.logs...)
}

// SortRows orders rows by their key, for stable comparisons.
func SortRows(rows []core.UsageRow) {
	sort.Slice(rows, func(i, j int) bool { return fmt.Sprint(rows[i].Key) < fmt.Sprint(rows[j].Key) })
}

func copyRow(r core.UsageRow) core.UsageRow {
	if r.CostReportedUSD != nil {
		c := *r.CostReportedUSD
		r.CostReportedUSD = &c
	}
	return r
}

// StaticSource is a core.UsageSource returning a fixed batch or error.
type StaticSource struct {
	Batch core.Batch
	Err   error
}

// Read implements core.UsageSource.
func (s StaticSource) Read(context.Context) (core.Batch, error) { return s.Batch, s.Err }

// FixedClock is a core.Clock that always returns T.
type FixedClock struct{ T time.Time }

// Now implements core.Clock.
func (c FixedClock) Now() time.Time { return c.T }

// TablePrices is a core.PriceBook backed by maps. Long holds the long-context
// tier of a model; Threshold the exclusive prompt size above which it applies.
type TablePrices struct {
	Prices    map[string]core.Price
	Long      map[string]core.Price
	Threshold map[string]int64
}

// Lookup implements core.PriceBook; a model without a long tier falls back to
// its base price.
func (t TablePrices) Lookup(model string, longContext bool) (core.Price, bool) {
	if longContext {
		if p, ok := t.Long[model]; ok {
			return p, true
		}
	}
	p, ok := t.Prices[model]
	return p, ok
}

// LongContext implements core.PriceBook.
func (t TablePrices) LongContext(model string, promptTokens int64) bool {
	th, ok := t.Threshold[model]
	return ok && promptTokens > th
}

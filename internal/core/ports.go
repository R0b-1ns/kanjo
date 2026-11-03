package core

import (
	"context"
	"time"
)

// Batch is what a UsageSource returns: rows, how each session's task was
// attributed, and how many source lines were skipped as unreadable.
type Batch struct {
	Rows     []UsageRow
	Sessions []SessionInfo
	Skipped  int
}

// UsageSource yields normalized usage rows (Claude Code transcripts, later
// agent exports).
type UsageSource interface {
	Read(ctx context.Context) (Batch, error)
}

// SnapshotLog is the record of one snapshot run.
type SnapshotLog struct {
	At                      time.Time
	Sessions, Rows, Skipped int
}

// Store persists usage rows and sessions.
type Store interface {
	// Merge saves b in one transaction. An incoming row replaces the stored
	// rows that share its source, session, day, model and long_context
	// (whatever their task), unless a stored one counts more messages: saved
	// usage never shrinks, because sources purge old data. Stored rows with no
	// incoming counterpart are kept. It also upserts b.Sessions and appends log.
	Merge(ctx context.Context, b Batch, log SnapshotLog) error
	Rows(ctx context.Context) ([]UsageRow, error)
	Sessions(ctx context.Context) ([]SessionInfo, error)
}

// Price is USD per million tokens.
type Price struct {
	Input, Output, CacheWrite5m, CacheWrite1h, CacheRead float64
}

// PriceBook gives the price of a model.
type PriceBook interface {
	// Lookup returns the price for the model and tier; ok is false when the
	// model has no price.
	Lookup(model string, longContext bool) (p Price, ok bool)
	// LongContext reports whether a prompt of promptTokens falls in the
	// model's long-context tier.
	LongContext(model string, promptTokens int64) bool
}

// Clock gives the current time.
type Clock interface {
	Now() time.Time
}

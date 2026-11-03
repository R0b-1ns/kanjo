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
	// Merge saves b in one transaction. The unit of replacement is the slot
	// (source, session, day, model, long_context), as a set of rows that may
	// carry several tasks: when the incoming rows of a slot count at least as
	// many messages in total as the stored ones, the stored rows of the slot
	// are replaced by the incoming ones; otherwise the stored rows stay and
	// the incoming ones are dropped, because sources purge old data and saved
	// usage never shrinks. Stored slots with no incoming rows are kept. It
	// also upserts b.Sessions and appends log.
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

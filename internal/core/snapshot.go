package core

import (
	"context"
	"fmt"
)

// Snapshot reads the source, validates everything it returned, and replaces
// the stored rows it supersedes; rows the source no longer has are kept. Nothing is written when any row or
// session is invalid. Errors name the index of the bad item, never its content.
func Snapshot(ctx context.Context, src UsageSource, st Store, clk Clock) (SnapshotLog, error) {
	b, err := src.Read(ctx)
	if err != nil {
		return SnapshotLog{}, fmt.Errorf("read usage: %w", err)
	}
	for i, r := range b.Rows {
		if err := r.Validate(); err != nil {
			return SnapshotLog{}, fmt.Errorf("source returned an invalid row (#%d): %w", i, err)
		}
	}
	for i, s := range b.Sessions {
		if err := s.Validate(); err != nil {
			return SnapshotLog{}, fmt.Errorf("source returned an invalid session (#%d): %w", i, err)
		}
	}
	log := SnapshotLog{At: clk.Now(), Sessions: len(b.Sessions), Rows: len(b.Rows), Skipped: b.Skipped}
	if err := st.Merge(ctx, b, log); err != nil {
		return SnapshotLog{}, fmt.Errorf("store snapshot: %w", err)
	}
	return log, nil
}

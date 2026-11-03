package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Candidate is a run that has no lead verdict, so its workspace may be left over.
type Candidate struct {
	Run      RunRecord
	Presence Presence
	// State is "running" (its lock is held), "crashed" (recorded as running,
	// lock free) or "finished" (ended, waiting for the lead).
	State string
}

// Candidates lists the recorded runs without a lead verdict. It only reads: it
// completes nothing and writes nothing. Branches the journal does not know are
// never listed.
func Candidates(ctx context.Context, d FinishDeps) ([]Candidate, error) {
	recs, err := d.Journal.Runs(ctx)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, r := range recs {
		if r.Lead != nil {
			continue
		}
		p, _ := d.Workspace.Inspect(ctx, ownedOf(r)) // a folder that is not ours reads as absent
		out = append(out, Candidate{Run: r, Presence: p, State: stateOf(r, p, d.Clock.Now())})
	}
	return out, nil
}

func stateOf(r RunRecord, p Presence, now time.Time) string {
	switch {
	case r.Verdict != RunRunning:
		return "finished"
	case p.Busy, now.Sub(r.StartedAt) < ReapGrace: // a young row may be about to take its lock
		return "running"
	}
	return "crashed"
}

// CleanResult is what Clean did with one candidate.
type CleanResult struct {
	Candidate Candidate
	Skipped   bool  // the run is alive: nothing was touched
	Err       error // the workspace was kept and nothing was recorded
}

// Clean abandons candidates: it completes a crashed run, removes its workspace
// and records the verdict abandoned. A run that is alive is skipped. One
// failure does not stop the others. A run that another process abandoned in
// the meantime counts as done.
func Clean(ctx context.Context, d FinishDeps, cs []Candidate) []CleanResult {
	out := make([]CleanResult, 0, len(cs))
	for _, c := range cs {
		res := CleanResult{Candidate: c}
		switch err := abandon(ctx, d, c.Run); {
		case errors.Is(err, ErrStillRunning):
			res.Skipped = true
		case err != nil:
			res.Err = err
		}
		out = append(out, res)
	}
	return out
}

func abandon(ctx context.Context, d FinishDeps, rec RunRecord) error {
	rec, err := Reap(ctx, d, rec)
	if err != nil {
		return err
	}
	if _, err := removeWorkspace(ctx, d, rec); err != nil {
		return err
	}
	_, err = d.Journal.Judge(ctx, Lead{At: d.Clock.Now(), Session: rec.Session, Verdict: Abandoned})
	if err != nil && !errors.Is(err, ErrJudged) {
		return fmt.Errorf("record the verdict: %w", err)
	}
	return nil
}

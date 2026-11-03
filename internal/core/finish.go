package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

var (
	// ErrStillRunning means the run holds its lock: it is alive.
	ErrStillRunning = errors.New("the run is still running")
	// ErrOtherVerdict means the run already has a different lead verdict.
	ErrOtherVerdict = errors.New("the run already has another lead verdict")
	// ErrUnknownRun means the journal has no run with that session.
	ErrUnknownRun = errors.New("the journal has no run with that session")
)

// FinishDeps are the ports finish, clean and reap need.
type FinishDeps struct {
	Journal   RunJournal
	Workspace Workspace
	Clock     Clock
}

// ownedOf names what the journal recorded about a run. A run that ended
// without a known tip is held to the base commit: a branch that moved from
// there is not removed.
func ownedOf(rec RunRecord) Owned {
	tip := rec.Tip
	if tip == "" {
		tip = rec.Base
	}
	return Owned{Session: rec.Session, Branch: BranchOf(rec.Session), Dir: DirOf(rec.Session), Tip: tip}
}

// Reap completes a run that is recorded as running but whose lock is free (its
// process died) as interrupted. Any other run is returned unchanged.
func Reap(ctx context.Context, d FinishDeps, rec RunRecord) (RunRecord, error) {
	if rec.Verdict != RunRunning {
		return rec, nil
	}
	if p, err := d.Workspace.Inspect(ctx, ownedOf(rec)); err == nil && p.Busy {
		return rec, nil
	}
	res := RunResult{Verdict: RunInterrupted, Exit: RunInterrupted.ExitCode(), FinishedAt: d.Clock.Now()}
	switch err := d.Journal.Finish(ctx, rec.Session, res); {
	case errors.Is(err, ErrNotRunning):
		return reload(ctx, d, rec.Session) // somebody completed it first
	case err != nil:
		return rec, fmt.Errorf("complete the crashed run: %w", err)
	}
	rec.RunResult = res
	return rec, nil
}

// reload reads one run again.
func reload(ctx context.Context, d FinishDeps, session string) (RunRecord, error) {
	recs, err := d.Journal.Runs(ctx)
	if err != nil {
		return RunRecord{}, err
	}
	if i := slices.IndexFunc(recs, func(r RunRecord) bool { return r.Session == session }); i >= 0 {
		return recs[i], nil
	}
	return RunRecord{}, ErrUnknownRun
}

// FinishOutcome says what Finish did.
type FinishOutcome struct {
	Run             RunRecord
	AlreadyRecorded bool // the run already had this verdict: nothing was done
	WorkspaceGone   bool // there was no workspace left to remove
}

// Finish records the lead's verdict on a run and removes its worktree and
// branch. The workspace goes first, then the verdict is written, so a crash
// between the two is repaired by running Finish again. It only touches what
// the journal recorded.
func Finish(ctx context.Context, d FinishDeps, session string, v LeadVerdict) (FinishOutcome, error) {
	if err := checkFinishArgs(session, v); err != nil {
		return FinishOutcome{}, err
	}
	rec, err := reload(ctx, d, session)
	if err != nil {
		return FinishOutcome{}, err
	}
	if rec.Lead != nil {
		return alreadyJudged(rec, v)
	}
	if rec, err = Reap(ctx, d, rec); err != nil {
		return FinishOutcome{}, err
	}
	gone, err := removeWorkspace(ctx, d, rec)
	if err != nil {
		return FinishOutcome{}, err
	}
	out, err := recordVerdict(ctx, d, rec, v)
	out.WorkspaceGone = gone && !out.AlreadyRecorded
	return out, err
}

func checkFinishArgs(session string, v LeadVerdict) error {
	switch {
	case !ValidSession(session):
		return errors.New("invalid session id")
	case v != Accepted && v != Modified && v != Rejected:
		return errors.New("the verdict must be accepted, modified or rejected")
	}
	return nil
}

// recordVerdict writes the lead verdict. A verdict another process wrote in
// the meantime is judged like one found at the start.
func recordVerdict(ctx context.Context, d FinishDeps, rec RunRecord, v LeadVerdict) (FinishOutcome, error) {
	lead := Lead{At: d.Clock.Now(), Session: rec.Session, Verdict: v}
	switch existing, err := d.Journal.Judge(ctx, lead); {
	case errors.Is(err, ErrJudged):
		rec.Lead = &Lead{Session: rec.Session, Verdict: existing}
		return alreadyJudged(rec, v)
	case err != nil:
		return FinishOutcome{}, fmt.Errorf("record the verdict: %w", err)
	}
	rec.Lead = &lead
	return FinishOutcome{Run: rec}, nil
}

func alreadyJudged(rec RunRecord, v LeadVerdict) (FinishOutcome, error) {
	if rec.Lead.Verdict != v {
		return FinishOutcome{}, fmt.Errorf("%w (%s)", ErrOtherVerdict, rec.Lead.Verdict)
	}
	return FinishOutcome{Run: rec, AlreadyRecorded: true}, nil
}

// removeWorkspace removes the worktree of a run that created one and reports
// whether there was none left. A run that is alive is ErrStillRunning.
func removeWorkspace(ctx context.Context, d FinishDeps, rec RunRecord) (gone bool, err error) {
	if rec.Verdict == RunRunning {
		return false, ErrStillRunning
	}
	if !rec.Created {
		return true, nil
	}
	o := ownedOf(rec)
	p, err := d.Workspace.Inspect(ctx, o)
	if err != nil {
		return false, fmt.Errorf("inspect the workspace: %w", err)
	}
	if err := d.Workspace.Remove(ctx, o); err != nil {
		if errors.Is(err, ErrBusy) {
			return false, ErrStillRunning
		}
		return false, fmt.Errorf("remove the workspace: %w", err)
	}
	return !p.DirExists && !p.BranchExists, nil
}

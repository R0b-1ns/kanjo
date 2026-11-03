package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

const fakeBase = "1111111111111111111111111111111111111111"

func (r *rig) finishDeps() core.FinishDeps {
	return core.FinishDeps{Journal: r.j, Workspace: r.ws, Clock: kanjotest.FixedClock{T: testNow.Add(time.Hour)}}
}

func (r *rig) finish(v core.LeadVerdict) (core.FinishOutcome, error) {
	return core.Finish(context.Background(), r.finishDeps(), testSession, v)
}

// finished runs a task that succeeds, leaving its worktree for the lead.
func finished(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, writeA)
	r.run(r.req())
	return r
}

// crashed leaves a row running whose process is gone: the worktree exists, the lock is free.
func crashed(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	ctx := context.Background()
	run := core.Run{Session: testSession, Task: "ABC-12", Adapter: "script", Model: "m", StartedAt: testNow, Base: fakeBase}
	if err := r.j.Begin(ctx, run); err != nil {
		t.Fatal(err)
	}
	w, err := r.ws.Create(ctx, testSession, fakeBase)
	if err != nil {
		t.Fatal(err)
	}
	r.j.Created(ctx, testSession)
	r.ws.Release(w)
	return r
}

func TestFinishRemovesThenRecords(t *testing.T) {
	r := finished(t)
	out, err := r.finish(core.Accepted)
	if err != nil || out.AlreadyRecorded || out.WorkspaceGone {
		t.Fatalf("out %+v err %v", out, err)
	}
	rec := r.record()
	if rec.Lead == nil || rec.Lead.Verdict != core.Accepted || !rec.Lead.At.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("record = %+v", rec)
	}
	if r.ws.Removals() != 1 || len(r.ws.Branches()) != 0 {
		t.Fatal("the workspace was not removed")
	}
	if out.Run.Lead == nil || out.Run.Session != testSession {
		t.Fatalf("outcome run = %+v", out.Run)
	}
}

func TestFinishTwiceWithTheSameVerdictIsANoOp(t *testing.T) {
	r := finished(t)
	r.finish(core.Modified)
	before := r.record()
	out, err := r.finish(core.Modified)
	if err != nil || !out.AlreadyRecorded {
		t.Fatalf("out %+v err %v", out, err)
	}
	if after := r.record(); after.Lead.At != before.Lead.At || r.ws.Removals() != 1 {
		t.Fatal("the second call changed something")
	}
}

func TestFinishWithAnotherVerdictIsRefusedAndChangesNothing(t *testing.T) {
	r := finished(t)
	r.finish(core.Accepted)
	if _, err := r.finish(core.Rejected); !errors.Is(err, core.ErrOtherVerdict) {
		t.Fatalf("err = %v", err)
	}
	if rec := r.record(); rec.Lead.Verdict != core.Accepted {
		t.Fatalf("record = %+v", rec)
	}
}

func TestFinishOfARunningRunIsRefused(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.j.Begin(ctx, core.Run{Session: testSession, Task: "ABC-12", Adapter: "script", Model: "m", StartedAt: testNow, Base: fakeBase})
	r.ws.Create(ctx, testSession, fakeBase) // the lock stays held: the run is alive
	r.j.Created(ctx, testSession)
	if _, err := r.finish(core.Accepted); !errors.Is(err, core.ErrStillRunning) {
		t.Fatalf("err = %v", err)
	}
	rec := r.record()
	if rec.Verdict != core.RunRunning || rec.Lead != nil || r.ws.Removals() != 0 {
		t.Fatalf("record = %+v", rec)
	}
}

func TestFinishReapsACrashedRun(t *testing.T) {
	r := crashed(t)
	if _, err := r.finish(core.Rejected); err != nil {
		t.Fatal(err)
	}
	rec := r.record()
	if rec.Verdict != core.RunInterrupted || rec.Exit != 1 || rec.Lead == nil || rec.Lead.Verdict != core.Rejected || !rec.FinishedAt.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("record = %+v", rec)
	}
	if r.ws.Removals() != 1 {
		t.Fatal("the crashed run's worktree was not removed")
	}
}

func TestFinishWhenTheWorkspaceIsAlreadyGone(t *testing.T) {
	r := finished(t)
	rec := r.record()
	r.ws.Release(core.Workdir{Session: testSession})
	if err := r.ws.Remove(context.Background(), core.Owned{Session: testSession, Branch: core.BranchOf(testSession), Dir: core.DirOf(testSession), Tip: rec.Tip}); err != nil {
		t.Fatal(err)
	}
	out, err := r.finish(core.Accepted)
	if err != nil || !out.WorkspaceGone || r.record().Lead == nil {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestFinishAfterACrashBetweenRemoveAndJudgeCompletes(t *testing.T) {
	r := finished(t)
	// the first finish removed the worktree and died before recording: same state as above
	rec := r.record()
	r.ws.Remove(context.Background(), core.Owned{Session: testSession, Branch: core.BranchOf(testSession), Dir: core.DirOf(testSession), Tip: rec.Tip})
	if out, err := r.finish(core.Accepted); err != nil || !out.WorkspaceGone {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestFinishWithAMovedTipDeletesAndRecordsNothing(t *testing.T) {
	r := finished(t)
	r.ws.MoveTip(testSession)
	if _, err := r.finish(core.Accepted); !errors.Is(err, core.ErrMoved) {
		t.Fatalf("err = %v", err)
	}
	if r.record().Lead != nil || r.ws.Removals() != 0 || len(r.ws.Branches()) != 1 {
		t.Fatal("something changed")
	}
}

func TestFinishNeverRemovesWhatTheJournalDoesNotName(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	w, _ := r.ws.Create(ctx, testSession, fakeBase) // a worktree the journal never heard of
	r.ws.Release(w)
	if _, err := r.finish(core.Accepted); !errors.Is(err, core.ErrUnknownRun) {
		t.Fatalf("err = %v", err)
	}
	if r.ws.Removals() != 0 || len(r.ws.Branches()) != 1 {
		t.Fatal("an unrecorded worktree was removed")
	}
}

func TestFinishRemoveOnlyWhenCreated(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.j.Begin(ctx, core.Run{Session: testSession, Task: "ABC-12", Adapter: "script", Model: "m", StartedAt: testNow, Base: fakeBase})
	r.j.Finish(ctx, testSession, core.RunResult{Verdict: core.RunFailed, Exit: 1, FinishedAt: testNow})
	w, _ := r.ws.Create(ctx, testSession, fakeBase) // somebody else's, at the derived name
	r.ws.Release(w)
	out, err := r.finish(core.Rejected)
	if err != nil || !out.WorkspaceGone || r.ws.Removals() != 0 || len(r.ws.Branches()) != 1 {
		t.Fatalf("out %+v err %v removals %d", out, err, r.ws.Removals())
	}
}

func TestFinishRefusesAbandonedAndUnknownVerdicts(t *testing.T) {
	r := finished(t)
	for _, v := range []core.LeadVerdict{core.Abandoned, "maybe", ""} {
		if _, err := r.finish(v); err == nil || errors.Is(err, core.ErrUnknownRun) {
			t.Errorf("verdict %q: err = %v", v, err)
		}
	}
	if r.record().Lead != nil || r.ws.Removals() != 0 {
		t.Fatal("something changed")
	}
}

func TestFinishRefusesABadSessionBeforeReadingAnything(t *testing.T) {
	r := finished(t)
	if _, err := core.Finish(context.Background(), r.finishDeps(), "../x", core.Accepted); err == nil || errors.Is(err, core.ErrUnknownRun) {
		t.Fatalf("err = %v", err)
	}
}

func TestFinishAfterCleanAbandonedKeepsTheAbandonedVerdict(t *testing.T) {
	r := finished(t)
	r.j.Judge(context.Background(), core.Lead{At: testNow, Session: testSession, Verdict: core.Abandoned})
	if _, err := r.finish(core.Accepted); !errors.Is(err, core.ErrOtherVerdict) {
		t.Fatalf("err = %v", err)
	}
}

func TestReapLeavesFinishedRunsAlone(t *testing.T) {
	r := finished(t)
	rec := r.record()
	got, err := core.Reap(context.Background(), r.finishDeps(), rec)
	if err != nil || got.Verdict != rec.Verdict || got.FinishedAt != rec.FinishedAt {
		t.Fatalf("got %+v err %v", got, err)
	}
}

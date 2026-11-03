package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

// readOnlyJournal fails every write.
type readOnlyJournal struct{ core.RunJournal }

var errWrite = errors.New("write attempted")

func (readOnlyJournal) Begin(context.Context, core.Run) error                { return errWrite }
func (readOnlyJournal) Created(context.Context, string) error                { return errWrite }
func (readOnlyJournal) Finish(context.Context, string, core.RunResult) error { return errWrite }
func (readOnlyJournal) Judge(context.Context, core.Lead) (core.LeadVerdict, error) {
	return "", errWrite
}

func candidates(t *testing.T, r *rig) []core.Candidate {
	t.Helper()
	cs, err := core.Candidates(context.Background(), r.finishDeps())
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestCandidatesAreRunsWithoutALead(t *testing.T) {
	r := finished(t)
	cs := candidates(t, r)
	if len(cs) != 1 || cs[0].State != "finished" || !cs[0].Presence.DirExists || cs[0].Run.Session != testSession {
		t.Fatalf("candidates = %+v", cs)
	}
	r.finish(core.Accepted)
	if cs := candidates(t, r); len(cs) != 0 {
		t.Fatalf("a judged run is a candidate: %+v", cs)
	}
}

func TestCandidateStates(t *testing.T) {
	if cs := candidates(t, crashed(t)); len(cs) != 1 || cs[0].State != "crashed" {
		t.Fatalf("crashed: %+v", cs)
	}
	r := newRig(t)
	ctx := context.Background()
	r.j.Begin(ctx, core.Run{Session: testSession, Task: "-", Adapter: "script", Model: "m", StartedAt: testNow, Base: fakeBase})
	r.ws.Create(ctx, testSession, fakeBase)
	r.j.Created(ctx, testSession)
	if cs := candidates(t, r); len(cs) != 1 || cs[0].State != "running" || !cs[0].Presence.Busy {
		t.Fatalf("running: %+v", cs)
	}
}

func TestCandidatesDoNotWrite(t *testing.T) {
	r := crashed(t)
	d := r.finishDeps()
	d.Journal = readOnlyJournal{r.j}
	cs, err := core.Candidates(context.Background(), d)
	if err != nil || len(cs) != 1 {
		t.Fatalf("cs %+v err %v", cs, err)
	}
	if r.record().Verdict != core.RunRunning {
		t.Fatal("Candidates reaped a run")
	}
}

func TestCandidatesIgnoreBranchesTheJournalDoesNotKnow(t *testing.T) {
	r := finished(t)
	w, _ := r.ws.Create(context.Background(), "ses_foreign", fakeBase)
	r.ws.Release(w)
	cs := candidates(t, r)
	if len(cs) != 1 || cs[0].Run.Session != testSession {
		t.Fatalf("candidates = %+v", cs)
	}
	core.Clean(context.Background(), r.finishDeps(), cs)
	if got := r.ws.Branches(); len(got) != 1 || got[0] != "delegate/ses_foreign" {
		t.Fatalf("branches = %v", got)
	}
}

func TestCleanSkipsUnrecordedBranches(t *testing.T) {
	r := newRig(t)
	w, _ := r.ws.Create(context.Background(), testSession, fakeBase)
	r.ws.Release(w)
	if res := core.Clean(context.Background(), r.finishDeps(), candidates(t, r)); len(res) != 0 || r.ws.Removals() != 0 {
		t.Fatalf("res %+v", res)
	}
}

func TestCleanNeverTouchesALockedRun(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.j.Begin(ctx, core.Run{Session: testSession, Task: "-", Adapter: "script", Model: "m", StartedAt: testNow, Base: fakeBase})
	r.ws.Create(ctx, testSession, fakeBase)
	r.j.Created(ctx, testSession)
	res := core.Clean(ctx, r.finishDeps(), candidates(t, r))
	if len(res) != 1 || !res[0].Skipped || res[0].Err != nil || r.ws.Removals() != 0 || r.record().Lead != nil || r.record().Verdict != core.RunRunning {
		t.Fatalf("res %+v", res)
	}
}

func TestCleanRecordsAbandoned(t *testing.T) {
	r := finished(t)
	res := core.Clean(context.Background(), r.finishDeps(), candidates(t, r))
	rec := r.record()
	if len(res) != 1 || res[0].Err != nil || res[0].Skipped || rec.Lead == nil || rec.Lead.Verdict != core.Abandoned || r.ws.Removals() != 1 {
		t.Fatalf("res %+v rec %+v", res, rec)
	}
}

func TestCleanReapsACrashedRunFirst(t *testing.T) {
	r := crashed(t)
	res := core.Clean(context.Background(), r.finishDeps(), candidates(t, r))
	rec := r.record()
	if len(res) != 1 || res[0].Err != nil || rec.Verdict != core.RunInterrupted || rec.Lead.Verdict != core.Abandoned || r.ws.Removals() != 1 {
		t.Fatalf("res %+v rec %+v", res, rec)
	}
}

func TestCleanContinuesAfterOneFailure(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for i, s := range []string{"ses_a", "ses_b"} {
		run := kanjotest.SampleRun(i + 1)
		run.Session, run.Base = s, fakeBase
		r.j.Begin(ctx, run)
		w, _ := r.ws.Create(ctx, s, fakeBase)
		r.j.Created(ctx, s)
		r.ws.Release(w)
		res := kanjotest.SampleResult(core.RunOK)
		res.Tip = fakeBase
		r.j.Finish(ctx, s, res)
	}
	r.ws.MoveTip("ses_a") // the first cannot be removed
	res := core.Clean(ctx, r.finishDeps(), candidates(t, r))
	if len(res) != 2 || !errors.Is(res[0].Err, core.ErrMoved) || res[1].Err != nil || r.ws.Removals() != 1 {
		t.Fatalf("res %+v", res)
	}
}

func TestCleanTwiceInParallelRecordsOnce(t *testing.T) {
	r := finished(t)
	cs := candidates(t, r)
	var wg sync.WaitGroup
	results := make([][]core.CleanResult, 2)
	for i := range results {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = core.Clean(context.Background(), r.finishDeps(), cs) }()
	}
	wg.Wait()
	for _, res := range results {
		if len(res) != 1 || res[0].Err != nil {
			t.Fatalf("res %+v", res)
		}
	}
	if r.record().Lead.Verdict != core.Abandoned {
		t.Fatal("no abandoned verdict")
	}
}

func TestCleanLeavesRunsWithALead(t *testing.T) {
	r := finished(t)
	r.finish(core.Accepted)
	if res := core.Clean(context.Background(), r.finishDeps(), candidates(t, r)); len(res) != 0 || r.record().Lead.Verdict != core.Accepted {
		t.Fatalf("res %+v", res)
	}
}

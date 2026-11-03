package kanjotest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// MemRunJournal is an in-memory core.RunJournal.
type MemRunJournal struct {
	mu   sync.Mutex
	recs []core.RunRecord
}

// NewMemRunJournal returns an empty journal.
func NewMemRunJournal() *MemRunJournal { return &MemRunJournal{} }

func (j *MemRunJournal) find(session string) int {
	return slices.IndexFunc(j.recs, func(r core.RunRecord) bool { return r.Session == session })
}

// Begin implements core.RunJournal.
func (j *MemRunJournal) Begin(_ context.Context, r core.Run) error {
	if err := r.Validate(); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.find(r.Session) >= 0 {
		return core.ErrDuplicate
	}
	j.recs = append(j.recs, core.RunRecord{Run: r, RunResult: core.RunResult{Verdict: core.RunRunning}})
	return nil
}

// Created implements core.RunJournal.
func (j *MemRunJournal) Created(_ context.Context, session string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i := j.find(session)
	if i < 0 {
		return core.ErrNoRun
	}
	j.recs[i].Created = true
	return nil
}

// Finish implements core.RunJournal.
func (j *MemRunJournal) Finish(_ context.Context, session string, r core.RunResult) error {
	if err := r.ValidateFinal(); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	i := j.find(session)
	switch {
	case i < 0:
		return core.ErrNoRun
	case j.recs[i].Verdict != core.RunRunning:
		return core.ErrNotRunning
	}
	j.recs[i].RunResult = r
	return nil
}

// Judge implements core.RunJournal.
func (j *MemRunJournal) Judge(_ context.Context, l core.Lead) (core.LeadVerdict, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	i := j.find(l.Session)
	switch {
	case i < 0:
		return "", core.ErrNoRun
	case j.recs[i].Lead != nil:
		return j.recs[i].Lead.Verdict, core.ErrJudged
	}
	j.recs[i].Lead = &l
	return "", nil
}

// Runs implements core.RunJournal; runs come back in start order.
func (j *MemRunJournal) Runs(context.Context) ([]core.RunRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]core.RunRecord, len(j.recs))
	for i, r := range j.recs {
		if r.Lead != nil {
			l := *r.Lead
			r.Lead = &l
		}
		out[i] = r
	}
	slices.SortStableFunc(out, func(a, b core.RunRecord) int { return a.StartedAt.Compare(b.StartedAt) })
	return out, nil
}

var rjBase = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

const rjCommit = "0123456789abcdef0123456789abcdef01234567"

// SampleRun is a valid run for contract tests; n makes the session and the
// start time distinct.
func SampleRun(n int) core.Run {
	return core.Run{
		Session: fmt.Sprintf("ses_20261103T1200%02d_a1b2c3", n), Task: "ABC-12", Adapter: "script",
		Model: "test-model", StartedAt: rjBase.Add(time.Duration(n) * time.Second), Base: rjCommit,
	}
}

// SampleResult is a valid final result for contract tests.
func SampleResult(v core.RunVerdict) core.RunResult {
	return core.RunResult{Verdict: v, Exit: v.ExitCode(), FinishedAt: rjBase.Add(time.Hour), Launches: 1, Duration: time.Second, Tip: rjCommit}
}

func mustBegin(t *testing.T, j core.RunJournal, n int) core.Run {
	t.Helper()
	r := SampleRun(n)
	if err := j.Begin(context.Background(), r); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return r
}

func runsOf(t *testing.T, j core.RunJournal) []core.RunRecord {
	t.Helper()
	recs, err := j.Runs(context.Background())
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	return recs
}

var runJournalCases = []contractCase[core.RunJournal]{
	{"begin records a running run", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		recs := runsOf(t, j)
		if len(recs) != 1 || recs[0].Run != r || recs[0].Verdict != core.RunRunning || recs[0].Created || recs[0].Lead != nil {
			t.Fatalf("runs = %+v", recs)
		}
	}},
	{"begin twice is ErrDuplicate and keeps the first", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		again := r
		again.Model = "other-model"
		if err := j.Begin(context.Background(), again); !errors.Is(err, core.ErrDuplicate) {
			t.Fatalf("err = %v", err)
		}
		if recs := runsOf(t, j); len(recs) != 1 || recs[0].Model != "test-model" {
			t.Fatalf("runs = %+v", recs)
		}
	}},
	{"created marks the run", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		if err := j.Created(context.Background(), r.Session); err != nil {
			t.Fatal(err)
		}
		if recs := runsOf(t, j); !recs[0].Created {
			t.Fatalf("runs = %+v", recs)
		}
	}},
	{"created of an unknown run is ErrNoRun", func(t *testing.T, j core.RunJournal) {
		if err := j.Created(context.Background(), "ses_nope"); !errors.Is(err, core.ErrNoRun) {
			t.Fatalf("err = %v", err)
		}
	}},
	{"finish completes a running run once", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		res := SampleResult(core.RunOK)
		if err := j.Finish(context.Background(), r.Session, res); err != nil {
			t.Fatal(err)
		}
		recs := runsOf(t, j)
		if recs[0].RunResult != res {
			t.Fatalf("result = %+v, want %+v", recs[0].RunResult, res)
		}
	}},
	{"finish of a final run is ErrNotRunning and changes nothing", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		first := SampleResult(core.RunOK)
		j.Finish(context.Background(), r.Session, first)
		if err := j.Finish(context.Background(), r.Session, SampleResult(core.RunFailed)); !errors.Is(err, core.ErrNotRunning) {
			t.Fatalf("err = %v", err)
		}
		if recs := runsOf(t, j); recs[0].RunResult != first {
			t.Fatalf("result = %+v", recs[0].RunResult)
		}
	}},
	{"finish of an unknown run is ErrNoRun", func(t *testing.T, j core.RunJournal) {
		if err := j.Finish(context.Background(), "ses_nope", SampleResult(core.RunOK)); !errors.Is(err, core.ErrNoRun) {
			t.Fatalf("err = %v", err)
		}
	}},
	{"finish refuses an invalid result", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		bad := SampleResult(core.RunOK)
		bad.Retries = 2
		for _, res := range []core.RunResult{bad, {Verdict: core.RunRunning}} {
			if err := j.Finish(context.Background(), r.Session, res); err == nil {
				t.Fatalf("result %+v accepted", res)
			}
		}
		if recs := runsOf(t, j); recs[0].Verdict != core.RunRunning {
			t.Fatalf("runs = %+v", recs)
		}
	}},
	{"judge records one verdict", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		l := core.Lead{At: rjBase.Add(2 * time.Hour), Session: r.Session, Verdict: core.Accepted}
		if _, err := j.Judge(context.Background(), l); err != nil {
			t.Fatal(err)
		}
		recs := runsOf(t, j)
		if recs[0].Lead == nil || *recs[0].Lead != l {
			t.Fatalf("runs = %+v", recs)
		}
	}},
	{"judge twice returns ErrJudged with the existing verdict", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		j.Judge(context.Background(), core.Lead{At: rjBase, Session: r.Session, Verdict: core.Accepted})
		got, err := j.Judge(context.Background(), core.Lead{At: rjBase, Session: r.Session, Verdict: core.Rejected})
		if !errors.Is(err, core.ErrJudged) || got != core.Accepted {
			t.Fatalf("got %q, %v", got, err)
		}
		if recs := runsOf(t, j); recs[0].Lead.Verdict != core.Accepted {
			t.Fatalf("runs = %+v", recs)
		}
	}},
	{"judge of an unknown run is ErrNoRun", func(t *testing.T, j core.RunJournal) {
		_, err := j.Judge(context.Background(), core.Lead{At: rjBase, Session: "ses_nope", Verdict: core.Accepted})
		if !errors.Is(err, core.ErrNoRun) {
			t.Fatalf("err = %v", err)
		}
	}},
	{"concurrent judges keep exactly one", func(t *testing.T, j core.RunJournal) {
		r := mustBegin(t, j, 1)
		verdicts := []core.LeadVerdict{core.Accepted, core.Modified, core.Rejected, core.Abandoned}
		const n = 16
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := j.Judge(context.Background(), core.Lead{At: rjBase, Session: r.Session, Verdict: verdicts[i%len(verdicts)]})
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					wins++
				case !errors.Is(err, core.ErrJudged):
					t.Errorf("Judge: %v", err)
				}
			}()
		}
		wg.Wait()
		if recs := runsOf(t, j); wins != 1 || recs[0].Lead == nil {
			t.Fatalf("wins = %d, runs = %+v", wins, recs)
		}
	}},
	{"runs come back in start order with their lead", func(t *testing.T, j core.RunJournal) {
		late := mustBegin(t, j, 5)
		early := mustBegin(t, j, 2)
		j.Judge(context.Background(), core.Lead{At: rjBase, Session: early.Session, Verdict: core.Modified})
		recs := runsOf(t, j)
		if len(recs) != 2 || recs[0].Session != early.Session || recs[1].Session != late.Session ||
			recs[0].Lead == nil || recs[0].Lead.Verdict != core.Modified || recs[1].Lead != nil {
			t.Fatalf("runs = %+v", recs)
		}
	}},
	{"an invalid record is refused on write", func(t *testing.T, j core.RunJournal) {
		bad := SampleRun(1)
		bad.Task = "../x"
		if err := j.Begin(context.Background(), bad); err == nil {
			t.Fatal("invalid run accepted")
		}
		r := mustBegin(t, j, 2)
		if _, err := j.Judge(context.Background(), core.Lead{At: rjBase, Session: r.Session, Verdict: "maybe"}); err == nil {
			t.Fatal("invalid lead accepted")
		}
		if recs := runsOf(t, j); len(recs) != 1 || recs[0].Lead != nil {
			t.Fatalf("runs = %+v", recs)
		}
	}},
}

// RunJournalContract checks the behavior every core.RunJournal must have. open
// must return a fresh, empty journal.
func RunJournalContract(t *testing.T, open func(t *testing.T) core.RunJournal) {
	runCases(t, runJournalCases, open)
}

package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

const testSession = "ses_20261103T120000_a1b2c3"

var testNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// rig is a Delegate wired to the fakes.
type rig struct {
	t     *testing.T
	j     *kanjotest.MemRunJournal
	ws    *kanjotest.FakeWorkspace
	sb    *kanjotest.FakeSandbox
	art   *kanjotest.MemArtifacts
	st    *kanjotest.MemStore
	agent *kanjotest.ScriptedAgent
}

func newRig(t *testing.T, launches ...[]kanjotest.AgentStep) *rig {
	t.Helper()
	r := &rig{t: t, j: kanjotest.NewMemRunJournal(), ws: kanjotest.NewFakeWorkspace(), sb: kanjotest.NewFakeSandbox(),
		art: kanjotest.NewMemArtifacts(), st: kanjotest.NewMemStore()}
	r.agent = kanjotest.NewScriptedAgent(r.ws, launches...)
	return r
}

func (r *rig) deps() core.DelegateDeps {
	return core.DelegateDeps{
		Journal: r.j, Workspace: r.ws, Sandbox: r.sb, Agent: r.agent, Artifacts: r.art, Store: r.st,
		Clock:      kanjotest.FixedClock{T: testNow},
		NewSession: func(time.Time) string { return testSession },
	}
}

func (r *rig) req() core.DelegateRequest {
	return core.DelegateRequest{
		Task: "ABC-12", Adapter: "script", Model: "test-model", Prompt: "write the file",
		Expect:  []string{"src/a.go"},
		Timeout: 5 * time.Second, CommandTimeout: 5 * time.Second, Grace: 50 * time.Millisecond, MaxPatchBytes: 1 << 20,
	}
}

func (r *rig) run(req core.DelegateRequest) core.DelegateOutcome {
	r.t.Helper()
	out, err := core.Delegate(context.Background(), r.deps(), req)
	if err != nil {
		r.t.Fatalf("Delegate: %v", err)
	}
	return out
}

func (r *rig) record() core.RunRecord {
	r.t.Helper()
	recs, err := r.j.Runs(context.Background())
	if err != nil || len(recs) != 1 {
		r.t.Fatalf("runs = %+v, err %v", recs, err)
	}
	return recs[0]
}

var writeA = []kanjotest.AgentStep{kanjotest.Write("src/a.go", "package a")}

func TestDelegateHappyPathOnFakes(t *testing.T) {
	r := newRig(t, writeA)
	out := r.run(r.req())
	rec := r.record()
	if out.Session != testSession || out.Result.Verdict != core.RunOK || rec.Verdict != core.RunOK || rec.Exit != 0 {
		t.Fatalf("outcome %+v record %+v", out, rec)
	}
	if !rec.Created || rec.Launches != 1 || rec.Retries != 0 || rec.Tip == "" || !core.ValidCommitID(rec.Base) || rec.Task != "ABC-12" {
		t.Fatalf("record = %+v", rec)
	}
	if r.ws.Holding(testSession) {
		t.Error("the run lock is still held")
	}
	if got := len(r.sb.Specs()); got != 1 {
		t.Errorf("boxes opened = %d, want 1", got)
	}
}

func TestDelegateWritesTheRowBeforeCreatingTheWorkspace(t *testing.T) {
	r := newRig(t, writeA)
	seen := false
	r.ws.OnCreate = func(session string) {
		recs, _ := r.j.Runs(context.Background())
		seen = len(recs) == 1 && recs[0].Session == session && recs[0].Verdict == core.RunRunning && !recs[0].Created
	}
	r.run(r.req())
	if !seen {
		t.Fatal("no running row when Create ran")
	}
}

func TestDelegateCompletesTheRowWhenCreateFails(t *testing.T) {
	r := newRig(t, writeA)
	commit, _ := r.ws.Resolve(context.Background(), "HEAD")
	other, err := r.ws.Create(context.Background(), testSession, commit) // a stranger already holds the name
	if err != nil {
		t.Fatal(err)
	}
	out := r.run(r.req())
	rec := r.record()
	if out.Result.Verdict != core.RunFailed || rec.Verdict != core.RunFailed || rec.Created || rec.Launches != 0 {
		t.Fatalf("outcome %+v record %+v", out, rec)
	}
	if r.ws.Removals() != 0 || len(r.ws.Branches()) != 1 {
		t.Error("something that was not ours was touched")
	}
	if !r.ws.Holding(other.Session) {
		t.Error("the stranger's lock was released")
	}
	if len(r.sb.Specs()) != 0 || r.agent.Launches() != 0 {
		t.Error("a box or an agent was started without a workspace")
	}
}

func TestDelegateReleasesTheLockOnEveryPath(t *testing.T) {
	cases := map[string][]kanjotest.AgentStep{
		"success":  writeA,
		"failure":  {kanjotest.Fail(2)},
		"no-write": {},
	}
	for name, steps := range cases {
		r := newRig(t, steps)
		r.run(r.req())
		if r.ws.Holding(testSession) {
			t.Errorf("%s: the lock is still held", name)
		}
	}
}

func TestDelegateRejectsInvalidInputBeforeAnyRow(t *testing.T) {
	bad := map[string]func(*core.DelegateRequest){
		"label":    func(q *core.DelegateRequest) { q.Task = "../x" },
		"expect":   func(q *core.DelegateRequest) { q.Expect = []string{"../x"} },
		"forbid":   func(q *core.DelegateRequest) { q.Forbid = []string{"a\nb"} },
		"model":    func(q *core.DelegateRequest) { q.Model = "" },
		"adapter":  func(q *core.DelegateRequest) { q.Adapter = "Bad Name" },
		"ref":      func(q *core.DelegateRequest) { q.Ref = "-x" },
		"prompt":   func(q *core.DelegateRequest) { q.Prompt = "" },
		"timeout":  func(q *core.DelegateRequest) { q.Timeout = 0 },
		"patchcap": func(q *core.DelegateRequest) { q.MaxPatchBytes = 0 },
		"setup":    func(q *core.DelegateRequest) { q.Setup = "a\x00b" },
		"check":    func(q *core.DelegateRequest) { q.Checks = []string{""} },
		"test":     func(q *core.DelegateRequest) { q.Test = string(make([]byte, 4097)) },
	}
	for name, mut := range bad {
		r := newRig(t, writeA)
		req := r.req()
		mut(&req)
		if _, err := core.Delegate(context.Background(), r.deps(), req); err == nil {
			t.Errorf("%s: no error", name)
		}
		if recs, _ := r.j.Runs(context.Background()); len(recs) != 0 || len(r.ws.Branches()) != 0 {
			t.Errorf("%s: something was written (%d rows)", name, len(recs))
		}
	}
}

func TestDelegateNoCommitWritesNothing(t *testing.T) {
	r := newRig(t, writeA)
	r.ws.HasCommit = false
	if _, err := core.Delegate(context.Background(), r.deps(), r.req()); !errors.Is(err, core.ErrNoCommit) {
		t.Fatalf("err = %v", err)
	}
	if recs, _ := r.j.Runs(context.Background()); len(recs) != 0 {
		t.Fatal("a row was written")
	}
}

func TestDelegateBadRefWritesNothing(t *testing.T) {
	r := newRig(t, writeA)
	req := r.req()
	req.Ref = "no-such-ref"
	if _, err := core.Delegate(context.Background(), r.deps(), req); !errors.Is(err, core.ErrBadRef) {
		t.Fatalf("err = %v", err)
	}
	if recs, _ := r.j.Runs(context.Background()); len(recs) != 0 {
		t.Fatal("a row was written")
	}
}

func TestDelegateUsesTheJournalClock(t *testing.T) {
	r := newRig(t, writeA)
	r.run(r.req())
	rec := r.record()
	if !rec.StartedAt.Equal(testNow) || !rec.FinishedAt.Equal(testNow) {
		t.Fatalf("started %v finished %v, want %v", rec.StartedAt, rec.FinishedAt, testNow)
	}
}

// failingFinish is a journal that cannot complete a run.
type failingFinish struct{ *kanjotest.MemRunJournal }

func (failingFinish) Finish(context.Context, string, core.RunResult) error {
	return errors.New("disk full")
}

func TestDelegateWarnsWhenTheRowCannotBeCompleted(t *testing.T) {
	r := newRig(t, writeA)
	deps := r.deps()
	deps.Journal = failingFinish{r.j}
	out, err := core.Delegate(context.Background(), deps, r.req())
	if err != nil || len(out.Warnings) != 1 || out.Result.Verdict != core.RunOK {
		t.Fatalf("out %+v err %v", out, err)
	}
	if r.ws.Holding(testSession) {
		t.Error("the lock must still be released")
	}
}

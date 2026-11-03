package core_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
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

func TestAgentTimeoutIsVerdictTimeoutAndExit124(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Loop()})
	req := r.req()
	req.Timeout = 50 * time.Millisecond
	out := r.run(req)
	rec := r.record()
	if out.Result.Verdict != core.RunTimeout || rec.Verdict != core.RunTimeout || rec.Exit != 124 || out.Result.Exit != 124 {
		t.Fatalf("outcome %+v record %+v", out.Result, rec)
	}
	if r.ws.Holding(testSession) {
		t.Error("lock still held")
	}
}

func TestInterruptCompletesTheRowAsInterrupted(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Loop()})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	out, err := core.Delegate(ctx, r.deps(), r.req())
	if err != nil {
		t.Fatal(err)
	}
	rec := r.record()
	if out.Result.Verdict != core.RunInterrupted || rec.Verdict != core.RunInterrupted || rec.Exit != 1 || !rec.Created {
		t.Fatalf("outcome %+v record %+v", out.Result, rec)
	}
	if r.ws.Removals() != 0 || len(r.ws.Branches()) != 1 {
		t.Error("the worktree must stay for the lead")
	}
	if r.ws.Holding(testSession) {
		t.Error("lock still held")
	}
}

func TestQuotaIsVerdictQuotaExhaustedWithExit1(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.ExhaustQuota()})
	out := r.run(r.req())
	if out.Result.Verdict != core.RunQuota || out.Result.Exit != 1 || r.record().Verdict != core.RunQuota {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestAgentNonZeroExitFailsTheRun(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Fail(3)})
	out := r.run(r.req())
	if out.Result.Verdict != core.RunFailed || out.Result.Exit != 1 {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestFailingSetupSkipsTheAgent(t *testing.T) {
	r := newRig(t, writeA)
	r.sb.Handler = func(_ context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult { return core.ExecResult{Exit: 1} }
	req := r.req()
	req.Setup = "make deps"
	out := r.run(req)
	if out.Result.Verdict != core.RunFailed || r.agent.Launches() != 0 || out.Result.Launches != 0 {
		t.Fatalf("outcome %+v, launches %d", out.Result, r.agent.Launches())
	}
	if cmds := r.sb.Cmds(); len(cmds) != 1 || cmds[0].Shell != "make deps" {
		t.Fatalf("cmds = %+v", cmds)
	}
}

func TestSuccessfulSetupRunsBeforeTheAgent(t *testing.T) {
	r := newRig(t, writeA)
	req := r.req()
	req.Setup = "make deps"
	out := r.run(req)
	if out.Result.Verdict != core.RunOK || r.agent.Launches() != 1 || len(r.sb.Cmds()) != 1 {
		t.Fatalf("outcome %+v", out.Result)
	}
	if got := r.sb.Specs()[0]; got.Dir != "/fake/worktrees/"+testSession {
		t.Fatalf("box spec = %+v", got)
	}
}

func TestCommandTimeoutIsAFailureNotExit124(t *testing.T) {
	r := newRig(t, writeA)
	r.sb.Handler = func(ctx context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		<-ctx.Done()
		return core.ExecResult{Exit: -1, Killed: true}
	}
	req := r.req()
	req.Setup, req.CommandTimeout = "sleep forever", 50*time.Millisecond
	out := r.run(req)
	if out.Result.Verdict != core.RunFailed || out.Result.Exit != 1 {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestInterruptDuringSetupIsInterrupted(t *testing.T) {
	r := newRig(t, writeA)
	r.sb.Handler = func(ctx context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		<-ctx.Done()
		return core.ExecResult{Exit: -1, Killed: true}
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	req := r.req()
	req.Setup = "sleep forever"
	out, err := core.Delegate(ctx, r.deps(), req)
	if err != nil || out.Result.Verdict != core.RunInterrupted || r.agent.Launches() != 0 {
		t.Fatalf("outcome %+v err %v launches %d", out.Result, err, r.agent.Launches())
	}
}

// deadlineAgent records the deadline of each launch and delegates to a scripted agent.
type deadlineAgent struct {
	*kanjotest.ScriptedAgent
	deadlines []time.Time
}

func (a *deadlineAgent) Run(ctx context.Context, b core.Box, req core.AgentRequest) (core.AgentResult, error) {
	dl, _ := ctx.Deadline()
	a.deadlines = append(a.deadlines, dl)
	return a.ScriptedAgent.Run(ctx, b, req)
}

func TestRetriesOnceWhenAFileIsMissing(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{}, writeA)
	out := r.run(r.req())
	rec := r.record()
	if out.Result.Verdict != core.RunOK || rec.Launches != 2 || rec.Retries != 1 || r.agent.Launches() != 2 {
		t.Fatalf("outcome %+v record %+v", out.Result, rec)
	}
}

func TestStillMissingAfterTheRetryFails(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{}, []kanjotest.AgentStep{})
	out := r.run(r.req())
	if out.Result.Verdict != core.RunFailed || out.Result.Launches != 2 || r.agent.Launches() != 2 {
		t.Fatalf("outcome %+v launches %d", out.Result, r.agent.Launches())
	}
}

func TestNoRetryAfterQuota(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.ExhaustQuota()}, writeA)
	if out := r.run(r.req()); out.Result.Launches != 1 || r.agent.Launches() != 1 || out.Result.Verdict != core.RunQuota {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestNoRetryAfterAKill(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Loop()}, writeA)
	req := r.req()
	req.Timeout = 30 * time.Millisecond
	if out := r.run(req); out.Result.Launches != 1 || r.agent.Launches() != 1 || out.Result.Verdict != core.RunTimeout {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestNoRetryAfterANonZeroExit(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Fail(2)}, writeA)
	if out := r.run(r.req()); out.Result.Launches != 1 || r.agent.Launches() != 1 {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestNoRetryWhenNothingIsMissing(t *testing.T) {
	r := newRig(t, writeA, writeA)
	if out := r.run(r.req()); out.Result.Launches != 1 || out.Result.Retries != 0 || r.agent.Launches() != 1 {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestNoRetryWithoutExpectedFiles(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{}, writeA)
	req := r.req()
	req.Expect = nil
	if out := r.run(req); out.Result.Launches != 1 || out.Result.Verdict != core.RunOK {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestRetryUsesTheSameSession(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{}, writeA)
	r.run(r.req())
	reqs := r.agent.Requests()
	if len(reqs) != 2 || reqs[0].Resume || !reqs[1].Resume || reqs[0].Session != reqs[1].Session || reqs[1].Session != testSession || reqs[1].Model != "test-model" {
		t.Fatalf("requests = %+v", reqs)
	}
	if reqs[0].Prompt != "write the file" {
		t.Errorf("first prompt = %q", reqs[0].Prompt)
	}
}

func TestRetryPromptNamesOnlyValidatedPaths(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{}, writeA)
	req := r.req()
	req.Expect = []string{"src/a.go", "src/b.go"}
	r.run(req)
	got := r.agent.Requests()[1].Prompt
	if got != core.RetryPrompt([]string{"src/a.go", "src/b.go"}) {
		t.Fatalf("prompt = %q", got)
	}
	for _, bad := range [][]string{{"../x"}, {"a\nb"}, {""}, {"/etc/passwd"}} {
		if p := core.RetryPrompt(append([]string{"src/a.go"}, bad...)); p != core.RetryPrompt([]string{"src/a.go"}) {
			t.Errorf("RetryPrompt kept %q: %q", bad, p)
		}
	}
}

func TestTimeoutCoversBothLaunches(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{}, []kanjotest.AgentStep{kanjotest.Loop()})
	da := &deadlineAgent{ScriptedAgent: r.agent}
	deps := r.deps()
	deps.Agent = da
	req := r.req()
	req.Timeout = 80 * time.Millisecond
	start := time.Now()
	out, err := core.Delegate(context.Background(), deps, req)
	if err != nil || out.Result.Verdict != core.RunTimeout || out.Result.Launches != 2 {
		t.Fatalf("outcome %+v err %v", out.Result, err)
	}
	if len(da.deadlines) != 2 || !da.deadlines[0].Equal(da.deadlines[1]) {
		t.Fatalf("deadlines differ: %v", da.deadlines)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the second launch got a fresh delay")
	}
}

func TestForbiddenPathTouchedFailsTheRun(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Write("config/prod.yaml", "x")})
	req := r.req()
	req.Forbid = []string{"config"}
	out := r.run(req)
	if out.Result.Verdict != core.RunFailed || out.Result.Exit != 1 {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestStrayFilesGiveCheckStrayFilesAndExit0(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Write("notes.txt", "x")})
	out := r.run(r.req())
	if out.Result.Verdict != core.RunStray || out.Result.Exit != 0 {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func TestUnsafeSymlinkFailsTheRun(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Symlink("src/a.go", "/etc/passwd")})
	req := r.req()
	if out := r.run(req); out.Result.Verdict != core.RunFailed {
		t.Fatalf("outcome %+v (an unsafe link fails even when the file was expected)", out.Result)
	}
}

func TestForbiddenIsJudgedEvenAfterATimeout(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write(".git/hooks/pre-commit", "x"), kanjotest.Loop()})
	req := r.req()
	req.Timeout = 30 * time.Millisecond
	if out := r.run(req); out.Result.Verdict != core.RunTimeout || out.Result.Exit != 124 {
		t.Fatalf("outcome %+v", out.Result)
	}
}

func okHandler(f func(c core.Cmd)) func(context.Context, core.BoxSpec, core.Cmd) core.ExecResult {
	return func(_ context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		f(c)
		return core.ExecResult{}
	}
}

func patchOf(t *testing.T, r *rig) string {
	t.Helper()
	b, ok := r.art.Read(testSession, core.ArtPatch)
	if !ok {
		t.Fatal("no changes.patch")
	}
	return string(b)
}

func TestPatchIsWrittenForNewChangedAndDeletedFiles(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Write("README.md", "changed"), kanjotest.Remove("gone.txt")})
	req := r.req()
	req.Expect = nil
	r.run(req)
	p := patchOf(t, r)
	for _, name := range []string{"src/a.go", "README.md", "gone.txt"} {
		if !strings.Contains(p, name) {
			t.Errorf("patch lacks %s:\n%s", name, p)
		}
	}
}

func TestPatchOverTheCapFailsTheRun(t *testing.T) {
	r := newRig(t, writeA)
	req := r.req()
	req.MaxPatchBytes = 5
	out := r.run(req)
	if out.Result.Verdict != core.RunFailed {
		t.Fatalf("outcome %+v", out.Result)
	}
	if _, ok := r.art.Read(testSession, core.ArtPatch); ok {
		t.Fatal("a partial patch was kept")
	}
}

func TestInterruptStillWritesThePatchOfWhatExists(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Loop()})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	out, err := core.Delegate(ctx, r.deps(), r.req())
	if err != nil || out.Result.Verdict != core.RunInterrupted {
		t.Fatalf("outcome %+v err %v", out.Result, err)
	}
	if !strings.Contains(patchOf(t, r), "src/a.go") {
		t.Fatal("the patch lacks what the agent had written")
	}
}

func TestTestAndChecksRunInOrderInTheSameBox(t *testing.T) {
	r := newRig(t, writeA)
	req := r.req()
	req.Test, req.Checks = "go test", []string{"lint", "vet"}
	out := r.run(req)
	var got []string
	for _, c := range r.sb.Cmds() {
		got = append(got, c.Shell)
	}
	if out.Result.Verdict != core.RunOK || !reflect.DeepEqual(got, []string{"go test", "lint", "vet"}) || len(r.sb.Specs()) != 1 {
		t.Fatalf("verdict %s cmds %v boxes %d", out.Result.Verdict, got, len(r.sb.Specs()))
	}
}

func TestFailingTestStillRunsTheChecks(t *testing.T) {
	r := newRig(t, writeA)
	r.sb.Handler = func(_ context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		if c.Shell == "go test" {
			return core.ExecResult{Exit: 1}
		}
		return core.ExecResult{}
	}
	req := r.req()
	req.Test, req.Checks = "go test", []string{"lint"}
	out := r.run(req)
	if out.Result.Verdict != core.RunFailed || len(r.sb.Cmds()) != 2 {
		t.Fatalf("verdict %s cmds %d", out.Result.Verdict, len(r.sb.Cmds()))
	}
}

func TestFailingCheckFailsTheRun(t *testing.T) {
	r := newRig(t, writeA)
	r.sb.Handler = func(_ context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		return core.ExecResult{Exit: map[string]int{"vet": 2}[c.Shell]}
	}
	req := r.req()
	req.Checks = []string{"lint", "vet"}
	if out := r.run(req); out.Result.Verdict != core.RunFailed {
		t.Fatalf("verdict %s", out.Result.Verdict)
	}
}

func TestCommandsRunInTheWorkdirWithTheBoxEnvironment(t *testing.T) {
	r := newRig(t, writeA)
	req := r.req()
	req.Test, req.BoxEnv = "go test", []string{"PATH=/bin", "LANG=C"}
	r.run(req)
	spec := r.sb.Specs()[0]
	if spec.Dir != "/fake/worktrees/"+testSession || !reflect.DeepEqual(spec.Env, []string{"PATH=/bin", "LANG=C"}) {
		t.Fatalf("spec = %+v", spec)
	}
	if c := r.sb.Cmds()[0]; c.Grace != req.Grace || c.Shell != "go test" {
		t.Fatalf("cmd = %+v", c)
	}
}

func TestCommandsAreNotRunWhenTheAgentDidNotEndNormally(t *testing.T) {
	for name, steps := range map[string][]kanjotest.AgentStep{
		"killed": {kanjotest.Loop()}, "failed": {kanjotest.Fail(1)}, "quota": {kanjotest.ExhaustQuota()},
	} {
		r := newRig(t, steps)
		req := r.req()
		req.Test, req.Checks, req.Timeout = "go test", []string{"lint"}, 30*time.Millisecond
		r.run(req)
		if len(r.sb.Cmds()) != 0 {
			t.Errorf("%s: commands ran: %v", name, r.sb.Cmds())
		}
	}
}

func TestEachCommandHasItsOwnLog(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Output("agent says hi")})
	r.sb.Handler = okHandler(func(c core.Cmd) { io.WriteString(c.Out, "out of "+c.Shell) })
	req := r.req()
	req.Setup, req.Test, req.Checks = "setup cmd", "test cmd", []string{"first", "second"}
	r.run(req)
	for name, want := range map[core.ArtifactName]string{
		core.ArtSetupLog: "out of setup cmd", core.ArtTestLog: "out of test cmd",
		core.ArtCheckLog(1): "out of first", core.ArtCheckLog(2): "out of second", core.ArtAgentLog: "agent says hi",
	} {
		if got, ok := r.art.Read(testSession, name); !ok || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", name, got, ok, want)
		}
	}
}

func TestChecksSideEffectsAreNotJudged(t *testing.T) {
	r := newRig(t, writeA)
	r.sb.Handler = okHandler(func(c core.Cmd) { r.ws.Put(testSession, "test-output.txt", "x") })
	req := r.req()
	req.Test = "go test"
	out := r.run(req)
	if out.Result.Verdict != core.RunOK {
		t.Fatalf("verdict %s: what the checks write is not the agent's doing", out.Result.Verdict)
	}
	if strings.Contains(patchOf(t, r), "test-output.txt") {
		t.Error("the checks' output is in the patch")
	}
}

func TestInterruptDuringTestsStopsTheRemainingCommands(t *testing.T) {
	r := newRig(t, writeA)
	r.sb.Handler = func(ctx context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		<-ctx.Done()
		return core.ExecResult{Exit: -1, Killed: true}
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	req := r.req()
	req.Test, req.Checks = "slow", []string{"never"}
	out, err := core.Delegate(ctx, r.deps(), req)
	if err != nil || out.Result.Verdict != core.RunInterrupted || len(r.sb.Cmds()) != 1 {
		t.Fatalf("outcome %+v err %v cmds %d", out.Result, err, len(r.sb.Cmds()))
	}
}

func TestAgentRequestCarriesTheGrace(t *testing.T) {
	r := newRig(t, writeA)
	r.run(r.req())
	if got := r.agent.Requests()[0].Grace; got != 50*time.Millisecond {
		t.Fatalf("grace = %v", got)
	}
}

func TestDelegateWarnsOfIgnoredFilesWithoutChangingTheVerdict(t *testing.T) {
	r := newRig(t, append([]kanjotest.AgentStep{kanjotest.WriteIgnored("sub/hidden", "x")}, writeA...))
	out := r.run(r.req())
	if out.Result.Verdict != core.RunOK {
		t.Fatalf("verdict = %v, ignored files must not change it", out.Result.Verdict)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "ignored") {
		t.Fatalf("warnings = %q, want one about ignored files", out.Warnings)
	}
}

func TestDelegateDoesNotWarnWithoutIgnoredFiles(t *testing.T) {
	r := newRig(t, writeA)
	out := r.run(r.req())
	if len(out.Warnings) != 0 {
		t.Fatalf("warnings = %q", out.Warnings)
	}
}

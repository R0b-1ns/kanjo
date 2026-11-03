package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

func goodRequest() core.DelegateRequest {
	return core.DelegateRequest{
		Task: "ABC-12", Adapter: "script", Model: "m", Prompt: "p",
		Timeout: time.Second, CommandTimeout: time.Second, Grace: 0, MaxPatchBytes: 1,
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s + strings.Repeat("x", i) // distinct valid paths
	}
	return out
}

func TestRequestLimitsAreInclusive(t *testing.T) {
	cases := []struct {
		name   string
		mut    func(*core.DelegateRequest)
		wantOK bool
	}{
		{"64 expect", func(q *core.DelegateRequest) { q.Expect = repeat("f", 64) }, true},
		{"65 expect", func(q *core.DelegateRequest) { q.Expect = repeat("f", 65) }, false},
		{"64 forbid", func(q *core.DelegateRequest) { q.Forbid = repeat("f", 64) }, true},
		{"65 forbid", func(q *core.DelegateRequest) { q.Forbid = repeat("f", 65) }, false},
		{"setup 4096", func(q *core.DelegateRequest) { q.Setup = strings.Repeat("a", 4096) }, true},
		{"setup 4097", func(q *core.DelegateRequest) { q.Setup = strings.Repeat("a", 4097) }, false},
		{"test 4096", func(q *core.DelegateRequest) { q.Test = strings.Repeat("a", 4096) }, true},
		{"check 4096", func(q *core.DelegateRequest) { q.Checks = []string{strings.Repeat("a", 4096)} }, true},
		{"check 4097", func(q *core.DelegateRequest) { q.Checks = []string{strings.Repeat("a", 4097)} }, false},
		{"empty check", func(q *core.DelegateRequest) { q.Checks = []string{"ok", ""} }, false},
		{"32 checks", func(q *core.DelegateRequest) { q.Checks = repeat("c", 32) }, true},
		{"33 checks", func(q *core.DelegateRequest) { q.Checks = repeat("c", 33) }, false},
		{"no task", func(q *core.DelegateRequest) { q.Task = "" }, true},
		{"dash task", func(q *core.DelegateRequest) { q.Task = "-" }, true}, // "-" is the no-task label
		{"timeout 1ns", func(q *core.DelegateRequest) { q.Timeout = 1 }, true},
		{"timeout 0", func(q *core.DelegateRequest) { q.Timeout = 0 }, false},
		{"timeout -1", func(q *core.DelegateRequest) { q.Timeout = -1 }, false},
		{"command timeout 1ns", func(q *core.DelegateRequest) { q.CommandTimeout = 1 }, true},
		{"command timeout 0", func(q *core.DelegateRequest) { q.CommandTimeout = 0 }, false},
		{"grace 0", func(q *core.DelegateRequest) { q.Grace = 0 }, true},
		{"grace -1", func(q *core.DelegateRequest) { q.Grace = -1 }, false},
		{"patch cap 1", func(q *core.DelegateRequest) { q.MaxPatchBytes = 1 }, true},
		{"patch cap 0", func(q *core.DelegateRequest) { q.MaxPatchBytes = 0 }, false},
	}
	for _, c := range cases {
		q := goodRequest()
		c.mut(&q)
		if err := q.Validate(); (err == nil) != c.wantOK {
			t.Errorf("%s: err = %v, want ok %v", c.name, err, c.wantOK)
		}
	}
}

func TestNoTaskLabelIsRecordedAsDash(t *testing.T) {
	r := newRig(t, writeA)
	req := r.req()
	req.Task = ""
	r.run(req)
	if got := r.record().Task; got != core.NoTask {
		t.Fatalf("task = %q", got)
	}
}

// stateFailing is a workspace whose nth State call fails.
type stateFailing struct {
	*kanjotest.FakeWorkspace
	n, calls int
}

func (s *stateFailing) State(ctx context.Context, w core.Workdir) (core.State, error) {
	s.calls++
	if s.calls == s.n {
		return core.State{}, errors.New("boom")
	}
	return s.FakeWorkspace.State(ctx, w)
}

func TestDelegateStopsWhenTheWorkspaceCannotBeRead(t *testing.T) {
	cases := []struct {
		fail     int
		launches int
		steps    [][]kanjotest.AgentStep
	}{
		{1, 0, [][]kanjotest.AgentStep{writeA}},              // before the agent
		{2, 1, [][]kanjotest.AgentStep{writeA}},              // after the first launch
		{2, 1, [][]kanjotest.AgentStep{{kanjotest.Fail(1)}}}, // after a failed launch (final read)
		{3, 2, [][]kanjotest.AgentStep{{}, writeA}},          // after the retry
	}
	for _, c := range cases {
		r := newRig(t, c.steps...)
		deps := r.deps()
		deps.Workspace = &stateFailing{FakeWorkspace: r.ws, n: c.fail}
		req := r.req()
		req.Test = "go test"
		out, err := core.Delegate(context.Background(), deps, req)
		if err != nil {
			t.Fatal(err)
		}
		if out.Result.Verdict != core.RunFailed || !strings.Contains(strings.Join(reasonStrings(out), ","), "workspace-failed") {
			t.Errorf("fail %d: outcome %+v reasons %v", c.fail, out.Result, out.Report.Reasons)
		}
		if _, ok := r.art.Read(testSession, core.ArtPatch); ok || len(r.sb.Cmds()) != 0 {
			t.Errorf("fail %d: a patch was written or a command ran after the read failed", c.fail)
		}
		if out.Result.Launches != c.launches {
			t.Errorf("fail %d: launches = %d, want %d", c.fail, out.Result.Launches, c.launches)
		}
		if r.ws.Holding(testSession) {
			t.Errorf("fail %d: the lock is still held", c.fail)
		}
	}
}

func reasonStrings(out core.DelegateOutcome) []string {
	var rs []string
	for _, r := range out.Report.Reasons {
		rs = append(rs, string(r))
	}
	return rs
}

func TestADelegationReadsTheWorktreeOncePerStageOnly(t *testing.T) {
	r := newRig(t, writeA)
	sf := &stateFailing{FakeWorkspace: r.ws}
	deps := r.deps()
	deps.Workspace = sf
	if _, err := core.Delegate(context.Background(), deps, r.req()); err != nil {
		t.Fatal(err)
	}
	if sf.calls != 2 {
		t.Fatalf("State was called %d times, want 2 (before and after the agent)", sf.calls)
	}
}

// failingCreated is a journal that cannot mark a run created.
type failingCreated struct{ *kanjotest.MemRunJournal }

func (failingCreated) Created(context.Context, string) error { return errors.New("disk full") }

// failingOpen is a sandbox that cannot open a box.
type failingOpen struct{}

func (failingOpen) Open(context.Context, core.BoxSpec) (core.Box, error) {
	return nil, errors.New("no box")
}

func TestSetupFailuresGiveExactlyTheirReason(t *testing.T) {
	cases := map[string]struct {
		mut  func(*rig, *core.DelegateDeps)
		want string
	}{
		"create": {func(r *rig, d *core.DelegateDeps) {
			c, _ := r.ws.Resolve(context.Background(), "HEAD")
			r.ws.Create(context.Background(), testSession, c)
		}, "workspace-failed"},
		"created": {func(r *rig, d *core.DelegateDeps) { d.Journal = failingCreated{r.j} }, "workspace-failed"},
		"open":    {func(r *rig, d *core.DelegateDeps) { d.Sandbox = failingOpen{} }, "sandbox-failed"},
	}
	for name, c := range cases {
		r := newRig(t, writeA)
		deps := r.deps()
		c.mut(r, &deps)
		out, err := core.Delegate(context.Background(), deps, r.req())
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(reasonStrings(out), ","); got != c.want {
			t.Errorf("%s: reasons = %q, want %q", name, got, c.want)
		}
		if r.agent.Launches() != 0 || len(r.sb.Cmds()) != 0 {
			t.Errorf("%s: the agent or a command ran", name)
		}
		if name != "create" && r.ws.Holding(testSession) {
			t.Errorf("%s: the lock is still held", name)
		}
	}
}

func TestStepTimedOutFlags(t *testing.T) {
	// a command that outlives its own timeout
	r := newRig(t, writeA)
	r.sb.Handler = func(ctx context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		<-ctx.Done()
		return core.ExecResult{Exit: -1, Killed: true}
	}
	req := r.req()
	req.Test, req.CommandTimeout = "slow", 30*time.Millisecond
	out := r.run(req)
	steps := map[string]core.ReportStep{}
	for _, s := range out.Report.Steps {
		steps[s.Name] = s
	}
	if !steps["test"].TimedOut || steps["agent"].TimedOut {
		t.Fatalf("steps = %+v", out.Report.Steps)
	}
	// an agent that runs out of time, and one that is interrupted
	r = newRig(t, []kanjotest.AgentStep{kanjotest.Loop()})
	req = r.req()
	req.Timeout = 30 * time.Millisecond
	if out := r.run(req); !out.Report.Steps[0].TimedOut {
		t.Fatalf("the agent step must be timed out: %+v", out.Report.Steps)
	}
	r = newRig(t, []kanjotest.AgentStep{kanjotest.Loop()})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	out, _ = core.Delegate(ctx, r.deps(), r.req())
	if out.Report.Steps[0].TimedOut {
		t.Fatalf("an interrupt is not a timeout: %+v", out.Report.Steps)
	}
	// an interrupted command is not a timed-out command
	r = newRig(t, writeA)
	r.sb.Handler = func(ctx context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		<-ctx.Done()
		return core.ExecResult{Exit: -1, Killed: true}
	}
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	req = r.req()
	req.Test = "slow"
	out, _ = core.Delegate(ctx, r.deps(), req)
	for _, s := range out.Report.Steps {
		if s.Name == "test" && s.TimedOut {
			t.Fatalf("an interrupted command is not timed out: %+v", out.Report.Steps)
		}
	}
}

// steppingClock goes back by a minute at its second reading.
type steppingClock struct{ n int }

func (c *steppingClock) Now() time.Time {
	c.n++
	if c.n == 1 {
		return testNow
	}
	return testNow.Add(-time.Minute)
}

func TestDurationIsNeverNegative(t *testing.T) {
	r := newRig(t, writeA)
	deps := r.deps()
	deps.Clock = &steppingClock{}
	out, err := core.Delegate(context.Background(), deps, r.req())
	if err != nil || out.Result.Duration != 0 {
		t.Fatalf("duration = %v err %v", out.Result.Duration, err)
	}
}

func TestDurationFollowsTheClock(t *testing.T) {
	r := newRig(t, writeA)
	deps := r.deps()
	deps.Clock = &advancingClock{t: testNow}
	out, _ := core.Delegate(context.Background(), deps, r.req())
	if out.Result.Duration != 5*time.Second || out.Report.DurationMS != 5000 {
		t.Fatalf("duration = %v, report %d ms", out.Result.Duration, out.Report.DurationMS)
	}
}

// advancingClock moves 5 seconds after its first reading.
type advancingClock struct {
	t time.Time
	n int
}

func (c *advancingClock) Now() time.Time {
	c.n++
	if c.n == 1 {
		return c.t
	}
	return c.t.Add(5 * time.Second)
}

func TestUsageLogCountsItsSessionsAndRows(t *testing.T) {
	r, _ := usageRig(t)
	logs := r.st.Logs()
	if len(logs) != 1 || logs[0].Sessions != 1 || logs[0].Rows != 2 || !logs[0].At.Equal(testNow) {
		t.Fatalf("logs = %+v", logs)
	}
}

// presenceWorkspace reports a fixed presence and removes nothing.
type presenceWorkspace struct {
	*kanjotest.FakeWorkspace
	p core.Presence
}

func (w presenceWorkspace) Inspect(context.Context, core.Owned) (core.Presence, error) {
	return w.p, nil
}
func (w presenceWorkspace) Remove(context.Context, core.Owned) error { return nil }

func TestWorkspaceIsGoneOnlyWhenNeitherFolderNorBranchRemains(t *testing.T) {
	cases := []struct {
		p    core.Presence
		gone bool
	}{
		{core.Presence{}, true},
		{core.Presence{DirExists: true}, false},
		{core.Presence{BranchExists: true}, false},
		{core.Presence{DirExists: true, BranchExists: true}, false},
	}
	for _, c := range cases {
		r := finished(t)
		d := r.finishDeps()
		d.Workspace = presenceWorkspace{r.ws, c.p}
		out, err := core.Finish(context.Background(), d, testSession, core.Accepted)
		if err != nil || out.WorkspaceGone != c.gone {
			t.Errorf("%+v: gone = %v err %v", c.p, out.WorkspaceGone, err)
		}
	}
}

func TestReportTextOfKnownValues(t *testing.T) {
	r := core.RunReport{
		Session: "ses_1", Task: "T-1", Adapter: "script", Model: "m", Verdict: core.RunFailed,
		Reasons: []core.Reason{core.ReasonAgentFailed}, ExitCode: 1, DurationMS: 8120, Launches: 2, Retries: 1,
		Tokens: core.ReportTokens{Status: "available", ByModel: []core.ReportModel{{Model: "m", Input: 3, CostUSD: costPtr(0.5)}, {Model: "n"}}},
		Steps:  []core.ReportStep{{Name: "agent", Exit: 0, DurationMS: 1500}, {Name: "test", Exit: -1, DurationMS: 2000, TimedOut: true}},
		Patch:  "changes.patch",
	}
	txt := r.Text("")
	for _, want := range []string{
		"reasons: agent-failed\n", "duration: 8.12s\n", "launches: 2 (retries 1)\n",
		"cost $0.5000", "cost unknown", "step agent: exit 0, 1.5s\n", "step test: exit -1, 2s, timed out\n",
		"note: T-1 ses_1 failed 8.12s\n",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("text lacks %q:\n%s", want, txt)
		}
	}
	r.Reasons, r.Patch = nil, ""
	if txt := r.Text(""); !strings.Contains(txt, "reasons: none\n") || strings.Contains(txt, "apply:") {
		t.Errorf("text:\n%s", txt)
	}
}

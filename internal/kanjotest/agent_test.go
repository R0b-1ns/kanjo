package kanjotest_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

func scriptedFixture(t *testing.T) kanjotest.AgentFixture {
	fw := kanjotest.NewFakeWorkspace()
	ctx := context.Background()
	commit, _ := fw.Resolve(ctx, "HEAD")
	if _, err := fw.Create(ctx, "ses_c", commit); err != nil {
		t.Fatal(err)
	}
	box, _ := kanjotest.NewFakeSandbox().Open(ctx, core.BoxSpec{Dir: "/fake/w"})
	return kanjotest.AgentFixture{
		Box: box, Session: "ses_c",
		New: func(b kanjotest.AgentBehavior) core.AgentRunner {
			switch b {
			case kanjotest.AgentFails:
				return kanjotest.NewScriptedAgent(fw, []kanjotest.AgentStep{kanjotest.Fail(3)})
			case kanjotest.AgentLoops:
				return kanjotest.NewScriptedAgent(fw, []kanjotest.AgentStep{kanjotest.Loop()})
			case kanjotest.AgentReportsUsage:
				return kanjotest.NewScriptedAgent(fw, []kanjotest.AgentStep{kanjotest.ReportUsage("test-model", core.Tokens{Input: 5}, nil)})
			}
			return kanjotest.NewScriptedAgent(fw, []kanjotest.AgentStep{kanjotest.Succeed()})
		},
	}
}

func TestScriptedAgentContract(t *testing.T) {
	kanjotest.AgentRunnerContract(t, scriptedFixture)
}

func newRun(t *testing.T, launches ...[]kanjotest.AgentStep) (*kanjotest.FakeWorkspace, *kanjotest.ScriptedAgent, core.Workdir) {
	t.Helper()
	fw := kanjotest.NewFakeWorkspace()
	commit, _ := fw.Resolve(context.Background(), "HEAD")
	w, err := fw.Create(context.Background(), "ses_s", commit)
	if err != nil {
		t.Fatal(err)
	}
	return fw, kanjotest.NewScriptedAgent(fw, launches...), w
}

func run(t *testing.T, a *kanjotest.ScriptedAgent, req core.AgentRequest) core.AgentResult {
	t.Helper()
	req.Session = "ses_s"
	res, err := a.Run(context.Background(), nil, req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func entryFor(st core.State, path string) (core.Entry, bool) {
	for _, e := range st.Entries {
		if e.Path == path {
			return e, true
		}
	}
	return core.Entry{}, false
}

func TestScriptedAgentFileStepsChangeTheWorkspace(t *testing.T) {
	fw, a, w := newRun(t, []kanjotest.AgentStep{
		kanjotest.Write("src/a.go", "package a"), kanjotest.Remove("gone.txt"), kanjotest.Symlink("ln", "/etc/passwd"),
	})
	res := run(t, a, core.AgentRequest{})
	st, _ := fw.State(context.Background(), w)
	if _, ok := entryFor(st, "src/a.go"); !ok {
		t.Error("Write step had no effect")
	}
	if _, ok := entryFor(st, "gone.txt"); ok {
		t.Error("Remove step had no effect")
	}
	if e, ok := entryFor(st, "ln"); !ok || e.Link != "/etc/passwd" {
		t.Errorf("Symlink step: %+v", e)
	}
	if res.Exit != 0 || res.Killed || res.Quota || res.Usage != nil {
		t.Errorf("res = %+v", res)
	}
}

func TestScriptedAgentUsesTheNthStepListPerLaunch(t *testing.T) {
	fw, a, w := newRun(t,
		[]kanjotest.AgentStep{kanjotest.Write("first.txt", "1")},
		[]kanjotest.AgentStep{kanjotest.Write("second.txt", "2")})
	run(t, a, core.AgentRequest{Prompt: "p1"})
	st, _ := fw.State(context.Background(), w)
	if _, ok := entryFor(st, "second.txt"); ok {
		t.Fatal("launch 2 ran in launch 1")
	}
	run(t, a, core.AgentRequest{Prompt: "p2", Resume: true})
	st, _ = fw.State(context.Background(), w)
	if _, ok := entryFor(st, "second.txt"); !ok {
		t.Fatal("launch 2 did not run its steps")
	}
	reqs := a.Requests()
	if a.Launches() != 2 || len(reqs) != 2 || reqs[0].Prompt != "p1" || reqs[1].Prompt != "p2" || !reqs[1].Resume || reqs[0].Resume {
		t.Fatalf("launches %d, requests %+v", a.Launches(), reqs)
	}
	run(t, a, core.AgentRequest{}) // beyond the lists: succeeds, does nothing
	if a.Launches() != 3 {
		t.Fatalf("launches = %d", a.Launches())
	}
}

func TestScriptedAgentFailQuotaAndUsage(t *testing.T) {
	cost := 0.0
	_, a, _ := newRun(t,
		[]kanjotest.AgentStep{kanjotest.Fail(7)},
		[]kanjotest.AgentStep{kanjotest.ExhaustQuota()},
		[]kanjotest.AgentStep{kanjotest.ReportUsage("m1", core.Tokens{Input: 1}, &cost), kanjotest.ReportUsage("m2", core.Tokens{Output: 2}, nil)})
	if res := run(t, a, core.AgentRequest{}); res.Exit != 7 || res.Quota || res.Killed {
		t.Errorf("fail: %+v", res)
	}
	if res := run(t, a, core.AgentRequest{}); !res.Quota || res.Exit == 0 {
		t.Errorf("quota: %+v", res)
	}
	res := run(t, a, core.AgentRequest{})
	if len(res.Usage) != 2 || res.Usage[0].Model != "m1" || res.Usage[0].CostUSD == nil || *res.Usage[0].CostUSD != 0 || res.Usage[1].CostUSD != nil || res.Usage[1].Tokens.Output != 2 {
		t.Errorf("usage: %+v", res.Usage)
	}
}

func TestScriptedAgentLoopEndsWithTheContextAndSkipsLaterSteps(t *testing.T) {
	fw, a, w := newRun(t, []kanjotest.AgentStep{kanjotest.Write("before.txt", "1"), kanjotest.Loop(), kanjotest.Write("after.txt", "2")})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res, err := a.Run(ctx, nil, core.AgentRequest{Session: "ses_s"})
	if err != nil || !res.Killed {
		t.Fatalf("res %+v err %v", res, err)
	}
	st, _ := fw.State(context.Background(), w)
	if _, ok := entryFor(st, "before.txt"); !ok {
		t.Error("the step before Loop did not run")
	}
	if _, ok := entryFor(st, "after.txt"); ok {
		t.Error("a step after Loop ran")
	}
}

func TestScriptedAgentOutputGoesToTheLog(t *testing.T) {
	_, a, _ := newRun(t, []kanjotest.AgentStep{kanjotest.Output("said this")})
	var log bytes.Buffer
	run(t, a, core.AgentRequest{Log: &log})
	if log.String() != "said this" {
		t.Fatalf("log = %q", log.String())
	}
	run(t, a, core.AgentRequest{}) // no log writer: must not panic
}

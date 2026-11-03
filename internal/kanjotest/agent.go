package kanjotest

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// agentRun is what one launch of a ScriptedAgent builds up.
type agentRun struct {
	ctx    context.Context
	w      *FakeWorkspace
	req    core.AgentRequest
	res    core.AgentResult
	halted bool
}

// AgentStep is one thing a ScriptedAgent does during a launch.
type AgentStep func(*agentRun)

// Write creates or changes a file of the worktree.
func Write(path, content string) AgentStep {
	return func(r *agentRun) { r.w.Put(r.req.Session, path, content) }
}

// Remove deletes a file of the worktree.
func Remove(path string) AgentStep {
	return func(r *agentRun) { r.w.Delete(r.req.Session, path) }
}

// Symlink creates a symbolic link in the worktree.
func Symlink(path, target string) AgentStep {
	return func(r *agentRun) { r.w.Link(r.req.Session, path, target) }
}

// Succeed ends the launch normally (exit 0); it is the default.
func Succeed() AgentStep { return func(r *agentRun) { r.res.Exit = 0 } }

// Fail ends the launch with a non-zero exit code.
func Fail(code int) AgentStep { return func(r *agentRun) { r.res.Exit = code } }

// ExhaustQuota ends the launch reporting that the quota is exhausted.
func ExhaustQuota() AgentStep {
	return func(r *agentRun) { r.res.Quota, r.res.Exit = true, 1 }
}

// Loop blocks until the context ends, then reports the launch as killed; the
// steps after it do not run.
func Loop() AgentStep {
	return func(r *agentRun) {
		<-r.ctx.Done()
		r.res.Killed, r.res.Exit, r.halted = true, -1, true
	}
}

// ReportUsage adds the usage of one model to the result; cost nil is unknown.
func ReportUsage(model string, t core.Tokens, cost *float64) AgentStep {
	return func(r *agentRun) {
		r.res.Usage = append(r.res.Usage, core.ModelUsage{Model: model, Tokens: t, CostUSD: cost})
	}
}

// Output writes text to the request's log, if it has one.
func Output(text string) AgentStep {
	return func(r *agentRun) {
		if r.req.Log != nil {
			io.WriteString(r.req.Log, text)
		}
	}
}

// ScriptedAgent is a core.AgentRunner that applies steps to a FakeWorkspace.
// The nth launch (the retry is the second) uses the nth step list; a launch
// beyond the lists does nothing and succeeds.
type ScriptedAgent struct {
	w        *FakeWorkspace
	launches [][]AgentStep
	mu       sync.Mutex
	reqs     []core.AgentRequest
}

// NewScriptedAgent returns an agent with one step list per launch.
func NewScriptedAgent(w *FakeWorkspace, launches ...[]AgentStep) *ScriptedAgent {
	return &ScriptedAgent{w: w, launches: launches}
}

// Launches counts the calls to Run so far.
func (a *ScriptedAgent) Launches() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.reqs)
}

// Requests returns every request received, in order.
func (a *ScriptedAgent) Requests() []core.AgentRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]core.AgentRequest, len(a.reqs))
	copy(out, a.reqs)
	return out
}

// Run implements core.AgentRunner. The box is not used.
func (a *ScriptedAgent) Run(ctx context.Context, _ core.Box, req core.AgentRequest) (core.AgentResult, error) {
	a.mu.Lock()
	n := len(a.reqs)
	a.reqs = append(a.reqs, req)
	a.mu.Unlock()
	r := &agentRun{ctx: ctx, w: a.w, req: req}
	start := time.Now()
	if n < len(a.launches) {
		for _, step := range a.launches[n] {
			step(r)
			if r.halted {
				break
			}
		}
	}
	r.res.Duration = time.Since(start)
	return r.res, nil
}

// AgentBehavior is what a contract asks a runner to do.
type AgentBehavior int

const (
	AgentSucceeds AgentBehavior = iota
	AgentFails
	AgentLoops
	AgentReportsUsage
)

// AgentFixture is what AgentRunnerContract needs: a box, the session of a
// worktree the runner may act on, and a constructor for each behavior.
type AgentFixture struct {
	Box     core.Box
	Session string
	New     func(b AgentBehavior) core.AgentRunner
}

const agentCanary = "CANARY-PROMPT-6d1"

func (f AgentFixture) req() core.AgentRequest {
	return core.AgentRequest{Session: f.Session, Model: "test-model", Prompt: "do it " + agentCanary}
}

var agentCases = []contractCase[AgentFixture]{
	{"a cancelled context ends the call within the bound and sets Killed", func(t *testing.T, f AgentFixture) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		type out struct {
			res core.AgentResult
			err error
		}
		done := make(chan out, 1)
		go func() {
			res, err := f.New(AgentLoops).Run(ctx, f.Box, f.req())
			done <- out{res, err}
		}()
		select {
		case o := <-done:
			if o.err != nil || !o.res.Killed {
				t.Fatalf("res %+v err %v", o.res, o.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after its context ended")
		}
	}},
	{"a failing agent is a result, not an error", func(t *testing.T, f AgentFixture) {
		res, err := f.New(AgentFails).Run(context.Background(), f.Box, f.req())
		if err != nil || res.Exit == 0 || res.Killed {
			t.Fatalf("res %+v err %v", res, err)
		}
	}},
	{"the result carries no prompt or output text", func(t *testing.T, f AgentFixture) {
		res, err := f.New(AgentReportsUsage).Run(context.Background(), f.Box, f.req())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(fmt.Sprintf("%+v", res), agentCanary) {
			t.Fatalf("result repeats the prompt: %+v", res)
		}
		if bad := stringFieldsOutsideModel(reflect.TypeOf(res), "AgentResult"); len(bad) > 0 {
			t.Fatalf("AgentResult can carry text in %v", bad)
		}
	}},
	{"Resume is accepted", func(t *testing.T, f AgentFixture) {
		req := f.req()
		req.Resume = true
		if _, err := f.New(AgentSucceeds).Run(context.Background(), f.Box, req); err != nil {
			t.Fatal(err)
		}
	}},
	{"two calls in a row work", func(t *testing.T, f AgentFixture) {
		a := f.New(AgentSucceeds)
		for i := 0; i < 2; i++ {
			if res, err := a.Run(context.Background(), f.Box, f.req()); err != nil || res.Exit != 0 {
				t.Fatalf("call %d: res %+v err %v", i+1, res, err)
			}
		}
	}},
	{"usage nil means unavailable, not zero", func(t *testing.T, f AgentFixture) {
		res, _ := f.New(AgentSucceeds).Run(context.Background(), f.Box, f.req())
		if res.Usage != nil {
			t.Fatalf("usage = %+v, want nil", res.Usage)
		}
		res, _ = f.New(AgentReportsUsage).Run(context.Background(), f.Box, f.req())
		if len(res.Usage) == 0 || !core.ValidModel(res.Usage[0].Model) {
			t.Fatalf("usage = %+v", res.Usage)
		}
	}},
	{"a request without a log is accepted", func(t *testing.T, f AgentFixture) {
		req := f.req()
		req.Log = nil
		if _, err := f.New(AgentSucceeds).Run(context.Background(), f.Box, req); err != nil {
			t.Fatal(err)
		}
	}},
}

// stringFieldsOutsideModel lists the string-typed fields of t, recursively,
// except ModelUsage.Model, which is a model id.
func stringFieldsOutsideModel(t reflect.Type, path string) []string {
	switch t.Kind() {
	case reflect.String:
		return []string{path}
	case reflect.Slice, reflect.Pointer:
		return stringFieldsOutsideModel(t.Elem(), path)
	case reflect.Struct:
		var bad []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if t == reflect.TypeOf(core.ModelUsage{}) && f.Name == "Model" {
				continue
			}
			bad = append(bad, stringFieldsOutsideModel(f.Type, path+"."+f.Name)...)
		}
		return bad
	}
	return nil
}

// AgentRunnerContract checks the behavior every core.AgentRunner must have.
func AgentRunnerContract(t *testing.T, open func(t *testing.T) AgentFixture) {
	runCases(t, agentCases, open)
}

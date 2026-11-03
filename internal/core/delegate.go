package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Reason is one cause of a verdict, from a closed list (spec section 10).
type Reason string

const (
	ReasonWorkspaceFailed Reason = "workspace-failed"
	ReasonSandboxFailed   Reason = "sandbox-failed"
	ReasonAgentFailed     Reason = "agent-failed"
)

// reasonVerdict says which verdict a reason leads to.
var reasonVerdict = map[Reason]RunVerdict{
	ReasonWorkspaceFailed: RunFailed,
	ReasonSandboxFailed:   RunFailed,
	ReasonAgentFailed:     RunFailed,
}

// verdictOrder is the precedence when several reasons apply.
var verdictOrder = []RunVerdict{RunInterrupted, RunTimeout, RunQuota, RunFailed, RunStray}

// verdictOf is the verdict that wins among the reasons.
func verdictOf(reasons []Reason) RunVerdict {
	for _, v := range verdictOrder {
		for _, r := range reasons {
			if reasonVerdict[r] == v {
				return v
			}
		}
	}
	return RunOK
}

const (
	maxPaths      = 64
	maxCommandLen = 4096
	// bookkeepingLimit bounds the writes that finish a run after an interrupt.
	bookkeepingLimit = 10 * time.Second
)

// DelegateDeps are the ports a delegation needs.
type DelegateDeps struct {
	Journal    RunJournal
	Workspace  Workspace
	Sandbox    Sandbox
	Agent      AgentRunner
	Artifacts  Artifacts
	Store      Store
	Clock      Clock
	NewSession func(now time.Time) string
}

// DelegateRequest is one task handed to an agent. Commands come from the
// command line only and are never stored.
type DelegateRequest struct {
	Task, Adapter, Model, Prompt, Ref string
	Expect, Forbid                    []string
	Setup, Test                       string
	Checks                            []string
	Timeout, CommandTimeout, Grace    time.Duration
	BoxEnv                            []string
	MaxPatchBytes                     int64
}

// DelegateOutcome is what a finished delegation returns.
type DelegateOutcome struct {
	Session string
	Result  RunResult
	// Warnings are problems that did not change the verdict (a file that could
	// not be written, a record that could not be completed).
	Warnings []string
}

func validPaths(ps []string) bool {
	if len(ps) > maxPaths {
		return false
	}
	for _, p := range ps {
		if !ValidRepoPath(p) {
			return false
		}
	}
	return true
}

func validCommand(s string) bool {
	return s != "" && len(s) <= maxCommandLen && !strings.ContainsRune(s, 0)
}

func (q DelegateRequest) validate() error {
	switch {
	case q.Task != "" && !ValidTask(q.Task):
		return errors.New("invalid task label")
	case !ValidSource(q.Adapter):
		return errors.New("invalid adapter name")
	case !ValidModel(q.Model):
		return errors.New("invalid model id")
	case q.Prompt == "":
		return errors.New("the prompt is empty")
	case q.Ref != "" && !ValidRef(q.Ref):
		return errors.New("invalid reference")
	case !validPaths(q.Expect) || !validPaths(q.Forbid):
		return errors.New("invalid --expect or --forbid path (or more than 64)")
	case q.Setup != "" && !validCommand(q.Setup), q.Test != "" && !validCommand(q.Test):
		return errors.New("invalid command")
	case len(q.Checks) > MaxCheckLogs:
		return errors.New("too many checks")
	case q.Timeout <= 0 || q.CommandTimeout <= 0 || q.Grace < 0 || q.MaxPatchBytes <= 0:
		return errors.New("invalid time or size limit")
	}
	for _, c := range q.Checks {
		if !validCommand(c) {
			return errors.New("invalid command")
		}
	}
	return nil
}

// Delegate runs one task: a worktree, an agent in a box, and a verdict. The
// error is for failures before the run is recorded (bad input, no commit, bad
// reference); everything after is a verdict. The run row is written before
// anything is created and completed on every path out.
func Delegate(ctx context.Context, d DelegateDeps, req DelegateRequest) (DelegateOutcome, error) {
	if err := req.validate(); err != nil {
		return DelegateOutcome{}, err
	}
	ref := req.Ref
	if ref == "" {
		ref = "HEAD"
	}
	commit, err := d.Workspace.Resolve(ctx, ref)
	if err != nil {
		return DelegateOutcome{}, err
	}
	task := req.Task
	if task == "" {
		task = NoTask
	}
	now := d.Clock.Now()
	run := Run{Session: d.NewSession(now), Task: task, Adapter: req.Adapter, Model: req.Model, StartedAt: now, Base: commit}
	if err := d.Journal.Begin(ctx, run); err != nil {
		return DelegateOutcome{}, fmt.Errorf("record the run: %w", err)
	}
	g := &delegation{d: d, req: req, ctx: ctx, run: run}
	return g.execute(), nil
}

// delegation is the state of one run.
type delegation struct {
	d    DelegateDeps
	req  DelegateRequest
	ctx  context.Context
	run  Run
	w    Workdir
	box  Box
	made bool // the workspace exists

	reasons  []Reason
	warnings []string
	launches int
	tip      string
}

func (g *delegation) fail(r Reason) { g.reasons = append(g.reasons, r) }

func (g *delegation) warn(msg string) { g.warnings = append(g.warnings, msg) }

// bookkeeping is a context that outlives an interrupt, for the writes that
// complete the run.
func (g *delegation) bookkeeping() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(g.ctx), bookkeepingLimit)
}

func (g *delegation) execute() DelegateOutcome {
	defer g.cleanup()
	g.phases()
	return g.complete()
}

// cleanup drops the run lock and closes the box, whatever happened.
func (g *delegation) cleanup() {
	if g.box != nil {
		g.box.Close()
	}
	if g.made {
		g.d.Workspace.Release(g.w)
	}
}

func (g *delegation) phases() {
	if !g.createWorkspace() || !g.openBox() {
		return
	}
	g.launch()
	g.takeTip()
}

func (g *delegation) createWorkspace() bool {
	w, err := g.d.Workspace.Create(g.ctx, g.run.Session, g.run.Base)
	if err != nil {
		g.fail(ReasonWorkspaceFailed)
		return false
	}
	g.w, g.made = w, true
	bk, cancel := g.bookkeeping()
	defer cancel()
	if err := g.d.Journal.Created(bk, g.run.Session); err != nil {
		g.fail(ReasonWorkspaceFailed)
		return false
	}
	return true
}

func (g *delegation) openBox() bool {
	box, err := g.d.Sandbox.Open(g.ctx, BoxSpec{Dir: g.w.Dir, Env: g.req.BoxEnv})
	if err != nil {
		g.fail(ReasonSandboxFailed)
		return false
	}
	g.box = box
	return true
}

func (g *delegation) launch() {
	g.launches++
	res, err := g.d.Agent.Run(g.ctx, g.box, AgentRequest{Session: g.run.Session, Model: g.run.Model, Prompt: g.req.Prompt})
	if err != nil || res.Exit != 0 {
		g.fail(ReasonAgentFailed)
	}
}

// takeTip records where the branch points at the end of the run.
func (g *delegation) takeTip() {
	bk, cancel := g.bookkeeping()
	defer cancel()
	if st, err := g.d.Workspace.State(bk, g.w); err == nil {
		g.tip = st.Head
	}
}

// complete writes the final row. It runs before the lock is released.
func (g *delegation) complete() DelegateOutcome {
	bk, cancel := g.bookkeeping()
	defer cancel()
	now := g.d.Clock.Now()
	v := verdictOf(g.reasons)
	res := RunResult{
		Verdict: v, Exit: v.ExitCode(), FinishedAt: now, Launches: g.launches,
		Duration: max(0, now.Sub(g.run.StartedAt)), Tip: g.tip,
	}
	// A journal that cannot be written leaves the row running; the next
	// finish or clean completes it as interrupted.
	if err := g.d.Journal.Finish(bk, g.run.Session, res); err != nil {
		g.warn("the run record could not be completed: " + err.Error())
	}
	return DelegateOutcome{Session: g.run.Session, Result: res, Warnings: g.warnings}
}

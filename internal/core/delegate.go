package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Reason is one cause of a verdict, from a closed list (spec section 10).
type Reason string

const (
	ReasonWorkspaceFailed Reason = "workspace-failed"
	ReasonSandboxFailed   Reason = "sandbox-failed"
	ReasonSetupFailed     Reason = "setup-failed"
	ReasonAgentFailed     Reason = "agent-failed"
	ReasonAgentTimeout    Reason = "agent-timeout"
	ReasonQuota           Reason = "quota-exhausted"
	ReasonInterrupted     Reason = "interrupted"
	ReasonStray           Reason = "stray-files"
	ReasonMissing         Reason = "missing-files"
	ReasonForbidden       Reason = "forbidden-path"
	ReasonUnsafeLink      Reason = "unsafe-symlink"
	ReasonTestFailed      Reason = "test-failed"
	ReasonCheckFailed     Reason = "check-failed"
	ReasonPatchTooLarge   Reason = "patch-too-large"
)

// reasonVerdict says which verdict a reason leads to.
var reasonVerdict = map[Reason]RunVerdict{
	ReasonWorkspaceFailed: RunFailed,
	ReasonSandboxFailed:   RunFailed,
	ReasonSetupFailed:     RunFailed,
	ReasonAgentFailed:     RunFailed,
	ReasonAgentTimeout:    RunTimeout,
	ReasonQuota:           RunQuota,
	ReasonInterrupted:     RunInterrupted,
	ReasonStray:           RunStray,
	ReasonMissing:         RunFailed,
	ReasonForbidden:       RunFailed,
	ReasonUnsafeLink:      RunFailed,
	ReasonTestFailed:      RunFailed,
	ReasonCheckFailed:     RunFailed,
	ReasonPatchTooLarge:   RunFailed,
}

// errAgentTimeout is the cause of the agent phase's deadline.
var errAgentTimeout = errors.New("the agent ran out of time")

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
	Report  RunReport
	Text    string // report.txt
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

// Validate checks the request before anything is created.
func (q DelegateRequest) Validate() error {
	return firstError(q.validateNames(), q.validatePaths(), q.validateCommands(), q.validateLimits())
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (q DelegateRequest) validateNames() error {
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
	}
	return nil
}

func (q DelegateRequest) validatePaths() error {
	if !validPaths(q.Expect) || !validPaths(q.Forbid) {
		return errors.New("invalid --expect or --forbid path (or more than 64)")
	}
	return nil
}

func (q DelegateRequest) validateCommands() error {
	if len(q.Checks) > MaxCheckLogs {
		return errors.New("too many checks")
	}
	for _, c := range []string{q.Setup, q.Test} { // optional
		if c != "" && !validCommand(c) {
			return errors.New("invalid command")
		}
	}
	for _, c := range q.Checks {
		if !validCommand(c) {
			return errors.New("invalid command")
		}
	}
	return nil
}

func (q DelegateRequest) validateLimits() error {
	if q.Timeout <= 0 || q.CommandTimeout <= 0 || q.Grace < 0 || q.MaxPatchBytes <= 0 {
		return errors.New("invalid time or size limit")
	}
	return nil
}

// Delegate runs one task: a worktree, an agent in a box, and a verdict. The
// error is for failures before the run is recorded (bad input, no commit, bad
// reference); everything after is a verdict. The run row is written before
// anything is created and completed on every path out.
func Delegate(ctx context.Context, d DelegateDeps, req DelegateRequest) (DelegateOutcome, error) {
	if err := req.Validate(); err != nil {
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

	reasons       []Reason
	warnings      []string
	ignored       int // files present but ignored by git, as of the last read
	launches      int
	retries       int
	before, after State
	agentOK       bool // the last launch ended normally
	log           io.Writer
	agentStep     StepResult
	steps         []StepResult
	findings      Findings
	patchKept     bool
	usage         map[string]*usageSum
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
	if !g.createWorkspace() || !g.openBox() || !g.setup() || !g.agentPhase() {
		return
	}
	g.writePatch()
	if g.agentOK {
		g.verify()
	}
}

// StepResult is how one command of the run ended.
type StepResult struct {
	Name     string
	Exit     int
	Duration time.Duration
	TimedOut bool
}

// setup runs --setup and reports whether the agent may start.
func (g *delegation) setup() bool {
	if g.req.Setup == "" {
		return true
	}
	switch ok := g.command("setup", ArtSetupLog, g.req.Setup); {
	case g.ctx.Err() != nil:
		g.fail(ReasonInterrupted)
	case !ok:
		g.fail(ReasonSetupFailed)
	}
	return len(g.reasons) == 0
}

// verify runs --test then each --check, in this order and in the same box.
// A failure does not stop the next command; an interruption does.
func (g *delegation) verify() {
	type step struct {
		name, shell string
		log         ArtifactName
		reason      Reason
	}
	var steps []step
	if g.req.Test != "" {
		steps = append(steps, step{"test", g.req.Test, ArtTestLog, ReasonTestFailed})
	}
	for i, c := range g.req.Checks {
		steps = append(steps, step{fmt.Sprintf("check-%d", i+1), c, ArtCheckLog(i + 1), ReasonCheckFailed})
	}
	for _, s := range steps {
		ok := g.command(s.name, s.log, s.shell)
		if g.ctx.Err() != nil {
			g.fail(ReasonInterrupted)
			return
		}
		if !ok {
			g.fail(s.reason)
		}
	}
}

// command runs one shell command of the run under the command timeout, with
// its output in its own log, and reports whether it succeeded: exit 0 and not
// stopped.
func (g *delegation) command(name string, log ArtifactName, shell string) bool {
	out, closeLog := g.openLog(log)
	defer closeLog()
	ctx, cancel := context.WithTimeout(g.ctx, g.req.CommandTimeout)
	defer cancel()
	res, err := g.box.Exec(ctx, Cmd{Shell: shell, Out: out, Grace: g.req.Grace})
	g.steps = append(g.steps, StepResult{Name: name, Exit: res.Exit, Duration: res.Duration, TimedOut: res.Killed && g.ctx.Err() == nil})
	return err == nil && res.Exit == 0 && !res.Killed
}

// openLog creates a log file of the run; when that fails the output is
// dropped and a warning is kept.
func (g *delegation) openLog(name ArtifactName) (io.Writer, func()) {
	bk, cancel := g.bookkeeping()
	defer cancel()
	w, err := g.d.Artifacts.Create(bk, g.run.Session, name)
	if err != nil {
		g.warn("could not create " + string(name))
		return io.Discard, func() {}
	}
	return w, func() { w.Close() }
}

// writePatch saves the changes of the run, base to final state. A patch over
// the cap is not kept.
func (g *delegation) writePatch() {
	bk, cancel := g.bookkeeping()
	defer cancel()
	var buf bytes.Buffer
	switch err := g.d.Workspace.Diff(bk, g.w, g.after, &buf, g.req.MaxPatchBytes); {
	case errors.Is(err, ErrDiffTooLarge):
		g.fail(ReasonPatchTooLarge)
	case err != nil:
		g.fail(ReasonWorkspaceFailed)
	default:
		if err := g.d.Artifacts.Write(bk, g.run.Session, ArtPatch, buf.Bytes()); err != nil {
			g.warn("could not write " + string(ArtPatch))
		} else {
			g.patchKept = true
		}
	}
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

// agentPhase runs the agent, and once more in the same session when expected
// files are missing after a normal first launch. One deadline covers both. The
// worktree is then read once more and judged, whatever happened to the agent.
func (g *delegation) agentPhase() bool {
	var ok bool
	if g.before, ok = g.read(); !ok {
		return false
	}
	log, closeLog := g.openLog(ArtAgentLog)
	defer closeLog()
	g.log = log
	defer func() { g.steps = append(g.steps, g.agentStep) }()
	ctx, cancel := context.WithTimeoutCause(g.ctx, g.req.Timeout, errAgentTimeout)
	defer cancel()
	fresh := false // after reflects the end of the last launch
	if g.runAgent(ctx, g.req.Prompt, false) {
		if g.after, ok = g.read(); !ok {
			return false
		}
		fresh = true
		if missing := missingExpected(ChangedPaths(g.before, g.after), g.req.Expect); len(missing) > 0 {
			g.retries++
			g.runAgent(ctx, RetryPrompt(missing), true)
			fresh = false
		}
	}
	if !fresh {
		if g.after, ok = g.read(); !ok {
			return false
		}
	}
	g.judge()
	return true
}

// judge compares the final state with the limits of the task.
func (g *delegation) judge() {
	f := JudgeChanges(ChangedPaths(g.before, g.after), g.req.Expect, g.req.Forbid)
	g.findings = f
	for _, c := range []struct {
		found  bool
		reason Reason
	}{
		{len(f.ForbiddenTouched) > 0, ReasonForbidden},
		{len(f.UnsafeSymlinks) > 0, ReasonUnsafeLink},
		{g.agentOK && len(f.Missing) > 0, ReasonMissing},
		{len(f.Unexpected) > 0, ReasonStray},
	} {
		if c.found {
			g.fail(c.reason)
		}
	}
}

// read takes the state of the worktree; a failure ends the run.
func (g *delegation) read() (State, bool) {
	bk, cancel := g.bookkeeping()
	defer cancel()
	st, err := g.d.Workspace.State(bk, g.w)
	if err != nil {
		g.fail(ReasonWorkspaceFailed)
		return State{}, false
	}
	g.ignored = st.Ignored
	return st, true
}

// runAgent launches the agent once and reports whether it ended normally
// (exit 0, not stopped, no quota); otherwise it records why.
func (g *delegation) runAgent(ctx context.Context, prompt string, resume bool) bool {
	g.launches++
	g.agentOK = false
	res, err := g.d.Agent.Run(ctx, g.box, AgentRequest{Session: g.run.Session, Model: g.run.Model, Prompt: prompt, Resume: resume, Log: g.log, Grace: g.req.Grace})
	g.addUsage(res.Usage)
	g.agentStep.Name, g.agentStep.Exit = "agent", res.Exit
	g.agentStep.Duration += res.Duration
	g.agentStep.TimedOut = res.Killed && errors.Is(context.Cause(ctx), errAgentTimeout)
	switch {
	case err != nil:
		g.fail(ReasonAgentFailed)
	case res.Killed:
		g.fail(killedReason(ctx))
	case res.Quota:
		g.fail(ReasonQuota)
	case res.Exit != 0:
		g.fail(ReasonAgentFailed)
	default:
		g.agentOK = true
	}
	return g.agentOK
}

// RetryPrompt is the fixed text of the second launch. Only valid repository
// paths are named in it.
func RetryPrompt(missing []string) string {
	var b strings.Builder
	b.WriteString("These expected files are missing. Create or update them now and keep the rest of your work as it is:")
	for _, p := range missing {
		if ValidRepoPath(p) {
			b.WriteString("\n- " + p)
		}
	}
	return b.String()
}

// killedReason tells the watchdog (the phase's deadline) from an interruption
// (the parent context ended) for an agent that was stopped.
func killedReason(ctx context.Context) Reason {
	switch {
	case errors.Is(context.Cause(ctx), errAgentTimeout):
		return ReasonAgentTimeout
	case ctx.Err() != nil:
		return ReasonInterrupted
	}
	return ReasonAgentFailed // it stopped without being asked to
}

// complete writes the report, the usage rows and the final row, in this order.
// It runs before the lock is released.
func (g *delegation) complete() DelegateOutcome {
	bk, cancel := g.bookkeeping()
	defer cancel()
	now := g.d.Clock.Now()
	v := verdictOf(g.reasons)
	res := RunResult{
		Verdict: v, Exit: v.ExitCode(), FinishedAt: now, Launches: g.launches, Retries: g.retries,
		Duration: max(0, now.Sub(g.run.StartedAt)), Tip: g.after.Head,
	}
	if g.ignored > 0 {
		g.warn(fmt.Sprintf("%d file(s) ignored by git were not judged; check them in the worktree", g.ignored))
	}
	rep := g.report(res)
	g.writeReport(bk, rep)
	g.recordUsage(bk, now)
	// A journal that cannot be written leaves the row running; the next
	// finish or clean completes it as interrupted.
	if err := g.d.Journal.Finish(bk, g.run.Session, res); err != nil {
		g.warn("the run record could not be completed: " + err.Error())
	}
	return DelegateOutcome{Session: g.run.Session, Result: res, Report: rep, Text: rep.Text(""), Warnings: g.warnings}
}

// writeReport saves report.json and report.txt.
func (g *delegation) writeReport(ctx context.Context, rep RunReport) {
	js, err := json.MarshalIndent(rep, "", "  ")
	if err == nil {
		err = g.d.Artifacts.Write(ctx, g.run.Session, ArtReportJSON, append(js, '\n'))
	}
	if err != nil {
		g.warn("could not write " + string(ArtReportJSON))
	}
	if err := g.d.Artifacts.Write(ctx, g.run.Session, ArtReportTXT, []byte(rep.Text(""))); err != nil {
		g.warn("could not write " + string(ArtReportTXT))
	}
}

// recordUsage saves one usage row per model, marked as attributed to the task
// (the command line fixed the task). Nothing is saved when the agent reported
// no usage.
func (g *delegation) recordUsage(ctx context.Context, now time.Time) {
	var rows []UsageRow
	for _, m := range g.models() {
		u := g.usage[m]
		row := UsageRow{
			Key:      Key{Source: g.run.Adapter, Session: g.run.Session, Day: g.run.StartedAt.UTC().Format("2006-01-02"), Task: g.run.Task, Model: m},
			Messages: int64(g.launches), Marked: int64(g.launches), Tokens: u.tokens, CostReportedUSD: u.costPtr(),
		}
		if err := row.Validate(); err != nil {
			g.warn("usage of one model was not saved: " + err.Error())
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return
	}
	batch := Batch{Rows: rows, Sessions: []SessionInfo{{Source: g.run.Adapter, Session: g.run.Session, TaskSource: "none"}}}
	if err := g.d.Store.Merge(ctx, batch, SnapshotLog{At: now, Sessions: 1, Rows: len(rows)}); err != nil {
		g.warn("usage was not saved: " + err.Error())
	}
}

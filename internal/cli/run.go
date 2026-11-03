package cli

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/R0b-1ns/kanjo/internal/config"
	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/gitworktree"
	"github.com/R0b-1ns/kanjo/internal/nosandbox"
	"github.com/R0b-1ns/kanjo/internal/runfiles"
	"github.com/R0b-1ns/kanjo/internal/scriptagent"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

// delegatePorts are the three ports tests replace; the rest is always real.
type delegatePorts struct {
	Workspace core.Workspace
	Sandbox   core.Sandbox
	Agent     core.AgentRunner
}

// multiFlag collects a repeated option.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type runOpts struct {
	agent, model, promptFile, task, ref, setup, test, repo, script, dataDir string
	expect, forbid, checks                                                  multiFlag
	timeout                                                                 int
	given                                                                   map[string]bool // options present on the command line
}

func runFlags(fs *flag.FlagSet) *runOpts {
	o := &runOpts{}
	fs.StringVar(&o.agent, "agent", "", "agent adapter; only \"script\" exists (required)")
	fs.StringVar(&o.model, "model", "", "model id handed to the agent (required)")
	fs.StringVar(&o.promptFile, "prompt-file", "", "file holding the prompt (required)")
	fs.StringVar(&o.task, "task", "", "task label the usage is counted on (default: none)")
	fs.StringVar(&o.ref, "ref", "", "commit, branch or tag the worktree starts from (default: HEAD)")
	fs.Var(&o.expect, "expect", "repository-relative path the agent must create or change; repeatable, at most 64")
	fs.Var(&o.forbid, "forbid", "repository-relative path the agent must not touch, with everything under it; repeatable, at most 64")
	fs.StringVar(&o.setup, "setup", "", "shell command run in the worktree before the agent")
	fs.StringVar(&o.test, "test", "", "shell command run in the worktree after the agent")
	fs.Var(&o.checks, "check", "shell command run after --test; repeatable, at most 32")
	fs.IntVar(&o.timeout, "timeout", 0, "seconds the agent may run, both launches together (default: delegate.timeout_s from the config, 900)")
	fs.StringVar(&o.script, "script", "", "the script to run as agent; only with --agent script")
	fs.StringVar(&o.repo, "repo", "", "git working tree root (default: the current folder)")
	dataDirFlag(fs, &o.dataDir)
	return o
}

const noSandboxWarning = "warning: no sandbox: the agent and the commands run with your rights\n"

func runCmd(args []string, stdout, stderr io.Writer, e env) int {
	fs := newFlagSet("run", stderr)
	o := runFlags(fs)
	if code := parseFlags(fs, args); code != proceed {
		return code
	}
	o.given = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { o.given[f.Name] = true })
	if err := o.validate(e.ports != nil); err != nil {
		fmt.Fprintf(stderr, "kanjo run: %v\n", err)
		return 2
	}
	cfg, _, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	prompt, code := readPrompt(o.promptFile, e.cwd, cfg.Delegate.MaxPromptBytes, stderr)
	if code != proceed {
		return code
	}
	req := o.request(cfg, prompt)
	if err := req.Validate(); err != nil {
		fmt.Fprintf(stderr, "kanjo run: %v\n", err)
		return 2
	}
	repo := o.repo
	if repo == "" {
		repo = e.cwd
	}
	if !filepath.IsAbs(repo) {
		repo = filepath.Join(e.cwd, repo)
	}
	deps, closeStore, err := o.dependencies(cfg, repo, e)
	if err != nil {
		return fail(stderr, err)
	}
	defer closeStore()
	fmt.Fprint(stderr, noSandboxWarning)
	ctx, stop := signal.NotifyContext(e.context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out, err := core.Delegate(ctx, deps, req)
	if err != nil {
		return fail(stderr, runError(err))
	}
	printReport(stdout, stderr, cfg.DataDir, out)
	return out.Result.Verdict.ExitCode()
}

// validate checks the options before any file is read or created.
func (o *runOpts) validate(injected bool) error {
	switch {
	case o.agent != "script":
		return errors.New("--agent is required and must be \"script\"")
	case !core.ValidModel(o.model):
		return errors.New("--model is required and must be a valid model id")
	case o.promptFile == "":
		return errors.New("--prompt-file is required")
	case o.task != "" && (o.task == core.NoTask || !core.ValidTask(o.task)):
		return errors.New("--task must be a valid task label (letters, digits, . _ - /, at most 64)")
	case o.script == "" && !injected:
		return errors.New("--script is required with --agent script")
	case o.given["timeout"] && o.timeout <= 0:
		return errors.New("--timeout must be positive")
	case o.given["setup"] && o.setup == "", o.given["test"] && o.test == "":
		return errors.New("--setup and --test need a command")
	}
	return nil
}

// request builds the delegation from the options and the configuration.
func (o *runOpts) request(cfg config.Config, prompt string) core.DelegateRequest {
	d := cfg.Delegate
	timeout := time.Duration(d.TimeoutS) * time.Second
	if o.timeout > 0 {
		timeout = time.Duration(o.timeout) * time.Second
	}
	return core.DelegateRequest{
		Task: o.task, Adapter: o.agent, Model: o.model, Prompt: prompt, Ref: o.ref,
		Expect: o.expect, Forbid: o.forbid, Setup: o.setup, Test: o.test, Checks: o.checks,
		Timeout: timeout, CommandTimeout: time.Duration(d.CommandTimeoutS) * time.Second, Grace: time.Duration(d.KillGraceS) * time.Second,
		BoxEnv: boxEnv(d.EnvPass), MaxPatchBytes: d.MaxPatchBytes,
	}
}

// boxEnv is the whole environment of the agent and the commands: PATH, LANG and
// LC_ALL when set, and the variables the user listed in delegate.env_pass.
func boxEnv(pass []string) []string {
	var env []string
	seen := map[string]bool{}
	for _, name := range append([]string{"PATH", "LANG", "LC_ALL"}, pass...) {
		if v, ok := os.LookupEnv(name); ok && !seen[name] {
			seen[name] = true
			env = append(env, name+"="+v)
		}
	}
	return env
}

// readPrompt reads the prompt file, at most max bytes. A file that is empty or
// over the cap is a usage error; one that cannot be read is an error.
func readPrompt(path, cwd string, max int64, stderr io.Writer) (string, int) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fail(stderr, fmt.Errorf("prompt file: %w", err))
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "", fail(stderr, errors.New("the prompt file must be a regular file"))
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	switch {
	case err != nil:
		return "", fail(stderr, fmt.Errorf("prompt file: %w", err))
	case int64(len(b)) > max:
		fmt.Fprintf(stderr, "kanjo run: the prompt file is larger than delegate.max_prompt_bytes (%d)\n", max)
		return "", 2
	case len(strings.TrimSpace(string(b))) == 0:
		fmt.Fprintln(stderr, "kanjo run: the prompt file is empty")
		return "", 2
	}
	return string(b), proceed
}

// dependencies wires the real adapters; tests replace the workspace, sandbox
// and agent. The returned function closes the store.
func (o *runOpts) dependencies(cfg config.Config, repo string, e env) (core.DelegateDeps, func(), error) {
	p := e.ports
	if p == nil {
		if fi, err := os.Lstat(filepath.Join(repo, ".git")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
			return core.DelegateDeps{}, nil, fmt.Errorf("%s is not a git working tree root (use --repo)", repo)
		}
		agent, err := scriptagent.New(o.script, filepath.Join(cfg.DataDir, "worktrees"))
		if err != nil {
			return core.DelegateDeps{}, nil, err
		}
		p = &delegatePorts{Workspace: gitworktree.New(repo, cfg.DataDir), Sandbox: nosandbox.Sandbox{}, Agent: agent}
	}
	st, err := sqlitestore.Open(cfg.DataDir)
	if err != nil {
		return core.DelegateDeps{}, nil, err
	}
	return core.DelegateDeps{
		Journal: st, Workspace: p.Workspace, Sandbox: p.Sandbox, Agent: p.Agent,
		Artifacts: runfiles.New(cfg.DataDir, cfg.Delegate.MaxLogBytes), Store: st, Clock: e.clock, NewSession: newSession,
	}, func() { st.Close() }, nil
}

// newSession is a session id from the clock and 24 random bits.
func newSession(now time.Time) string {
	var rnd [3]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		panic("kanjo: no randomness: " + err.Error())
	}
	return core.NewSessionID(now, rnd)
}

// runError words the errors that stop a run before it is recorded.
func runError(err error) error {
	switch {
	case errors.Is(err, core.ErrNoCommit):
		return errors.New("the repository has no commit: commit once, or pass --ref")
	case errors.Is(err, core.ErrBadRef):
		return errors.New("--ref is not a commit of this repository")
	}
	return err
}

// printReport prints report.txt with absolute paths, one printable line at a
// time, and the warnings on stderr.
func printReport(stdout, stderr io.Writer, dataDir string, out core.DelegateOutcome) {
	for _, line := range strings.Split(strings.TrimRight(out.Report.Text(dataDir), "\n"), "\n") {
		fmt.Fprintln(stdout, printable(line))
	}
	for _, w := range out.Warnings {
		fmt.Fprintf(stderr, "warning: %s\n", printable(w))
	}
}

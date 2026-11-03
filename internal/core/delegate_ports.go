package core

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrDuplicate means a run with that session already exists.
	ErrDuplicate = errors.New("a run with that session already exists")
	// ErrNoRun means the journal has no run with that session.
	ErrNoRun = errors.New("no such run")
	// ErrNotRunning means the run is already final.
	ErrNotRunning = errors.New("the run is not running")
	// ErrJudged means the run already has a lead verdict.
	ErrJudged = errors.New("the run already has a lead verdict")
	// ErrNoCommit means the repository has no commit to start from.
	ErrNoCommit = errors.New("the repository has no commit")
	// ErrBadRef means the reference is not a commit of the repository.
	ErrBadRef = errors.New("the reference is not a commit")
	// ErrExists means a worktree or branch for that session already exists.
	ErrExists = errors.New("a worktree or branch for that session already exists")
	// ErrBusy means a run holds the lock of the worktree.
	ErrBusy = errors.New("the run is still alive")
	// ErrMoved means the branch tip is not the one the journal recorded.
	ErrMoved = errors.New("the branch moved since the run ended")
	// ErrNotOurs means the folder or branch is not one a run created.
	ErrNotOurs = errors.New("not a worktree this tool created")
	// ErrDiffTooLarge means the diff is over the byte cap.
	ErrDiffTooLarge = errors.New("the diff is over the size cap")
	// ErrArtifactExists means that file of the run was already created.
	ErrArtifactExists = errors.New("that run file already exists")
	// ErrBadArtifact means the session id or the file name is not allowed.
	ErrBadArtifact = errors.New("invalid run file name")
	// ErrBoxClosed means the box was closed and runs nothing any more.
	ErrBoxClosed = errors.New("the box is closed")
)

// RunJournal is the append-only journal of runs and lead verdicts.
type RunJournal interface {
	// Begin records a run as running. ErrDuplicate if the session exists.
	Begin(ctx context.Context, r Run) error
	// Created marks that the run's workspace was created. ErrNoRun if unknown.
	Created(ctx context.Context, session string) error
	// Finish completes a running run, once. ErrNoRun, ErrNotRunning; an
	// invalid or running result is refused.
	Finish(ctx context.Context, session string, r RunResult) error
	// Judge records the lead's verdict, once per run. ErrNoRun if the run is
	// unknown. When the run already has a verdict it returns that verdict and
	// ErrJudged and writes nothing.
	Judge(ctx context.Context, l Lead) (existing LeadVerdict, err error)
	// Runs returns every run in start order, each with its lead if any.
	Runs(ctx context.Context) ([]RunRecord, error)
}

// Sandbox opens an execution environment for one working folder.
type Sandbox interface {
	// Open refuses an empty or relative Dir.
	Open(ctx context.Context, spec BoxSpec) (Box, error)
}

// BoxSpec describes a box. Env is the whole environment of what runs in it:
// nothing is inherited from the caller (an adapter may add private HOME and
// TMPDIR folders).
type BoxSpec struct {
	Dir string
	Env []string
}

// Box runs commands in a working folder.
type Box interface {
	// Exec runs one command and waits for it. When ctx ends, the command and
	// its children are stopped (politely, then by force after Cmd.Grace) and
	// the result says Killed. A command that fails or is killed is a result,
	// not an error; the error is for a command that could not start or a
	// closed box (ErrBoxClosed).
	Exec(ctx context.Context, cmd Cmd) (ExecResult, error)
	Close() error
}

// Cmd is one command: exactly one of Argv and Shell (run by /bin/sh -c).
type Cmd struct {
	Argv  []string
	Shell string
	Stdin io.Reader
	Out   io.Writer // stdout and stderr, written by the adapter; the caller caps it
	Grace time.Duration
}

// ExecResult is how a command ended.
type ExecResult struct {
	Exit     int
	Killed   bool // ctx ended it
	Duration time.Duration
}

// Workspace owns the disposable worktrees of runs.
type Workspace interface {
	// Resolve turns a reference into a commit id. ErrNoCommit when the
	// repository has no commit, ErrBadRef when ref is not a commit.
	Resolve(ctx context.Context, ref string) (commit string, err error)
	// Create makes the worktree and branch of a session at commit and takes the
	// run lock, held until Release. ErrExists when either already exists;
	// nothing is then changed.
	Create(ctx context.Context, session, commit string) (Workdir, error)
	// State is the content of the worktree (files not ignored by git). It never
	// follows a symbolic link and never changes the repository's own index.
	State(ctx context.Context, w Workdir) (State, error)
	// Diff writes the changes from the base commit to the state, new, changed,
	// deleted and binary files included. ErrDiffTooLarge past max bytes.
	Diff(ctx context.Context, w Workdir, to State, dst io.Writer, max int64) error
	// Release drops the run lock. Safe to call twice.
	Release(w Workdir) error
	// Inspect says what exists of an owned workspace. ErrNotOurs when the
	// folder is not a worktree this tool created.
	Inspect(ctx context.Context, o Owned) (Presence, error)
	// Remove deletes the worktree, its branch and its lock file, and nothing
	// else. ErrNotOurs, ErrBusy (lock held), ErrMoved (branch tip is not
	// o.Tip) delete nothing. Anything already absent is skipped: removing
	// twice succeeds.
	Remove(ctx context.Context, o Owned) error
}

// Workdir is a created worktree. Dir is opaque to the core.
type Workdir struct {
	Session, Dir, Branch, Base string
}

// Entry is one file of a State: Mode is the git mode ("100644", "100755",
// "120000" for a link); Blob identifies the content; Link is the target of a
// link and empty otherwise.
type Entry struct {
	Path, Mode, Blob, Link string
}

// State is the content of a worktree at an instant. Equal Trees mean equal
// content. Head is the commit the branch points to.
type State struct {
	Tree    string
	Head    string
	Entries []Entry
}

// Owned names what the journal recorded about a run: nothing else may be
// inspected or removed. Branch and Dir must equal BranchOf and DirOf of the
// session; Tip is the branch tip recorded at the end of the run.
type Owned struct {
	Session, Branch, Dir, Tip string
}

// Presence says what exists of an Owned workspace.
type Presence struct {
	DirExists, BranchExists, Busy, TipMatches bool
}

// ArtifactName is one of the closed set of files of a run.
type ArtifactName string

const (
	ArtReportJSON ArtifactName = "report.json"
	ArtReportTXT  ArtifactName = "report.txt"
	ArtPatch      ArtifactName = "changes.patch"
	ArtAgentLog   ArtifactName = "agent.log"
	ArtSetupLog   ArtifactName = "setup.log"
	ArtTestLog    ArtifactName = "test.log"

	// MaxCheckLogs is how many check-N.log files a run may have.
	MaxCheckLogs = 32
)

// ArtCheckLog is the log of the nth --check, numbered from 1.
func ArtCheckLog(n int) ArtifactName { return ArtifactName("check-" + strconv.Itoa(n) + ".log") }

// Valid reports whether n is in the closed set.
func (n ArtifactName) Valid() bool {
	switch n {
	case ArtReportJSON, ArtReportTXT, ArtPatch, ArtAgentLog, ArtSetupLog, ArtTestLog:
		return true
	}
	num, ok := strings.CutPrefix(string(n), "check-")
	num, ok2 := strings.CutSuffix(num, ".log")
	i, err := strconv.Atoi(num)
	return ok && ok2 && err == nil && i >= 1 && i <= MaxCheckLogs && strconv.Itoa(i) == num
}

// Artifacts creates the files of one run (runs/<session>/).
type Artifacts interface {
	// Create makes a new file for streaming; ErrArtifactExists if the name was
	// used, ErrBadArtifact for a session id or name that is not allowed.
	Create(ctx context.Context, session string, name ArtifactName) (io.WriteCloser, error)
	// Write makes a new file with data, whole or not at all.
	Write(ctx context.Context, session string, name ArtifactName, data []byte) error
}

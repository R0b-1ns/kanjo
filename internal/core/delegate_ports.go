package core

import (
	"context"
	"errors"
	"io"
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

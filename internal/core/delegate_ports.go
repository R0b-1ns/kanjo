package core

import (
	"context"
	"errors"
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

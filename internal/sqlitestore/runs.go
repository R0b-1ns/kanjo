package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// formatTime stores a time as UTC with millisecond precision.
func formatTime(t time.Time) string { return t.UTC().Format(core.MarkerTimeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(core.MarkerTimeLayout, s) }

// Begin implements core.RunJournal.
func (s *Store) Begin(ctx context.Context, r core.Run) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return s.immediate(ctx, func(conn *sql.Conn) error {
		if _, err := runState(ctx, conn, r.Session); err == nil {
			return core.ErrDuplicate
		} else if !errors.Is(err, core.ErrNoRun) {
			return err
		}
		_, err := conn.ExecContext(ctx,
			"INSERT INTO runs(session, task, adapter, model, started_at, verdict, base, branch, dir) VALUES(?,?,?,?,?,?,?,?,?)",
			r.Session, r.Task, r.Adapter, r.Model, formatTime(r.StartedAt), string(core.RunRunning), r.Base,
			core.BranchOf(r.Session), core.DirOf(r.Session))
		return err
	})
}

// runState returns the verdict of a run, or core.ErrNoRun.
func runState(ctx context.Context, conn *sql.Conn, session string) (core.RunVerdict, error) {
	var v string
	err := conn.QueryRowContext(ctx, "SELECT verdict FROM runs WHERE session = ?", session).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", core.ErrNoRun
	}
	return core.RunVerdict(v), err
}

// Created implements core.RunJournal.
func (s *Store) Created(ctx context.Context, session string) error {
	return s.immediate(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "UPDATE runs SET created = 1 WHERE session = ?", session)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(err, core.ErrNoRun)
		}
		return nil
	})
}

// Finish implements core.RunJournal. It is the only update of a run's result
// and only while the run is running.
func (s *Store) Finish(ctx context.Context, session string, r core.RunResult) error {
	if err := r.ValidateFinal(); err != nil {
		return err
	}
	return s.immediate(ctx, func(conn *sql.Conn) error {
		v, err := runState(ctx, conn, session)
		switch {
		case err != nil:
			return err
		case v != core.RunRunning:
			return core.ErrNotRunning
		}
		var tip any
		if r.Tip != "" {
			tip = r.Tip
		}
		_, err = conn.ExecContext(ctx,
			"UPDATE runs SET verdict = ?, exit_code = ?, finished_at = ?, launches = ?, retries = ?, duration_ms = ?, tip = ? WHERE session = ? AND verdict = 'running'",
			string(r.Verdict), r.Exit, formatTime(r.FinishedAt), r.Launches, r.Retries, r.Duration.Milliseconds(), tip, session)
		return err
	})
}

// Judge implements core.RunJournal.
func (s *Store) Judge(ctx context.Context, l core.Lead) (existing core.LeadVerdict, err error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	err = s.immediate(ctx, func(conn *sql.Conn) error {
		if _, err := runState(ctx, conn, l.Session); err != nil {
			return err
		}
		var v string
		switch err := conn.QueryRowContext(ctx, "SELECT verdict FROM leads WHERE session = ?", l.Session).Scan(&v); {
		case err == nil:
			existing = core.LeadVerdict(v)
			return core.ErrJudged
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err := conn.ExecContext(ctx, "INSERT INTO leads(at, session, verdict) VALUES(?,?,?)",
			formatTime(l.At), l.Session, string(l.Verdict))
		return err
	})
	return existing, err
}

const runCols = `r.id, r.session, r.task, r.adapter, r.model, r.started_at, r.finished_at, r.verdict, r.exit_code,
  r.launches, r.retries, r.duration_ms, r.base, r.tip, r.branch, r.dir, r.created, l.at, l.verdict`

// Runs implements core.RunJournal. A database from before schema version 3 has
// no runs. Every row is validated again; a bad row fails the whole read and
// the error never repeats a stored value.
func (s *Store) Runs(ctx context.Context) ([]core.RunRecord, error) {
	if s.version < 3 {
		return []core.RunRecord{}, nil
	}
	rs, err := s.db.QueryContext(ctx, "SELECT "+runCols+" FROM runs r LEFT JOIN leads l ON l.session = r.session ORDER BY r.started_at, r.id")
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []core.RunRecord{}
	for rs.Next() {
		rec, err := scanRun(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rs.Err()
}

// runRow is a row of runs joined with its lead, as stored.
type runRow struct {
	id, durMS               int64
	session, task, adapter  string
	model, started, verdict string
	finished, tip, leadAt   sql.NullString
	leadVerdict             sql.NullString
	exit                    sql.NullInt64
	launches, retries       int
	base, branch, dir       string
	created                 int
}

func scanRun(rs *sql.Rows) (core.RunRecord, error) {
	var r runRow
	if err := rs.Scan(&r.id, &r.session, &r.task, &r.adapter, &r.model, &r.started, &r.finished, &r.verdict, &r.exit,
		&r.launches, &r.retries, &r.durMS, &r.base, &r.tip, &r.branch, &r.dir, &r.created, &r.leadAt, &r.leadVerdict); err != nil {
		return core.RunRecord{}, err
	}
	return r.record()
}

// record re-validates a stored row; an error never repeats a stored value.
func (r runRow) record() (core.RunRecord, error) {
	rec := core.RunRecord{Run: core.Run{Session: r.session, Task: r.task, Adapter: r.adapter, Model: r.model, Base: r.base}}
	bad := func(what string) error { return fmt.Errorf("stored run #%d %s", r.id, what) }
	var err error
	if rec.StartedAt, err = parseTime(r.started); err != nil {
		return rec, bad("has an invalid time")
	}
	if r.finished.Valid {
		if rec.FinishedAt, err = parseTime(r.finished.String); err != nil {
			return rec, bad("has an invalid time")
		}
	}
	rec.Verdict, rec.Exit, rec.Duration = core.RunVerdict(r.verdict), int(r.exit.Int64), time.Duration(r.durMS)*time.Millisecond
	rec.Launches, rec.Retries, rec.Tip, rec.Created = r.launches, r.retries, r.tip.String, r.created == 1
	if r.branch != core.BranchOf(r.session) || r.dir != core.DirOf(r.session) {
		return rec, bad("names a branch or folder that is not derived from its session")
	}
	if r.leadVerdict.Valid {
		l := core.Lead{Session: r.session, Verdict: core.LeadVerdict(r.leadVerdict.String)}
		if l.At, err = parseTime(r.leadAt.String); err != nil {
			return rec, bad("has a lead verdict with an invalid time")
		}
		rec.Lead = &l
	}
	if err := rec.Validate(); err != nil {
		return rec, fmt.Errorf("stored run #%d is invalid: %w", r.id, err)
	}
	return rec, nil
}

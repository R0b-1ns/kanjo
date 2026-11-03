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

func scanRun(rs *sql.Rows) (core.RunRecord, error) {
	var (
		rec                   core.RunRecord
		id, durMS             int64
		started, verdict      string
		finished, tip, leadAt sql.NullString
		leadVerdict           sql.NullString
		exit                  sql.NullInt64
		branch, dir           string
		created               int
	)
	if err := rs.Scan(&id, &rec.Session, &rec.Task, &rec.Adapter, &rec.Model, &started, &finished, &verdict, &exit,
		&rec.Launches, &rec.Retries, &durMS, &rec.Base, &tip, &branch, &dir, &created, &leadAt, &leadVerdict); err != nil {
		return rec, err
	}
	bad := func(what string) error { return fmt.Errorf("stored run #%d %s", id, what) }
	var err error
	if rec.StartedAt, err = time.Parse(core.MarkerTimeLayout, started); err != nil {
		return rec, bad("has an invalid time")
	}
	if finished.Valid {
		if rec.FinishedAt, err = time.Parse(core.MarkerTimeLayout, finished.String); err != nil {
			return rec, bad("has an invalid time")
		}
	}
	rec.Verdict, rec.Exit, rec.Duration = core.RunVerdict(verdict), int(exit.Int64), time.Duration(durMS)*time.Millisecond
	rec.Tip, rec.Created = tip.String, created == 1
	if branch != core.BranchOf(rec.Session) || dir != core.DirOf(rec.Session) {
		return rec, bad("names a branch or folder that is not derived from its session")
	}
	if leadVerdict.Valid {
		l := core.Lead{Session: rec.Session, Verdict: core.LeadVerdict(leadVerdict.String)}
		if l.At, err = time.Parse(core.MarkerTimeLayout, leadAt.String); err != nil {
			return rec, bad("has a lead verdict with an invalid time")
		}
		rec.Lead = &l
	}
	if err := rec.Validate(); err != nil {
		return rec, fmt.Errorf("stored run #%d is invalid: %w", id, err)
	}
	return rec, nil
}

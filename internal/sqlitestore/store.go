// Package sqlitestore is the SQLite adapter of core.Store.
package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registers "sqlite"

	"github.com/R0b-1ns/kanjo/internal/core"
)

const (
	schemaVersion = 1
	dbFile        = "kanjo.db"
)

const schema = `
CREATE TABLE IF NOT EXISTS usage(
  source TEXT NOT NULL, session TEXT NOT NULL, day TEXT NOT NULL, task TEXT NOT NULL,
  model TEXT NOT NULL, long_context INTEGER NOT NULL,
  messages INTEGER NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
  reasoning INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL,
  cache_read INTEGER NOT NULL, cost_reported_usd REAL,
  PRIMARY KEY(source, session, day, task, model, long_context));
CREATE TABLE IF NOT EXISTS sessions(
  source TEXT NOT NULL, session TEXT NOT NULL, task_source TEXT NOT NULL, labels_seen INTEGER NOT NULL,
  PRIMARY KEY(source, session));
CREATE TABLE IF NOT EXISTS snapshots(
  at TEXT NOT NULL, sessions INTEGER NOT NULL, rows INTEGER NOT NULL, skipped INTEGER NOT NULL);
`

// slotMatch selects the stored rows of one (source, session, day, model, tier),
// whatever their task.
const slotMatch = "source = ? AND session = ? AND day = ? AND model = ? AND long_context = ?"

const usageCols = `source, session, day, task, model, long_context, messages, input, output,
  reasoning, cache_write_5m, cache_write_1h, cache_read, cost_reported_usd`

// Store is a core.Store backed by <dir>/kanjo.db.
type Store struct{ db *sql.DB }

// Open creates the data directory (0700, with a .gitignore that ignores
// everything) and the database file (0600) if needed, and migrates the schema.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", dir, err)
	}
	if err := writeGitignore(dir); err != nil {
		return nil, fmt.Errorf("write %s: %w", filepath.Join(dir, ".gitignore"), err)
	}
	path := Path(dir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // private from the first byte
	if err != nil {
		return nil, fmt.Errorf("create database %s: %w", path, err)
	}
	f.Close()
	u := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("database %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// writeGitignore keeps usage data out of git even when the folder is inside a
// repository. An existing file is left alone.
func writeGitignore(dir string) error {
	p := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return os.WriteFile(p, []byte("*\n"), 0o644)
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("schema version %d is newer than this kanjo supports (%d); update kanjo", v, schemaVersion)
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	_, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

type stmt struct {
	query string
	args  []any
}

// Merge implements core.Store.
func (s *Store) Merge(ctx context.Context, b core.Batch, log core.SnapshotLog) error {
	var stmts []stmt
	for _, r := range b.Rows {
		slot := []any{r.Source, r.Session, r.Day, r.Model, r.LongContext}
		// Saved rows of the slot with at most as many messages are superseded;
		// a saved row with more survives and the incoming row is dropped.
		stmts = append(stmts,
			stmt{"DELETE FROM usage WHERE " + slotMatch + " AND messages <= ?", append(slot, r.Messages)},
			stmt{"INSERT OR REPLACE INTO usage(" + usageCols + ") SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM usage WHERE " + slotMatch + ")",
				append([]any{r.Source, r.Session, r.Day, r.Task, r.Model, r.LongContext, r.Messages, r.Input, r.Output,
					r.Reasoning, r.CacheWrite5m, r.CacheWrite1h, r.CacheRead, r.CostReportedUSD}, slot...)})
	}
	for _, ses := range b.Sessions {
		stmts = append(stmts, stmt{"INSERT OR REPLACE INTO sessions VALUES(?,?,?,?)",
			[]any{ses.Source, ses.Session, ses.TaskSource, ses.LabelsSeen}})
	}
	stmts = append(stmts, stmt{"INSERT INTO snapshots VALUES(?,?,?,?)",
		[]any{log.At.UTC().Format(time.RFC3339), log.Sessions, log.Rows, log.Skipped}})
	return s.inTx(ctx, stmts)
}

// inTx runs the statements in one transaction: all or nothing.
func (s *Store) inTx(ctx context.Context, stmts []stmt) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.query, st.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Rows implements core.Store. Every stored row is validated again, so a
// tampered database is an error rather than bad data.
func (s *Store) Rows(ctx context.Context) ([]core.UsageRow, error) {
	rs, err := s.db.QueryContext(ctx, "SELECT "+usageCols+" FROM usage ORDER BY source, session, day, task, model, long_context")
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	rows := []core.UsageRow{}
	for rs.Next() {
		var r core.UsageRow
		var cost sql.NullFloat64
		if err := rs.Scan(&r.Source, &r.Session, &r.Day, &r.Task, &r.Model, &r.LongContext, &r.Messages,
			&r.Input, &r.Output, &r.Reasoning, &r.CacheWrite5m, &r.CacheWrite1h, &r.CacheRead, &cost); err != nil {
			return nil, err
		}
		if cost.Valid {
			r.CostReportedUSD = &cost.Float64
		}
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("stored usage row is invalid: %w", err)
		}
		rows = append(rows, r)
	}
	return rows, rs.Err()
}

// Sessions implements core.Store.
func (s *Store) Sessions(ctx context.Context) ([]core.SessionInfo, error) {
	rs, err := s.db.QueryContext(ctx, "SELECT source, session, task_source, labels_seen FROM sessions ORDER BY source, session")
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []core.SessionInfo{}
	for rs.Next() {
		var si core.SessionInfo
		if err := rs.Scan(&si.Source, &si.Session, &si.TaskSource, &si.LabelsSeen); err != nil {
			return nil, err
		}
		if err := si.Validate(); err != nil {
			return nil, fmt.Errorf("stored session is invalid: %w", err)
		}
		out = append(out, si)
	}
	return out, rs.Err()
}

// Path is the database file of a data folder.
func Path(dir string) string { return filepath.Join(dir, dbFile) }

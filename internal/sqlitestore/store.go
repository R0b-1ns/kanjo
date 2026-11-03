// Package sqlitestore is the SQLite adapter of core.Store.
package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
		return nil, err
	}
	path := Path(dir)
	if err := refuseLink(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // private from the first byte
	if err != nil {
		return nil, fmt.Errorf("create database %s: %w", path, err)
	}
	f.Close()
	db, err := openDB(path, "")
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("database %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// openDB opens the SQLite file at path; extra is URI query text put before the
// common options.
func openDB(path, extra string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: extra + "_pragma=trusted_schema(0)&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return db, nil
}

// OpenReadOnly opens an existing database for reading only. It creates no
// folder or file, runs no DDL and writes nothing, so it works on a read-only
// folder and never waits behind a writer for schema work. A missing database
// is an error that wraps fs.ErrNotExist.
func OpenReadOnly(dir string) (*Store, error) {
	path := Path(dir)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("database %s: %w", path, err)
	}
	if err := refuseLink(path); err != nil {
		return nil, err
	}
	db, err := openDB(path, "mode=ro&")
	if err != nil {
		return nil, err
	}
	v, err := version(db)
	if err == nil && v == 0 {
		err = errors.New("not a kanjo database: no schema")
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("database %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// writeGitignore keeps usage data out of git even when the folder is inside a
// repository. A missing file is created with "*"; an existing one is kept only
// if it still ignores everything, because a committed .gitignore could
// otherwise make the database trackable.
func writeGitignore(dir string) error {
	p := filepath.Join(dir, ".gitignore")
	if err := refuseLink(p); err != nil {
		return err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(p, []byte("*\n"), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	if !ignoresEverything(string(data)) {
		return fmt.Errorf("%s does not ignore everything, so usage data could be committed: remove it or make it a line \"*\" with no \"!\" rules", p)
	}
	return nil
}

func ignoresEverything(gitignore string) bool {
	star := false
	for _, line := range strings.Split(gitignore, "\n") {
		line = strings.TrimSpace(line)
		star = star || line == "*"
		if strings.HasPrefix(line, "!") {
			return false
		}
	}
	return star
}

// refuseLink fails when path is a symbolic link: a link committed inside a
// data folder could redirect writes anywhere.
func refuseLink(path string) error {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link; refusing to follow it", path)
	}
	return nil
}

// version reads the schema version and refuses one from a newer kanjo.
func version(db *sql.DB) (v int, err error) {
	if err = db.QueryRow("PRAGMA user_version").Scan(&v); err == nil && v > schemaVersion {
		err = fmt.Errorf("schema version %d is newer than this kanjo supports (%d); update kanjo", v, schemaVersion)
	}
	return v, err
}

func migrate(db *sql.DB) error {
	if _, err := version(db); err != nil {
		return err
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

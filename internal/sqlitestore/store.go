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
	schemaVersion = 2
	dbFile        = "kanjo.db"
)

const schema = `
CREATE TABLE IF NOT EXISTS usage(
  source TEXT NOT NULL, session TEXT NOT NULL, day TEXT NOT NULL, task TEXT NOT NULL,
  model TEXT NOT NULL, long_context INTEGER NOT NULL,
  messages INTEGER NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
  reasoning INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL,
  cache_read INTEGER NOT NULL, cost_reported_usd REAL,
  marked INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(source, session, day, task, model, long_context));
CREATE TABLE IF NOT EXISTS sessions(
  source TEXT NOT NULL, session TEXT NOT NULL, task_source TEXT NOT NULL, labels_seen INTEGER NOT NULL,
  PRIMARY KEY(source, session));
CREATE TABLE IF NOT EXISTS snapshots(
  at TEXT NOT NULL, sessions INTEGER NOT NULL, rows INTEGER NOT NULL, skipped INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS markers(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('start','end')),
  session TEXT NOT NULL,
  task TEXT,
  CHECK((kind = 'start') = (task IS NOT NULL)));
CREATE INDEX IF NOT EXISTS markers_scope ON markers(session, at, id);
`

// addMarked upgrades a version 1 usage table.
const addMarked = "ALTER TABLE usage ADD COLUMN marked INTEGER NOT NULL DEFAULT 0"

// slotMatch selects the stored rows of one (source, session, day, model, tier),
// whatever their task.
const slotMatch = "source = ? AND session = ? AND day = ? AND model = ? AND long_context = ?"

const usageCols = `source, session, day, task, model, long_context, messages, input, output,
  reasoning, cache_write_5m, cache_write_1h, cache_read, cost_reported_usd, marked`

// Store is a core.Store backed by <dir>/kanjo.db.
type Store struct {
	db      *sql.DB
	version int // schema version of the file as opened
}

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
	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("database %s: %w", path, err)
	}
	return &Store{db: db, version: schemaVersion}, nil
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
	return &Store{db: db, version: v}, nil
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
	if err = db.QueryRow("PRAGMA user_version").Scan(&v); err == nil {
		err = checkVersion(v)
	}
	return v, err
}

// checkVersion refuses a schema from a newer kanjo.
func checkVersion(v int) error {
	if v > schemaVersion {
		return fmt.Errorf("schema version %d is newer than this kanjo supports (%d); update kanjo", v, schemaVersion)
	}
	return nil
}

// migrate brings the file to schemaVersion in one immediate transaction: the
// version is read after the write lock is taken, so two kanjo processes opening
// a version 1 file cannot both upgrade it.
func migrate(ctx context.Context, db *sql.DB) error {
	return (&Store{db: db}).immediate(ctx, func(conn *sql.Conn) error {
		var v int
		if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		if err := checkVersion(v); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, schema); err != nil {
			return err
		}
		if v == 1 {
			if _, err := conn.ExecContext(ctx, addMarked); err != nil {
				return err
			}
		}
		_, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
		return err
	})
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// immediate runs fn on one connection inside a BEGIN IMMEDIATE transaction:
// the write lock is taken before anything is read, so concurrent writers
// (other kanjo processes included) are serialized. An error rolls back.
func (s *Store) immediate(ctx context.Context, fn func(conn *sql.Conn) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()
	if err = fn(conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

// Merge implements core.Store.
func (s *Store) Merge(ctx context.Context, b core.Batch, log core.SnapshotLog) error {
	return s.immediate(ctx, func(conn *sql.Conn) error {
		for _, g := range groupBySlot(b.Rows) {
			if err := mergeSlot(ctx, conn, g); err != nil {
				return err
			}
		}
		for _, ses := range b.Sessions {
			if _, err := conn.ExecContext(ctx, "INSERT OR REPLACE INTO sessions VALUES(?,?,?,?)",
				ses.Source, ses.Session, ses.TaskSource, ses.LabelsSeen); err != nil {
				return err
			}
		}
		_, err := conn.ExecContext(ctx, "INSERT INTO snapshots VALUES(?,?,?,?)",
			log.At.UTC().Format(time.RFC3339), log.Sessions, log.Rows, log.Skipped)
		return err
	})
}

// slotRows are the incoming rows of one slot.
type slotRows struct {
	args  []any // source, session, day, model, long_context
	rows  []core.UsageRow
	total int64
}

// groupBySlot groups rows by slot, in order of first appearance.
func groupBySlot(rows []core.UsageRow) []*slotRows {
	var out []*slotRows
	byKey := map[[5]any]*slotRows{}
	for _, r := range rows {
		k := [5]any{r.Source, r.Session, r.Day, r.Model, r.LongContext}
		g := byKey[k]
		if g == nil {
			g = &slotRows{args: k[:]}
			byKey[k] = g
			out = append(out, g)
		}
		g.rows = append(g.rows, r)
		g.total += r.Messages
	}
	return out
}

// mergeSlot replaces the stored rows of a slot by the incoming ones, unless
// the stored ones count more messages in total.
func mergeSlot(ctx context.Context, conn *sql.Conn, g *slotRows) error {
	var stored int64
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(SUM(messages), 0) FROM usage WHERE "+slotMatch, g.args...).Scan(&stored); err != nil {
		return err
	}
	if stored > g.total {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "DELETE FROM usage WHERE "+slotMatch, g.args...); err != nil {
		return err
	}
	for _, r := range g.rows {
		if _, err := conn.ExecContext(ctx, "INSERT INTO usage("+usageCols+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			r.Source, r.Session, r.Day, r.Task, r.Model, r.LongContext, r.Messages, r.Input, r.Output,
			r.Reasoning, r.CacheWrite5m, r.CacheWrite1h, r.CacheRead, r.CostReportedUSD, r.Marked); err != nil {
			return err
		}
	}
	return nil
}

// Rows implements core.Store. Every stored row is validated again, so a
// tampered database is an error rather than bad data.
func (s *Store) Rows(ctx context.Context) ([]core.UsageRow, error) {
	cols := usageCols
	if s.version < 2 {
		cols = strings.TrimSuffix(usageCols, "marked") + "0"
	}
	rs, err := s.db.QueryContext(ctx, "SELECT "+cols+" FROM usage ORDER BY source, session, day, task, model, long_context")
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	rows := []core.UsageRow{}
	for rs.Next() {
		var r core.UsageRow
		var cost sql.NullFloat64
		if err := rs.Scan(&r.Source, &r.Session, &r.Day, &r.Task, &r.Model, &r.LongContext, &r.Messages,
			&r.Input, &r.Output, &r.Reasoning, &r.CacheWrite5m, &r.CacheWrite1h, &r.CacheRead, &cost, &r.Marked); err != nil {
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

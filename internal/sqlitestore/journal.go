package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

const markerCols = "id, at, kind, session, task"

// Update implements core.Journal. The scope's events are read and the new one
// appended in one immediate transaction.
func (s *Store) Update(ctx context.Context, session string, fn func([]core.Marker, int) (*core.Marker, error)) error {
	return s.immediate(ctx, func(conn *sql.Conn) error {
		scope, err := queryMarkers(ctx, conn, "WHERE session = ? ORDER BY at, id", session)
		if err != nil {
			return err
		}
		var total int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM markers").Scan(&total); err != nil {
			return err
		}
		m, err := fn(scope, total)
		if err != nil || m == nil {
			return err
		}
		return appendMarker(ctx, conn, *m)
	})
}

func appendMarker(ctx context.Context, conn *sql.Conn, m core.Marker) error {
	if err := m.Validate(); err != nil {
		return err
	}
	var task any
	if m.Kind == core.MarkStart {
		task = m.Task
	}
	_, err := conn.ExecContext(ctx, "INSERT INTO markers(at, kind, session, task) VALUES(?,?,?,?)",
		m.At.UTC().Format(core.MarkerTimeLayout), string(m.Kind), m.Session, task)
	return err
}

// Markers implements core.Journal. A database from before schema version 2
// has no journal.
func (s *Store) Markers(ctx context.Context) ([]core.Marker, error) {
	if s.version < 2 {
		return []core.Marker{}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return queryMarkers(ctx, conn, "ORDER BY at, id")
}

// queryMarkers reads and re-validates events; a bad stored value is an error
// that never repeats the value.
func queryMarkers(ctx context.Context, conn *sql.Conn, tail string, args ...any) ([]core.Marker, error) {
	rs, err := conn.QueryContext(ctx, "SELECT "+markerCols+" FROM markers "+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []core.Marker{}
	for rs.Next() {
		var m core.Marker
		var at, kind string
		var task sql.NullString
		if err := rs.Scan(&m.ID, &at, &kind, &m.Session, &task); err != nil {
			return nil, err
		}
		m.Kind, m.Task = core.MarkerKind(kind), task.String
		if m.At, err = time.Parse(core.MarkerTimeLayout, at); err != nil {
			return nil, fmt.Errorf("stored marker #%d has an invalid time", m.ID)
		}
		if err := m.Validate(); err != nil {
			return nil, fmt.Errorf("stored marker #%d is invalid: %w", m.ID, err)
		}
		out = append(out, m)
	}
	return out, rs.Err()
}

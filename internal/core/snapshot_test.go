package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

var now = time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)

func goodBatch() core.Batch {
	return core.Batch{
		Rows: []core.UsageRow{{
			Key:      core.Key{Source: "claude", Session: "s-1", Day: "2026-11-03", Task: "ABC-1", Model: "m"},
			Messages: 2, Tokens: core.Tokens{Input: 10},
		}},
		Sessions: []core.SessionInfo{{Source: "claude", Session: "s-1", TaskSource: "heuristic", LabelsSeen: 1}},
		Skipped:  4,
	}
}

func TestSnapshotLogsClockTime(t *testing.T) {
	st := kanjotest.NewMemStore()
	log, err := core.Snapshot(context.Background(), kanjotest.StaticSource{Batch: goodBatch()}, st, kanjotest.FixedClock{T: now})
	if err != nil {
		t.Fatal(err)
	}
	want := core.SnapshotLog{At: now, Sessions: 1, Rows: 1, Skipped: 4}
	if log != want || !reflect.DeepEqual(st.Logs(), []core.SnapshotLog{want}) {
		t.Fatalf("log = %+v, stored %+v", log, st.Logs())
	}
}

func TestSnapshotRejectsInvalidBatch(t *testing.T) {
	bad := goodBatch()
	bad.Rows[0].Task = "../etc"
	badSession := goodBatch()
	badSession.Sessions[0].TaskSource = "bogus"
	for name, b := range map[string]core.Batch{"row": bad, "session": badSession} {
		st := kanjotest.NewMemStore()
		_, err := core.Snapshot(context.Background(), kanjotest.StaticSource{Batch: b}, st, kanjotest.FixedClock{T: now})
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if rows, _ := st.Rows(context.Background()); len(rows) != 0 || len(st.Logs()) != 0 {
			t.Fatalf("%s: store must stay untouched", name)
		}
	}
}

func TestSnapshotSourceErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	st := kanjotest.NewMemStore()
	_, err := core.Snapshot(context.Background(), kanjotest.StaticSource{Err: boom}, st, kanjotest.FixedClock{T: now})
	if !errors.Is(err, boom) || len(st.Logs()) != 0 {
		t.Fatalf("err = %v, logs %v", err, st.Logs())
	}
}

func TestSnapshotTwiceKeepsRows(t *testing.T) {
	st := kanjotest.NewMemStore()
	src := kanjotest.StaticSource{Batch: goodBatch()}
	for i := 0; i < 2; i++ {
		if _, err := core.Snapshot(context.Background(), src, st, kanjotest.FixedClock{T: now}); err != nil {
			t.Fatal(err)
		}
	}
	if rows, _ := st.Rows(context.Background()); len(rows) != 1 || rows[0].Messages != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

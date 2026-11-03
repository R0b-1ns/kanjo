package core_test

import (
	"reflect"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
)

func urow(session, day, task, model string, msgs, out int64) core.UsageRow {
	return core.UsageRow{
		Key:      core.Key{Source: "claude", Session: session, Day: day, Task: task, Model: model},
		Messages: msgs, Tokens: core.Tokens{Output: out},
	}
}

func TestAggregateByTaskCutsAtSlash(t *testing.T) {
	rows := []core.UsageRow{
		urow("s", "2026-11-03", "ABC-1/one", "m", 1, 1_000_000),
		urow("s", "2026-11-03", "ABC-1/two", "m", 2, 1_000_000),
		urow("s", "2026-11-03", "-", "m", 1, 0),
	}
	rep := core.Aggregate(rows, nil, prices(), core.Query{By: "task"})
	if len(rep.Groups) != 2 || rep.Groups[0].Name != "-" || rep.Groups[1].Name != "ABC-1" {
		t.Fatalf("groups = %+v", rep.Groups)
	}
	if g := rep.Groups[1]; g.Messages != 3 || g.Output != 2_000_000 || !near(g.USD, 40) || !g.Priced {
		t.Fatalf("ABC-1 group = %+v", g)
	}
}

func TestAggregateUnpricedNotInTotal(t *testing.T) {
	rows := []core.UsageRow{
		urow("s", "2026-11-03", "ABC-1", "m", 1, 1_000_000),
		urow("s", "2026-11-03", "ABC-1", "unknown", 5, 9_000_000),
	}
	rep := core.Aggregate(rows, nil, prices(), core.Query{By: "model"})
	if !near(rep.TotalUSD, 20) || rep.UnpricedMessages != 5 {
		t.Fatalf("total %v unpriced %d", rep.TotalUSD, rep.UnpricedMessages)
	}
	if rep.Groups[0].Name != "m" || !rep.Groups[0].Priced || rep.Groups[1].Priced || rep.Groups[1].UnpricedMessages != 5 {
		t.Fatalf("groups = %+v", rep.Groups)
	}
}

func TestAggregateFiltersTaskAndSince(t *testing.T) {
	rows := []core.UsageRow{
		urow("s", "2026-11-01", "ABC-1", "m", 1, 1),
		urow("s", "2026-11-02", "ABC-1", "m", 1, 1),
		urow("s", "2026-11-02", "XYZ-2", "m", 5, 1),
	}
	rep := core.Aggregate(rows, nil, prices(), core.Query{By: "day", Task: "ABC-1", Since: "2026-11-02"})
	if len(rep.Groups) != 1 || rep.Groups[0].Name != "2026-11-02" || rep.Groups[0].Messages != 1 {
		t.Fatalf("groups = %+v", rep.Groups)
	}
}

func TestAggregateBySession(t *testing.T) {
	rows := []core.UsageRow{urow("s-b", "2026-11-03", "-", "m", 1, 1), urow("s-a", "2026-11-03", "-", "m", 1, 1)}
	rep := core.Aggregate(rows, nil, prices(), core.Query{By: "session"})
	if rep.Groups[0].Name != "s-a" || rep.Groups[1].Name != "s-b" {
		t.Fatalf("groups = %+v", rep.Groups)
	}
}

func TestAggregateFlagsMultiLabelSessions(t *testing.T) {
	rows := []core.UsageRow{urow("s-multi", "2026-11-03", "ABC-1", "m", 1, 1), urow("s-multi", "2026-11-03", "ABC-1", "x", 1, 1), urow("s-one", "2026-11-03", "ABC-1", "m", 1, 1)}
	sessions := []core.SessionInfo{
		{Source: "claude", Session: "s-multi", TaskSource: "heuristic", LabelsSeen: 2},
		{Source: "claude", Session: "s-one", TaskSource: "heuristic", LabelsSeen: 1},
	}
	rep := core.Aggregate(rows, sessions, prices(), core.Query{By: "task"})
	if want := []string{"s-multi"}; !reflect.DeepEqual(rep.MultiLabelSessions, want) {
		t.Fatalf("MultiLabelSessions = %v, want %v", rep.MultiLabelSessions, want)
	}
	rep = core.Aggregate(rows, sessions, prices(), core.Query{By: "task", Since: "2026-11-04"})
	if len(rep.MultiLabelSessions) != 0 {
		t.Fatalf("filtered-out sessions must not be flagged: %v", rep.MultiLabelSessions)
	}
}

func TestQueryValidate(t *testing.T) {
	for _, q := range []core.Query{{By: "task"}, {By: "model", Task: "ABC-1", Since: "2026-11-03"}, {By: "session"}, {By: "day"}} {
		if err := q.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", q, err)
		}
	}
	for _, q := range []core.Query{{By: "bogus"}, {}, {By: "task", Task: "../x"}, {By: "task", Since: "yesterday"}} {
		if err := q.Validate(); err == nil {
			t.Errorf("%+v accepted", q)
		}
	}
}

func TestTaskFilterMatchesTheLabelAndItsSubTasks(t *testing.T) {
	rows := []core.UsageRow{
		urow("s", "2026-11-03", "ABC-1", "m", 1, 1),
		urow("s", "2026-11-03", "ABC-1/review", "m", 2, 1),
		urow("s", "2026-11-03", "ABC-1/review/deep", "m", 4, 1),
		urow("s", "2026-11-03", "ABC-1/other", "m", 8, 1),
		urow("s", "2026-11-03", "ABC-10", "m", 16, 1),
	}
	for filter, want := range map[string]int64{
		"ABC-1":             1 + 2 + 4 + 8,
		"ABC-1/review":      2 + 4,
		"ABC-1/review/deep": 4,
		"ABC-10":            16,
		"ABC-1/rev":         0,
	} {
		rep := core.Aggregate(rows, nil, prices(), core.Query{By: "day", Task: filter})
		var got int64
		for _, g := range rep.Groups {
			got += g.Messages
		}
		if got != want {
			t.Errorf("--task %s counted %d messages, want %d", filter, got, want)
		}
	}
}

package core_test

import (
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
)

func TestUSDBySourceSplitsClaudeAndJuniors(t *testing.T) {
	junior := urow("j", "2026-11-03", "ABC-1", "free-model", 1, 5)
	junior.Source = "junior"
	cost := 0.5
	junior.CostReportedUSD = &cost
	rows := []core.UsageRow{urow("s", "2026-11-03", "ABC-1", "m", 1, 1_000_000), junior}
	rep := core.Aggregate(rows, nil, prices(), core.Query{By: "task"})
	if !near(rep.USDBySource["claude"], 20) || !near(rep.USDBySource["junior"], 0.5) || !near(rep.TotalUSD, 20.5) {
		t.Fatalf("by source = %v, total %v", rep.USDBySource, rep.TotalUSD)
	}
}

func TestUnpricedRowsAddNothingToSources(t *testing.T) {
	rep := core.Aggregate([]core.UsageRow{urow("s", "2026-11-03", "ABC-1", "unknown", 4, 9)}, nil, prices(), core.Query{By: "task"})
	if len(rep.USDBySource) != 0 || rep.UnpricedMessages != 4 {
		t.Fatalf("by source = %v, unpriced %d", rep.USDBySource, rep.UnpricedMessages)
	}
}

func TestNewDashboardGroupsThreeWays(t *testing.T) {
	rows := []core.UsageRow{
		urow("s", "2026-11-02", "XYZ-2", "m", 1, 1_000_000),
		urow("s", "2026-11-03", "ABC-1", "m", 2, 1_000_000),
	}
	d := core.NewDashboard(rows, nil, prices(), core.Query{})
	if !near(d.Models.TotalUSD, 40) || !near(d.Tasks.TotalUSD, 40) || !near(d.Days.TotalUSD, 40) {
		t.Fatalf("totals differ: %v %v %v", d.Models.TotalUSD, d.Tasks.TotalUSD, d.Days.TotalUSD)
	}
	if len(d.Models.Groups) != 1 || d.Tasks.Groups[0].Name != "ABC-1" || d.Days.Groups[0].Name != "2026-11-02" {
		t.Fatalf("dashboard = %+v", d)
	}
}

func TestNewDashboardHonoursFilters(t *testing.T) {
	rows := []core.UsageRow{
		urow("s", "2026-11-02", "XYZ-2", "m", 1, 1_000_000),
		urow("s", "2026-11-03", "ABC-1", "m", 2, 1_000_000),
	}
	d := core.NewDashboard(rows, nil, prices(), core.Query{Task: "ABC-1", Since: "2026-11-03"})
	if !near(d.Days.TotalUSD, 20) || len(d.Tasks.Groups) != 1 {
		t.Fatalf("dashboard = %+v", d)
	}
}

func TestFlattenKeepsAllRowsAndSessions(t *testing.T) {
	ps := []core.ProjectUsage{
		{Name: "a", Rows: []core.UsageRow{urow("s", "2026-11-03", "-", "m", 1, 1)}, Sessions: []core.SessionInfo{{Source: "claude", Session: "s", TaskSource: "none"}}},
		{Name: "b", Rows: []core.UsageRow{urow("t", "2026-11-03", "-", "m", 1, 1)}},
	}
	rows, sessions := core.Flatten(ps)
	if len(rows) != 2 || len(sessions) != 1 {
		t.Fatalf("rows %d sessions %d", len(rows), len(sessions))
	}
}

func twoProjects() []core.ProjectUsage {
	a := urow("s1", "2026-11-02", "ABC-1", "m", 3, 0)
	a.Input = 1_000_000 // 4 USD
	return []core.ProjectUsage{
		{Name: "app-a", Rows: []core.UsageRow{a}},
		{Name: "app-b", Rows: []core.UsageRow{
			urow("s1", "2026-11-03", "XYZ-3", "m", 2, 500_000), // 10 USD
			urow("s2", "2026-11-03", "XYZ-3", "unknown", 2, 7),
		}},
	}
}

func TestAggregateProjectsByProject(t *testing.T) {
	rep := core.AggregateProjects(twoProjects(), prices(), core.Query{By: "project"})
	if len(rep.Groups) != 2 || rep.Groups[0].Name != "app-a" || rep.Groups[1].Name != "app-b" {
		t.Fatalf("groups = %+v", rep.Groups)
	}
	a, b := rep.Groups[0], rep.Groups[1]
	if !near(a.USD, 4) || a.Messages != 3 || !a.Priced || !near(b.USD, 10) || b.Messages != 4 || b.UnpricedMessages != 2 || b.Output != 500_007 {
		t.Fatalf("a = %+v b = %+v", a, b)
	}
	if !near(rep.TotalUSD, 14) || rep.UnpricedMessages != 2 || !near(rep.USDBySource["claude"], 14) {
		t.Fatalf("report = %+v", rep)
	}
}

func TestAggregateProjectsByModelSumsAcrossProjects(t *testing.T) {
	rep := core.AggregateProjects(twoProjects(), prices(), core.Query{By: "model"})
	if len(rep.Groups) != 2 || rep.Groups[0].Name != "m" || !near(rep.Groups[0].USD, 14) || rep.Groups[1].Name != "unknown" {
		t.Fatalf("groups = %+v", rep.Groups)
	}
}

func TestAggregateProjectsSinceFilter(t *testing.T) {
	rep := core.AggregateProjects(twoProjects(), prices(), core.Query{By: "project", Since: "2026-11-03"})
	if len(rep.Groups) != 1 || rep.Groups[0].Name != "app-b" {
		t.Fatalf("groups = %+v", rep.Groups)
	}
}

func TestAggregateProjectsKeepsMultiLabelWarnings(t *testing.T) {
	ps := twoProjects()
	ps[0].Sessions = []core.SessionInfo{{Source: "claude", Session: "s1", TaskSource: "heuristic", LabelsSeen: 2}}
	rep := core.AggregateProjects(ps, prices(), core.Query{By: "project"})
	if len(rep.MultiLabelSessions) != 1 || rep.MultiLabelSessions[0] != "s1" {
		t.Fatalf("multi = %v", rep.MultiLabelSessions)
	}
}

func TestValidateAll(t *testing.T) {
	for by, ok := range map[string]bool{"project": true, "task": true, "model": true, "day": true, "session": false, "bogus": false} {
		if err := (core.Query{By: by}).ValidateAll(); (err == nil) != ok {
			t.Errorf("ValidateAll(%q) = %v, want ok=%v", by, err, ok)
		}
	}
	if err := (core.Query{By: "task", Since: "x"}).ValidateAll(); err == nil {
		t.Error("a bad --since must be refused")
	}
}

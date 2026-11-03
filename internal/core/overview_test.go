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

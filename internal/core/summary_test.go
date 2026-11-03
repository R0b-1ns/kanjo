package core_test

import (
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

func rec(task string, started time.Time, v core.RunVerdict, launches, retries int, dur time.Duration, lead core.LeadVerdict) core.RunRecord {
	r := core.RunRecord{Run: core.Run{Session: "s", Task: task, StartedAt: started}, RunResult: core.RunResult{Verdict: v, Launches: launches, Retries: retries, Duration: dur}}
	if lead != "" {
		r.Lead = &core.Lead{Verdict: lead}
	}
	return r
}

var day1, day2 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func journalOf() []core.RunRecord {
	return []core.RunRecord{
		rec("A-1", day1, core.RunOK, 1, 0, time.Second, core.Accepted),
		rec("A-1", day1, core.RunFailed, 2, 1, 2*time.Second, core.Rejected),
		rec("B-2", day2, core.RunOK, 2, 1, 3*time.Second, ""),
		rec("-", day2, core.RunTimeout, 1, 0, 4*time.Second, core.Abandoned),
		rec("-", day2, core.RunRunning, 0, 0, 0, ""),
	}
}

func TestSummaryTotalsOnAHandComputedJournal(t *testing.T) {
	s := core.Summarize(journalOf(), "", time.Time{})
	if s.Runs != 5 || s.Launches != 6 || s.Retries != 2 || s.Duration != 10*time.Second || s.NoLead != 2 {
		t.Fatalf("%+v", s)
	}
	if s.Script[core.RunOK] != 2 || s.Script[core.RunFailed] != 1 || s.Script[core.RunTimeout] != 1 || s.Script[core.RunRunning] != 1 {
		t.Fatalf("script %v", s.Script)
	}
	if s.Lead[core.Accepted] != 1 || s.Lead[core.Rejected] != 1 || s.Lead[core.Abandoned] != 1 || s.Lead[core.Modified] != 0 {
		t.Fatalf("lead %v", s.Lead)
	}
}

func TestSummaryCountsDistinctTasksNotRuns(t *testing.T) {
	if s := core.Summarize(journalOf(), "", time.Time{}); s.Tasks != 2 {
		t.Fatalf("tasks = %d", s.Tasks)
	}
}

func TestSummaryCountsUnlabelledRunsSeparately(t *testing.T) {
	if s := core.Summarize(journalOf(), "", time.Time{}); s.Unlabelled != 2 {
		t.Fatalf("unlabelled = %d", s.Unlabelled)
	}
}

func TestSummaryFiltersByTaskAndSince(t *testing.T) {
	if s := core.Summarize(journalOf(), "A-1", time.Time{}); s.Runs != 2 || s.Tasks != 1 {
		t.Fatalf("by task: %+v", s)
	}
	if s := core.Summarize(journalOf(), "", day2.Truncate(24*time.Hour)); s.Runs != 3 {
		t.Fatalf("since: %+v", s)
	}
	if s := core.Summarize(journalOf(), "A-1", day2.Truncate(24*time.Hour)); s.Runs != 0 {
		t.Fatalf("both: %+v", s)
	}
}

func TestSummaryCountsRunsWithoutALead(t *testing.T) {
	if s := core.Summarize(journalOf()[2:3], "", time.Time{}); s.NoLead != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestSummaryOfNothingIsZeros(t *testing.T) {
	s := core.Summarize(nil, "", time.Time{})
	if s.Runs != 0 || s.Tasks != 0 || s.Script == nil || s.Lead == nil || len(s.Script) != 0 {
		t.Fatalf("%+v", s)
	}
}

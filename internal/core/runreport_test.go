package core_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func costPtr(v float64) *float64 { return &v }

// usageRig runs a task whose agent forgets the file once, then writes it, and
// reports usage on both launches.
func usageRig(t *testing.T) (*rig, core.DelegateOutcome) {
	t.Helper()
	r := newRig(t,
		[]kanjotest.AgentStep{kanjotest.ReportUsage("model-b", core.Tokens{Input: 10, Output: 1}, costPtr(0.5)), kanjotest.ReportUsage("model-a", core.Tokens{Input: 3}, nil)},
		[]kanjotest.AgentStep{kanjotest.Write("src/a.go", "package a"), kanjotest.ReportUsage("model-b", core.Tokens{Input: 5, CacheRead: 7}, costPtr(0.25))})
	req := r.req()
	req.Setup, req.Test, req.Checks = "make deps", "go test", []string{"lint"}
	return r, r.run(req)
}

func TestReportJSONHasTheDocumentedFields(t *testing.T) {
	_, out := usageRig(t)
	for i := range out.Report.Steps {
		out.Report.Steps[i].DurationMS = 0 // measured with the real clock by the fakes
	}
	got, err := json.MarshalIndent(out.Report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const golden = "testdata/report.golden.json"
	if *update {
		os.WriteFile(golden, got, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("report.json differs from %s:\n%s", golden, got)
	}
}

func TestReportFileEqualsTheOutcome(t *testing.T) {
	r, out := usageRig(t)
	onDisk, ok := r.art.Read(testSession, core.ArtReportJSON)
	want, _ := json.MarshalIndent(out.Report, "", "  ")
	if !ok || strings.TrimSpace(string(onDisk)) != string(want) {
		t.Fatalf("report.json = %q, ok %v", onDisk, ok)
	}
	txt, ok := r.art.Read(testSession, core.ArtReportTXT)
	if !ok || string(txt) != out.Text || out.Text != out.Report.Text("") {
		t.Fatalf("report.txt = %q", txt)
	}
}

func TestReportTextEndsWithApplyFinishAndNote(t *testing.T) {
	r := newRig(t, writeA)
	out := r.run(r.req())
	lines := strings.Split(strings.TrimRight(out.Text, "\n"), "\n")
	n := len(lines)
	if n < 3 || lines[n-3] != "apply: git apply runs/"+testSession+"/changes.patch" ||
		lines[n-2] != "finish: kanjo finish "+testSession+" --verdict accepted|modified|rejected" ||
		lines[n-1] != "note: ABC-12 "+testSession+" ok-to-review 0s" {
		t.Fatalf("text:\n%s", out.Text)
	}
	abs := out.Report.Text("/data dir/it's")
	if !strings.Contains(abs, `apply: git apply '/data dir/it'\''s/runs/`+testSession+`/changes.patch'`) {
		t.Fatalf("absolute text:\n%s", abs)
	}
}

func TestReportHasNoCommandText(t *testing.T) {
	const canary = "CANARY-CMD-31"
	r := newRig(t, writeA)
	req := r.req()
	req.Setup, req.Test, req.Checks = "echo "+canary+" setup", "echo "+canary+" test", []string{"echo " + canary + " check"}
	out := r.run(req)
	js, _ := json.Marshal(out.Report)
	txt, _ := r.art.Read(testSession, core.ArtReportTXT)
	for _, s := range []string{string(js), out.Text, string(txt)} {
		if strings.Contains(s, canary) || strings.Contains(s, "echo ") {
			t.Fatalf("a command leaked into the report:\n%s", s)
		}
	}
}

func TestReportStepsAreNamedNotQuoted(t *testing.T) {
	_, out := usageRig(t)
	var names []string
	for _, s := range out.Report.Steps {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "setup,agent,test,check-1" {
		t.Fatalf("steps = %s", got)
	}
}

func TestReportListsEveryReason(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Write("keys/id", "x"), kanjotest.Write("notes.txt", "x"), kanjotest.Symlink("ln", "/etc")})
	req := r.req()
	req.Forbid, req.Test = []string{"keys"}, "go test"
	r.sb.Handler = func(context.Context, core.BoxSpec, core.Cmd) core.ExecResult { return core.ExecResult{Exit: 1} }
	out := r.run(req)
	got := fmt.Sprint(out.Report.Reasons)
	for _, want := range []string{"forbidden-path", "unsafe-symlink", "stray-files", "test-failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("reasons %s lack %s", got, want)
		}
	}
	if out.Report.Verdict != core.RunFailed || len(out.Report.Files.Unexpected) == 0 || len(out.Report.Files.ForbiddenTouched) != 1 {
		t.Fatalf("report = %+v", out.Report)
	}
}

func TestReportTimedOutAndPatchFields(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Loop()})
	req := r.req()
	req.Timeout = 30_000_000
	out := r.run(req)
	if !out.Report.TimedOut || out.Report.Verdict != core.RunTimeout || out.Report.Patch != "changes.patch" || out.Report.ExitCode != 124 {
		t.Fatalf("report = %+v", out.Report)
	}
	r = newRig(t, writeA)
	req = r.req()
	req.MaxPatchBytes = 3
	if out := r.run(req); out.Report.Patch != "" {
		t.Fatalf("patch = %q although none was kept", out.Report.Patch)
	}
}

func TestUsageRowIsWrittenForEachModel(t *testing.T) {
	r, out := usageRig(t)
	rows, _ := r.st.Rows(context.Background())
	if len(rows) != 2 || out.Report.Tokens.Status != "available" {
		t.Fatalf("rows = %+v", rows)
	}
	a, b := rows[0], rows[1]
	if a.Model != "model-a" || b.Model != "model-b" {
		t.Fatalf("models = %s %s", a.Model, b.Model)
	}
	for _, row := range rows {
		if row.Source != "script" || row.Session != testSession || row.Day != "2026-11-03" || row.Task != "ABC-12" || row.Messages != 2 || row.Marked != 2 || row.LongContext {
			t.Errorf("row = %+v", row)
		}
	}
	sessions, _ := r.st.Sessions(context.Background())
	if len(sessions) != 1 || sessions[0] != (core.SessionInfo{Source: "script", Session: testSession, TaskSource: "none"}) {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestUsageOfBothLaunchesIsSummedPerModel(t *testing.T) {
	r, _ := usageRig(t)
	rows, _ := r.st.Rows(context.Background())
	b := rows[1]
	if b.Input != 15 || b.Output != 1 || b.CacheRead != 7 || b.CostReportedUSD == nil || *b.CostReportedUSD != 0.75 {
		t.Fatalf("model-b = %+v", b)
	}
}

func TestUnknownCostIsNull(t *testing.T) {
	r, out := usageRig(t)
	rows, _ := r.st.Rows(context.Background())
	if rows[0].CostReportedUSD != nil || out.Report.Tokens.ByModel[0].CostUSD != nil {
		t.Fatalf("model-a cost = %v", rows[0].CostReportedUSD)
	}
}

func TestFreeModelCostZeroIsKeptAsZero(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.ReportUsage("free-model", core.Tokens{Input: 4}, costPtr(0))})
	out := r.run(r.req())
	rows, _ := r.st.Rows(context.Background())
	if len(rows) != 1 || rows[0].CostReportedUSD == nil || *rows[0].CostReportedUSD != 0 || out.Report.Tokens.ByModel[0].CostUSD == nil {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUnavailableUsageIsNotAFailure(t *testing.T) {
	r := newRig(t, writeA)
	out := r.run(r.req())
	rows, _ := r.st.Rows(context.Background())
	sessions, _ := r.st.Sessions(context.Background())
	if len(rows) != 0 || len(sessions) != 0 || out.Report.Tokens.Status != "unavailable" || out.Result.Verdict != core.RunOK || len(out.Warnings) != 0 {
		t.Fatalf("rows %v sessions %v report %+v", rows, sessions, out.Report.Tokens)
	}
}

func TestUsageRowPassesValidate(t *testing.T) {
	r, _ := usageRig(t)
	rows, _ := r.st.Rows(context.Background())
	for _, row := range rows {
		if err := row.Validate(); err != nil {
			t.Errorf("%+v: %v", row, err)
		}
	}
	bad := newRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.ReportUsage("bad model!", core.Tokens{Input: 1}, nil)})
	out := bad.run(bad.req())
	if rows, _ := bad.st.Rows(context.Background()); len(rows) != 0 || len(out.Warnings) == 0 || out.Result.Verdict != core.RunOK {
		t.Fatalf("an invalid model id must be a warning, not a row: %v %v", rows, out.Warnings)
	}
}

func TestUsageIsKeptEvenWhenTheRunFails(t *testing.T) {
	r := newRig(t, []kanjotest.AgentStep{kanjotest.ReportUsage("m", core.Tokens{Input: 9}, nil), kanjotest.Fail(2)})
	out := r.run(r.req())
	if rows, _ := r.st.Rows(context.Background()); len(rows) != 1 || out.Result.Verdict != core.RunFailed {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestCanaryNeverInReportOrRows(t *testing.T) {
	const canary = "CANARY-ALL-8c2"
	r := newRig(t, []kanjotest.AgentStep{
		kanjotest.Write("src/a.go", "package a // "+canary), kanjotest.Output("answer " + canary),
		kanjotest.ReportUsage("m", core.Tokens{Input: 1}, nil)})
	r.sb.Handler = okHandler(func(c core.Cmd) { io.WriteString(c.Out, "test output "+canary) })
	req := r.req()
	req.Prompt = "do it " + canary
	req.Test, req.Setup, req.Checks = "echo "+canary, "echo "+canary, []string{"echo " + canary}
	out := r.run(req)
	rows, _ := r.st.Rows(context.Background())
	sessions, _ := r.st.Sessions(context.Background())
	recs, _ := r.j.Runs(context.Background())
	var haystack []string
	haystack = append(haystack, out.Text, fmt.Sprintf("%+v", out), fmt.Sprintf("%+v", rows), fmt.Sprintf("%+v", sessions), fmt.Sprintf("%+v", recs))
	for name, data := range r.art.All() {
		if strings.HasSuffix(name, ".log") {
			continue // logs hold output by design
		}
		haystack = append(haystack, name+string(data))
	}
	for _, h := range haystack {
		if strings.Contains(h, canary) {
			t.Fatalf("canary found in %.200s", h)
		}
	}
	if log, _ := r.art.Read(testSession, core.ArtAgentLog); !strings.Contains(string(log), canary) {
		t.Error("the test is blind: the canary should be in agent.log")
	}
}

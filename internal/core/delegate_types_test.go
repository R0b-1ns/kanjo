package core

import (
	"strings"
	"testing"
	"time"
)

var dt0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

const (
	commit40 = "0123456789abcdef0123456789abcdef01234567"
	commit64 = commit40 + "0123456789abcdef01234567"
)

func TestValidRepoPath(t *testing.T) {
	ok := []string{"src/a.go", "my file.txt", "a", "-x", "dir/sub/file.txt", strings.Repeat("a", 256)}
	bad := []string{"..", "a/../b", "/abs", "a//b", "./a", "", "a\nb", "a\x00b", strings.Repeat("a", 257), "a/", "a/.", "a\x7f", "\xff"}
	for _, p := range ok {
		if !ValidRepoPath(p) {
			t.Errorf("ValidRepoPath(%q) = false", p)
		}
	}
	for _, p := range bad {
		if ValidRepoPath(p) {
			t.Errorf("ValidRepoPath(%q) = true", p)
		}
	}
}

func TestValidRef(t *testing.T) {
	for _, r := range []string{"main", "origin/main", "v1.2", "HEAD~1", "abc123", strings.Repeat("a", 200)} {
		if !ValidRef(r) {
			t.Errorf("ValidRef(%q) = false", r)
		}
	}
	for _, r := range []string{"-x", "a b", "a\nb", "", strings.Repeat("a", 201), "--upload-pack=x", "$(x)"} {
		if ValidRef(r) {
			t.Errorf("ValidRef(%q) = true", r)
		}
	}
}

func TestValidCommitID(t *testing.T) {
	for _, c := range []string{commit40, commit64} {
		if !ValidCommitID(c) {
			t.Errorf("ValidCommitID(%q) = false", c)
		}
	}
	for _, c := range []string{"", strings.ToUpper(commit40), commit40 + "0", "xyz"} {
		if ValidCommitID(c) {
			t.Errorf("ValidCommitID(%q) = true", c)
		}
	}
}

func TestNewSessionIDIsAValidSession(t *testing.T) {
	id := NewSessionID(dt0, [3]byte{0xa1, 0xb2, 0xc3})
	if id != "ses_20261103T120000_a1b2c3" || !ValidSession(id) {
		t.Fatalf("id = %q", id)
	}
}

func TestBranchAndDirAreDerivedFromTheSession(t *testing.T) {
	if BranchOf("ses_1") != "delegate/ses_1" || DirOf("ses_1") != "worktrees/ses_1" {
		t.Fatalf("%q %q", BranchOf("ses_1"), DirOf("ses_1"))
	}
}

func goodRun() Run {
	return Run{Session: "ses_20261103T120000_a1b2c3", Task: "ABC-12", Adapter: "script", Model: "test-model", StartedAt: dt0, Base: commit40}
}

func TestRunValidate(t *testing.T) {
	if err := goodRun().Validate(); err != nil {
		t.Fatalf("good run: %v", err)
	}
	r := goodRun()
	r.Task = NoTask
	if err := r.Validate(); err != nil {
		t.Fatalf("task -: %v", err)
	}
	for name, mut := range map[string]func(*Run){
		"session": func(r *Run) { r.Session = "-bad" },
		"task":    func(r *Run) { r.Task = "../x" },
		"adapter": func(r *Run) { r.Adapter = "Bad Adapter" },
		"model":   func(r *Run) { r.Model = "" },
		"time":    func(r *Run) { r.StartedAt = time.Time{} },
		"base":    func(r *Run) { r.Base = "nothex" },
	} {
		r := goodRun()
		mut(&r)
		if r.Validate() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestRunResultValidate(t *testing.T) {
	good := RunResult{Verdict: RunOK, FinishedAt: dt0, Launches: 2, Retries: 1, Duration: time.Second, Tip: commit40}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (RunResult{Verdict: RunRunning}).Validate(); err != nil {
		t.Fatalf("running: %v", err)
	}
	for name, mut := range map[string]func(*RunResult){
		"retries>launches":  func(r *RunResult) { r.Launches, r.Retries = 0, 1 },
		"launches 3":        func(r *RunResult) { r.Launches = 3 },
		"retries 2":         func(r *RunResult) { r.Launches, r.Retries = 2, 2 },
		"negative duration": func(r *RunResult) { r.Duration = -1 },
		"running+finish":    func(r *RunResult) { r.Verdict = RunRunning },
		"final no finish":   func(r *RunResult) { r.FinishedAt = time.Time{} },
		"unknown verdict":   func(r *RunResult) { r.Verdict = "bogus" },
		"bad tip":           func(r *RunResult) { r.Tip = "zz" },
	} {
		r := good
		mut(&r)
		if r.Validate() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if (RunResult{Verdict: RunRunning}).ValidateFinal() == nil {
		t.Error("running is not final")
	}
	if good.ValidateFinal() != nil {
		t.Error("ok is final")
	}
}

func TestLeadValidate(t *testing.T) {
	good := Lead{At: dt0, Session: "ses_1", Verdict: Accepted}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, l := range map[string]Lead{
		"time":    {Session: "ses_1", Verdict: Accepted},
		"session": {At: dt0, Session: "-x", Verdict: Accepted},
		"verdict": {At: dt0, Session: "ses_1", Verdict: "maybe"},
	} {
		if l.Validate() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if err := (Lead{At: dt0, Session: "ses_1", Verdict: Abandoned}).Validate(); err != nil {
		t.Fatalf("abandoned: %v", err)
	}
}

func TestRunRecordValidate(t *testing.T) {
	rec := RunRecord{Run: goodRun(), RunResult: RunResult{Verdict: RunRunning}}
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
	rec.Lead = &Lead{At: dt0, Session: "ses_other", Verdict: Accepted}
	if rec.Validate() == nil {
		t.Error("lead of another session must be refused")
	}
	rec = RunRecord{Run: goodRun(), RunResult: RunResult{Verdict: "bogus"}}
	if rec.Validate() == nil {
		t.Error("bad result must be refused")
	}
	rec = RunRecord{Run: Run{}, RunResult: RunResult{Verdict: RunRunning}}
	if rec.Validate() == nil {
		t.Error("bad run must be refused")
	}
	rec = RunRecord{Run: goodRun(), RunResult: RunResult{Verdict: RunRunning}, Lead: &Lead{Session: goodRun().Session}}
	if rec.Validate() == nil {
		t.Error("bad lead must be refused")
	}
}

func TestVerdictExitCodes(t *testing.T) {
	want := map[RunVerdict]int{RunOK: 0, RunStray: 0, RunTimeout: 124, RunFailed: 1, RunQuota: 1, RunInterrupted: 1, RunRunning: 1}
	for v, code := range want {
		if got := v.ExitCode(); got != code {
			t.Errorf("%s: exit %d, want %d", v, got, code)
		}
	}
}

func TestRunErrorsNeverEchoTheValue(t *testing.T) {
	const canary = "CANARY-9f3"
	r := goodRun()
	r.Task = canary + "/../x"
	r.Model = canary + " bad"
	if err := r.Validate(); err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("err = %v", err)
	}
	r = goodRun()
	r.Base = canary
	if err := r.Validate(); err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("err = %v", err)
	}
}

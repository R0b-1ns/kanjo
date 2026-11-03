package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

// finished runs a task on the fakes and returns its session.
func (r *runRig) finished(extra ...string) string {
	r.t.Helper()
	code, out, errb := r.run(extra...)
	if code != 0 {
		r.t.Fatalf("run exited %d: %s %s", code, out, errb)
	}
	return sessionOf(r.t, out)
}

func (r *runRig) finish(args ...string) (int, string, string) {
	return r.exec(context.Background(), append([]string{"finish"}, args...)...)
}

func TestFinishPrintsTheApplyCommandQuoted(t *testing.T) {
	r := newRunRig(t, writeFileA)
	r.data = filepath.Join(filepath.Dir(r.data), "it's a data dir")
	r.setConfig("")
	session := r.finished()
	code, out, errb := r.finish(session, "--verdict", "accepted", "--repo", r.cwd)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	want := "apply: git -C " + core.QuoteShell(r.cwd) + " apply " + core.QuoteShell(filepath.Join(r.data, "runs", session, "changes.patch"))
	if !strings.Contains(out, want) || !strings.Contains(want, `'\''`) {
		t.Fatalf("output lacks %q:\n%s", want, out)
	}
	if !strings.Contains(out, "workspace removed") || !strings.Contains(out, "finish: "+session+" accepted") {
		t.Fatalf("output:\n%s", out)
	}
	if r.ws.Removals() != 1 {
		t.Fatal("the worktree was not removed")
	}
}

func TestFinishRejectedPrintsNoApplyCommand(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	code, out, _ := r.finish(session, "--verdict", "rejected")
	if code != 0 || strings.Contains(out, "apply:") || !strings.Contains(out, "rejected") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestFinishRunsASnapshotAndPrintsTheTaskTotals(t *testing.T) {
	r := newRunRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.ReportUsage("test-model", core.Tokens{Input: 100, Output: 5}, nil)})
	session := r.finished("--task", "ABC-12")
	code, out, errb := r.finish(session, "--verdict", "modified")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	for _, want := range []string{"snapshot: ", "task ", "ABC-12", "total est_usd"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if rows := r.rows(); len(rows) < 2 {
		t.Fatalf("the snapshot saved no transcript rows: %+v", rows)
	}
}

func TestFinishWithoutATaskPrintsNoTotals(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	_, out, _ := r.finish(session, "--verdict", "accepted")
	if strings.Contains(out, "total est_usd") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestFinishSnapshotFailureIsAWarning(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	r.setConfig("[task]\nmap_file = \"" + r.cwd + "\"\n") // a folder cannot be read as a map: the snapshot fails, the verdict stands
	code, out, errb := r.finish(session, "--verdict", "accepted")
	if code != 0 || !strings.Contains(out, "finish: "+session+" accepted") || !strings.Contains(errb, "warning: snapshot") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
}

func TestFinishTwiceSameVerdictExits0(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	r.finish(session, "--verdict", "accepted")
	code, out, _ := r.finish(session, "--verdict", "accepted")
	if code != 0 || !strings.Contains(out, "already recorded") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestFinishOtherVerdictExits1(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	r.finish(session, "--verdict", "accepted")
	code, _, errb := r.finish(session, "--verdict", "rejected")
	if code != 1 || !strings.Contains(errb, "accepted") {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestFinishUnknownSessionExits1WithoutRemoving(t *testing.T) {
	r := newRunRig(t, writeFileA)
	commit, _ := r.ws.Resolve(context.Background(), "HEAD")
	w, _ := r.ws.Create(context.Background(), "ses_20261103T120000_ffffff", commit)
	r.ws.Release(w)
	code, _, errb := r.finish("ses_20261103T120000_ffffff", "--verdict", "accepted")
	if code != 1 || !strings.Contains(errb, "no run") || r.ws.Removals() != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestFinishOfARunningRunExits1(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	st := r.openStore()
	const s = "ses_20261103T120000_aaaaaa"
	st.Begin(ctx, core.Run{Session: s, Task: "-", Adapter: "script", Model: "m", StartedAt: fixedNow, Base: "1111111111111111111111111111111111111111"})
	st.Close()
	w, _ := r.ws.Create(ctx, s, "1111111111111111111111111111111111111111") // lock held: alive
	_ = w
	st = r.openStore()
	st.Created(ctx, s)
	st.Close()
	code, _, errb := r.finish(s, "--verdict", "accepted")
	if code != 1 || !strings.Contains(errb, "still running") {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestFinishBadSessionAndBadVerdictExit2(t *testing.T) {
	r := newRunRig(t, writeFileA)
	for _, args := range [][]string{
		{"../x", "--verdict", "accepted"}, {"ses_1"}, {"ses_1", "--verdict", "maybe"}, {"--verdict", "accepted"},
		{"ses_1", "--verdict", "accepted", "extra"}, {"-x", "--verdict", "accepted"},
	} {
		if code, _, _ := r.finish(args...); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if _, err := os.Stat(r.data); err == nil {
		t.Error("the data folder was created")
	}
}

func TestFinishAbandonedIsAUsageError(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	code, _, errb := r.finish(session, "--verdict", "abandoned")
	if code != 2 || !strings.Contains(errb, "clean") {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestFinishOutputCannotCarryEscapes(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	_, out, errb := r.finish(session, "--verdict", "accepted", "--repo", filepath.Join(r.cwd, "a\x1b[31mb"))
	if strings.ContainsAny(out+errb, "\x1b\x07") {
		t.Fatalf("escape in output: %q %q", out, errb)
	}
}

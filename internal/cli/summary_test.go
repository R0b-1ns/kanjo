package cli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// seedRuns writes three runs: two on ABC-12 (days apart) and one without a task.
func (w *world) seedRuns() {
	w.t.Helper()
	st, err := openStoreAt(w.data)
	if err != nil {
		w.t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	const base = "1111111111111111111111111111111111111111"
	runs := []struct {
		session, task string
		at            time.Time
		verdict       core.RunVerdict
		lead          core.LeadVerdict
		launches      int
		dur           time.Duration
	}{
		{"ses_1", "ABC-12", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), core.RunOK, core.Accepted, 1, 2 * time.Second},
		{"ses_2", "ABC-12", time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC), core.RunFailed, core.Rejected, 2, 3 * time.Second},
		{"ses_3", "-", time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC), core.RunTimeout, "", 1, 5 * time.Second},
	}
	for _, r := range runs {
		if err := st.Begin(ctx, core.Run{Session: r.session, Task: r.task, Adapter: "script", Model: "m", StartedAt: r.at, Base: base}); err != nil {
			w.t.Fatal(err)
		}
		res := core.RunResult{Verdict: r.verdict, Exit: r.verdict.ExitCode(), FinishedAt: r.at.Add(r.dur), Launches: r.launches, Duration: r.dur}
		if r.launches == 2 {
			res.Retries = 1
		}
		if err := st.Finish(ctx, r.session, res); err != nil {
			w.t.Fatal(err)
		}
		if r.lead != "" {
			if _, err := st.Judge(ctx, core.Lead{At: r.at, Session: r.session, Verdict: r.lead}); err != nil {
				w.t.Fatal(err)
			}
		}
	}
}

func TestSummaryPrintsTheTotals(t *testing.T) {
	w := newWorld(t)
	w.seedRuns()
	out := w.must("summary")
	for _, want := range []string{
		"runs  3", "tasks  1", "runs without a task  1", "launches  4", "retries  1", "duration  10s",
		"ok-to-review  1", "failed  1", "timeout  1", "accepted  1", "rejected  1", "no lead verdict  1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestSummaryBeforeAnyRunCreatesNothing(t *testing.T) {
	w := newWorld(t)
	if code, out, _ := w.run("summary"); code != 0 || out != "no runs\n" {
		t.Fatalf("exit %d out %q", code, out)
	}
	if _, err := os.Stat(w.data); err == nil {
		t.Fatal("summary created the data folder")
	}
}

func TestSummaryOnAVersion2DatabaseSaysNoRuns(t *testing.T) {
	w := newWorld(t)
	os.MkdirAll(w.data, 0o700)
	db, err := sql.Open("sqlite", filepath.Join(w.data, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE usage(x); PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(filepath.Join(w.data, "kanjo.db"))
	if code, out, _ := w.run("summary"); code != 0 || out != "no runs\n" {
		t.Fatalf("exit %d out %q", code, out)
	}
	if after, _ := os.ReadFile(filepath.Join(w.data, "kanjo.db")); string(after) != string(before) {
		t.Fatal("summary changed the database")
	}
}

func TestSummaryFilters(t *testing.T) {
	w := newWorld(t)
	w.seedRuns()
	out := w.must("summary", "--task", "ABC-12")
	if !strings.Contains(out, "runs  2") || strings.Contains(out, "timeout") {
		t.Fatalf("--task:\n%s", out)
	}
	out = w.must("summary", "--since", "2026-11-03")
	if !strings.Contains(out, "runs  2") || strings.Contains(out, "ok-to-review") {
		t.Fatalf("--since:\n%s", out)
	}
	if out := w.must("summary", "--task", "OTHER-1"); out != "no runs\n" {
		t.Fatalf("no match: %q", out)
	}
}

func TestSummaryOnAReadOnlyDataFolder(t *testing.T) {
	w := newWorld(t)
	w.seedRuns()
	os.Chmod(w.data, 0o500)
	defer os.Chmod(w.data, 0o700)
	if code, out, errb := w.run("summary"); code != 0 || !strings.Contains(out, "runs  3") {
		t.Fatalf("exit %d: %s %s", code, out, errb)
	}
}

func TestSummaryBadSinceExits2(t *testing.T) {
	w := newWorld(t)
	for _, since := range []string{"yesterday", "2026-13-01", "2026-1-1", ""} {
		if code, _, _ := w.run("summary", "--since", since); code != 2 {
			t.Errorf("--since %q: exit %d", since, code)
		}
	}
	if code, _, _ := w.run("summary", "--task", "../x"); code != 2 {
		t.Errorf("bad task: exit %d", code)
	}
	if code, _, _ := w.run("summary", "extra"); code != 2 {
		t.Errorf("stray argument: exit %d", code)
	}
}

func TestSummaryRefusesALinkedDatabase(t *testing.T) {
	w := newWorld(t)
	w.seedRuns()
	other := filepath.Join(t.TempDir(), "other.db")
	os.Rename(filepath.Join(w.data, "kanjo.db"), other)
	os.Symlink(other, filepath.Join(w.data, "kanjo.db"))
	if code, _, errb := w.run("summary"); code != 1 || !strings.Contains(errb, "symbolic link") {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

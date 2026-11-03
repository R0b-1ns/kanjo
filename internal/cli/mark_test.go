package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

// runAt runs a command with the clock at now.
func (w *world) runAt(now time.Time, args ...string) (code int, stdout, stderr string) {
	w.t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb, env{home: w.home, cwd: w.cwd, clock: kanjotest.FixedClock{T: now}})
	return code, out.String(), errb.String()
}

func TestMarkStartThenCurrent(t *testing.T) {
	w := newWorld(t)
	if out := w.must("mark", "start", "ABC-12"); out != "started ABC-12 (global) at 2026-11-03T12:00:00.000Z\n" {
		t.Fatalf("start output = %q", out)
	}
	if out := w.must("mark", "current"); out != "ABC-12 since 2026-11-03T12:00:00.000Z (global)\n" {
		t.Fatalf("current output = %q", out)
	}
}

func TestMarkFlagsMayComeBeforeOrAfterTheTask(t *testing.T) {
	w := newWorld(t)
	w.must("mark", "start", "--session", "s-one", "ABC-12")
	if out := w.must("mark", "current", "--session", "s-one"); !strings.HasPrefix(out, "ABC-12 since") || !strings.Contains(out, "(session s-one)") {
		t.Fatalf("current = %q", out)
	}
	w.must("mark", "start", "XYZ-3", "--session", "s-two")
	if out := w.must("mark", "current", "--session", "s-two"); !strings.HasPrefix(out, "XYZ-3 since") {
		t.Fatalf("current = %q", out)
	}
}

func TestMarkEndThenCurrentSaysNoActiveTask(t *testing.T) {
	w := newWorld(t)
	w.must("mark", "start", "ABC-12")
	later := fixedNow.Add(time.Minute)
	if code, out, _ := w.runAt(later, "mark", "end"); code != 0 || out != "ended the active task (global) at 2026-11-03T12:01:00.000Z\n" {
		t.Fatalf("end: code %d out %q", code, out)
	}
	if code, out, _ := w.runAt(later, "mark", "current"); code != 0 || out != "no active task\n" {
		t.Fatalf("current: code %d out %q", code, out)
	}
}

func TestMarkCurrentOfASessionFallsBackToGlobal(t *testing.T) {
	w := newWorld(t)
	w.must("mark", "start", "ABC-12")
	if out := w.must("mark", "current", "--session", "s-other"); !strings.Contains(out, "ABC-12") || !strings.Contains(out, "(global)") {
		t.Fatalf("current = %q", out)
	}
}

func TestMarkUsageErrorsExit2(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{
		{"mark"},
		{"mark", "pause"},
		{"mark", "start"},
		{"mark", "start", "../x"},
		{"mark", "start", "a b"},
		{"mark", "start", "-"},
		{"mark", "start", "ABC-1", "extra"},
		{"mark", "start", "ABC-1", "--session", "-bad"},
		{"mark", "end", "extra"},
		{"mark", "end", "--session", "a/b"},
		{"mark", "current", "--nope"},
	} {
		if code, out, errb := w.run(args...); code != 2 || out != "" || errb == "" {
			t.Errorf("%v: code %d stdout %q stderr %q, want 2 and a message", args, code, out, errb)
		}
	}
	if _, err := os.Stat(w.data); err == nil {
		t.Error("a usage error must not create the data folder")
	}
}

func TestRepeatedStartWritesOnce(t *testing.T) {
	w := newWorld(t)
	w.must("mark", "start", "ABC-12")
	code, out, _ := w.runAt(fixedNow.Add(time.Minute), "mark", "start", "ABC-12")
	if code != 0 || !strings.Contains(out, "already the active task") || !strings.Contains(out, "12:00:00.000Z") {
		t.Fatalf("code %d out %q", code, out)
	}
	if n := countMarkers(t, w); n != 1 {
		t.Fatalf("markers = %d, want 1", n)
	}
}

func TestMarkEndWithNothingActiveExits1(t *testing.T) {
	w := newWorld(t)
	code, out, errb := w.run("mark", "end")
	if code != 1 || out != "" || !strings.Contains(errb, "no active task") {
		t.Fatalf("code %d out %q err %q", code, out, errb)
	}
	w.must("mark", "start", "ABC-12", "--session", "s-one")
	if code, _, _ := w.runAt(fixedNow.Add(time.Minute), "mark", "end"); code != 1 {
		t.Fatalf("ending the global scope while only a session task is active: code %d", code)
	}
}

func TestMarkWithTheClockBehindExits1(t *testing.T) {
	w := newWorld(t)
	w.must("mark", "start", "ABC-12")
	code, _, errb := w.runAt(fixedNow.Add(-time.Hour), "mark", "start", "XYZ-3")
	if code != 1 || !strings.Contains(errb, "behind") || !strings.Contains(errb, "2026-11-03T12:00:00.000Z") {
		t.Fatalf("code %d err %q", code, errb)
	}
	if code, _, _ := w.runAt(fixedNow.Add(-time.Hour), "mark", "end"); code != 1 {
		t.Fatalf("end with the clock behind: code %d", code)
	}
	if n := countMarkers(t, w); n != 1 {
		t.Fatalf("markers = %d, want 1", n)
	}
}

func TestMarkStartAtTheCapExits1(t *testing.T) {
	w := newWorld(t)
	cfg, _ := os.ReadFile(filepath.Join(w.home, ".config", "kanjo", "config.toml"))
	write(t, filepath.Join(w.home, ".config", "kanjo", "config.toml"), string(cfg)+"[markers]\nmax_events = 1\n")
	w.must("mark", "start", "ABC-12")
	code, _, errb := w.runAt(fixedNow.Add(time.Minute), "mark", "start", "XYZ-3")
	if code != 1 || !strings.Contains(errb, "markers.max_events") {
		t.Fatalf("code %d err %q", code, errb)
	}
}

func TestMarkCurrentCreatesNothing(t *testing.T) {
	w := newWorld(t)
	if code, out, _ := w.run("mark", "current"); code != 0 || out != "no active task\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	if _, err := os.Stat(w.data); err == nil {
		t.Fatal("mark current must not create the data folder")
	}
}

func TestMarkRefusesALinkedDatabase(t *testing.T) {
	w := newWorld(t)
	target := filepath.Join(t.TempDir(), "elsewhere.db")
	write(t, target, "")
	if err := os.MkdirAll(w.data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(w.data, "kanjo.db")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mark", "start", "ABC-12"}, {"mark", "end"}, {"mark", "current"}} {
		if code, _, errb := w.run(args...); code != 1 || !strings.Contains(errb, "symbolic link") {
			t.Errorf("%v: code %d err %q", args, code, errb)
		}
	}
	if data, _ := os.ReadFile(target); len(data) != 0 {
		t.Fatal("the link target was written")
	}
}

func TestMarkOutputCannotCarryEscapes(t *testing.T) {
	w := newWorld(t)
	w.must("mark", "start", "ABC-12")
	rawExec(t, filepath.Join(w.data, "kanjo.db"), `INSERT INTO markers(at, kind, session, task) VALUES('2026-11-03T12:30:00.000Z','start','','ABC-13')`)
	_, out, errb := w.runAt(fixedNow.Add(time.Hour), "mark", "current")
	for _, r := range out + errb {
		if r < ' ' && r != '\n' {
			t.Fatalf("control character %q in output %q", r, out)
		}
	}
}

func TestMarkStoresNoPathOrText(t *testing.T) {
	w := newWorld(t)
	w.must("mark", "start", "ABC-12", "--session", "s-one")
	db, err := os.ReadFile(filepath.Join(w.data, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{w.home, w.cwd, w.data} {
		if strings.Contains(string(db), leak) {
			t.Fatalf("the database holds the path %q", leak)
		}
	}
}

func TestMarkedUsageEndToEnd(t *testing.T) {
	w := newWorld(t)
	w.must("mark", "start", "MARK-1")
	w.must("snapshot")
	if _, marked, _ := sessionTotals(w.rows(), "s-one"); marked != 0 {
		t.Fatalf("a marker started after the transcripts must not mark them (marked %d)", marked)
	}
}

func countMarkers(t *testing.T, w *world) int {
	t.Helper()
	return len(w.markers())
}

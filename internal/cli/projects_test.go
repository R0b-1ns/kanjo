package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

const allHeader = "project,messages,input,output,reasoning,cache_write_5m,cache_write_1h,cache_read,est_usd,unpriced_messages\n"

// seedProject saves rows in <folder>/.ai-usage and returns folder.
func seedProject(t *testing.T, folder string, rows ...core.UsageRow) string {
	t.Helper()
	st, err := sqlitestore.Open(filepath.Join(folder, ".ai-usage"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sessions := map[string]bool{}
	var infos []core.SessionInfo
	for _, r := range rows {
		if !sessions[r.Session] {
			sessions[r.Session] = true
			infos = append(infos, core.SessionInfo{Source: r.Source, Session: r.Session, TaskSource: "none"})
		}
	}
	if err := st.Merge(context.Background(), core.Batch{Rows: rows, Sessions: infos}, core.SnapshotLog{At: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	return folder
}

func usageRow(session, day, task, model string, msgs int64, tok core.Tokens) core.UsageRow {
	return core.UsageRow{Key: core.Key{Source: "claude", Session: session, Day: day, Task: task, Model: model}, Messages: msgs, Tokens: tok}
}

// topLevel puts top-level TOML lines first in the user config (a top-level
// key placed after a table would belong to the table).
func (w *world) topLevel(lines string) {
	w.t.Helper()
	path := filepath.Join(w.home, ".config", "kanjo", "config.toml")
	old, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	write(w.t, path, lines+string(old))
}

func (w *world) listProjects(folders ...string) {
	w.t.Helper()
	quoted := make([]string, len(folders))
	for i, f := range folders {
		quoted[i] = `"` + f + `"`
	}
	w.topLevel("projects = [" + strings.Join(quoted, ", ") + "]\n")
}

// twoProjects: app-a costs 2.0000 (1M sonnet input tokens); app-b costs
// 10.0000 (0.5M opus output tokens at 20 USD) plus 2 unpriced messages.
func (w *world) twoProjects() (a, b string) {
	root := w.t.TempDir()
	a = seedProject(w.t, filepath.Join(root, "app-a"),
		usageRow("a1", "2026-11-02", "ABC-1", "claude-sonnet-5-5", 3, core.Tokens{Input: 1_000_000}))
	b = seedProject(w.t, filepath.Join(root, "app-b"),
		usageRow("b1", "2026-11-03", "XYZ-3", "claude-opus-5-5", 2, core.Tokens{Output: 500_000}),
		usageRow("b2", "2026-11-03", "XYZ-3", "claude-unknown-9", 2, core.Tokens{Output: 7}))
	w.listProjects(a, b)
	return a, b
}

func TestUsageAllByProject(t *testing.T) {
	w := newWorld(t)
	w.twoProjects()
	code, out, errb := w.run("usage", "--all", "--csv")
	want := allHeader + "app-a,3,1000000,0,0,0,0,0,2.0000,0\n" + "app-b,4,0,500007,0,0,0,0,10.0000,2\n"
	if code != 0 || out != want {
		t.Fatalf("code %d\n%s\nwant\n%s", code, out, want)
	}
	if !strings.Contains(errb, "total est_usd 12.0000 (2 messages unpriced)") {
		t.Fatalf("stderr = %q", errb)
	}
}

func TestUsageAllByModelDayAndTask(t *testing.T) {
	w := newWorld(t)
	w.twoProjects()
	if out := w.must("usage", "--all", "--by", "model", "--csv"); !strings.Contains(out, "\nclaude-opus-5-5,2,0,500000,0,0,0,0,10.0000,0\n") || !strings.Contains(out, "\nclaude-unknown-9,2,0,7,0,0,0,0,,2\n") {
		t.Fatalf("by model:\n%s", out)
	}
	if out := w.must("usage", "--all", "--by", "day", "--since", "2026-11-03", "--csv"); !strings.Contains(out, "\n2026-11-03,4,0,500007,0,0,0,0,10.0000,2\n") || strings.Contains(out, "2026-11-02") {
		t.Fatalf("by day since:\n%s", out)
	}
	if out := w.must("usage", "--all", "--by", "task", "--task", "ABC-1", "--csv"); !strings.Contains(out, "\nABC-1,3,1000000,") || strings.Contains(out, "XYZ-3") {
		t.Fatalf("by task:\n%s", out)
	}
}

func TestUsageAllRefusesBySession(t *testing.T) {
	w := newWorld(t)
	w.twoProjects()
	if code, _, errb := w.run("usage", "--all", "--by", "session"); code != 2 || !strings.Contains(errb, "--by") {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
}

func TestUsageAllWithABrokenProjectStillPrintsTheRest(t *testing.T) {
	w := newWorld(t)
	_, b := w.twoProjects()
	if err := os.WriteFile(sqlitestore.Path(filepath.Join(b, ".ai-usage")), []byte("garbage that is not a sqlite database at all, padded to be long enough........"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errb := w.run("usage", "--all", "--csv")
	if code != 0 || out != allHeader+"app-a,3,1000000,0,0,0,0,0,2.0000,0\n" {
		t.Fatalf("code %d\n%s", code, out)
	}
	if !strings.Contains(errb, "warning: project app-b could not be read") || strings.Contains(errb, b) {
		t.Fatalf("stderr must name the project, not its path: %q", errb)
	}
}

func TestUsageAllWithoutProjects(t *testing.T) {
	w := newWorld(t)
	code, out, errb := w.run("usage", "--all")
	if code != 0 || !strings.Contains(out, "no projects") || errb != "" {
		t.Fatalf("code %d out %q err %q", code, out, errb)
	}
	code, out, errb = w.run("usage", "--all", "--csv")
	if code != 0 || out != allHeader || !strings.Contains(errb, "no projects") {
		t.Fatalf("csv: code %d out %q err %q", code, out, errb)
	}
}

func TestUsageAllTextShowsTotalAndNotice(t *testing.T) {
	w := newWorld(t)
	w.twoProjects()
	out := w.must("usage", "--all")
	for _, want := range []string{"project", "app-a", "total est_usd 12.0000 (2 messages unpriced)", "not a subscription bill"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestRepoConfigProjectsAreRefusedByUsageAll(t *testing.T) {
	w := newWorld(t)
	victim := seedProject(t, filepath.Join(t.TempDir(), "victim"), usageRow("v", "2026-11-03", "-", "claude-sonnet-5-5", 1, core.Tokens{Input: 1}))
	write(t, filepath.Join(w.cwd, "kanjo.toml"), "projects = [\""+victim+"\"]\n")
	code, out, errb := w.run("usage", "--all")
	if code != 1 || strings.Contains(out, "victim") || !strings.Contains(errb, "projects") {
		t.Fatalf("code %d out %q err %q", code, out, errb)
	}
}

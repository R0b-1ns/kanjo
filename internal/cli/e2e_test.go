package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

const fixtureProjects = "../../testdata/transcripts"

var fixedNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// world is a fake home with the fixture transcripts and a user config that
// reads the "-home-dev-demo" projects and writes into its own data folder.
type world struct {
	t               *testing.T
	home, cwd, data string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()
	w := &world{t: t, home: filepath.Join(root, "home"), cwd: filepath.Join(root, "work"), data: filepath.Join(root, "data")}
	if err := os.MkdirAll(w.cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	copyTree(t, fixtureProjects, filepath.Join(w.home, ".claude", "projects"))
	cfg := "data_dir = \"" + w.data + "\"\n[claude]\nproject_prefixes = [\"-home-dev-demo\"]\n"
	write(t, filepath.Join(w.home, ".config", "kanjo", "config.toml"), cfg)
	return w
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		write(t, filepath.Join(to, rel), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (w *world) run(args ...string) (code int, stdout, stderr string) {
	w.t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb, env{home: w.home, cwd: w.cwd, clock: kanjotest.FixedClock{T: fixedNow}})
	return code, out.String(), errb.String()
}

func (w *world) must(args ...string) string {
	w.t.Helper()
	code, out, errb := w.run(args...)
	if code != 0 {
		w.t.Fatalf("kanjo %v exited %d: %s", args, code, errb)
	}
	return out
}

func TestSnapshotThenUsageByTaskExactTotals(t *testing.T) {
	w := newWorld(t)
	out := w.must("snapshot")
	if !strings.HasPrefix(out, "snapshot: 2 sessions, 6 rows, 4 lines skipped -> ") {
		t.Fatalf("snapshot output = %q", out)
	}
	// Hand-computed from the fixtures with the default prices (see the plan).
	want := "task,messages,input,output,reasoning,cache_write_5m,cache_write_1h,cache_read,est_usd,unpriced_messages\n" +
		"ABC-12,4,36,164,0,250,340,1003,0.0066,0\n" +
		"XYZ-3,2,90100,250,0,400,0,20300,0.0461,1\n"
	if got := w.must("usage", "--csv"); got != want {
		t.Fatalf("usage --csv =\n%s\nwant\n%s", got, want)
	}
}

func TestUsageTextShowsTotalsUnpricedAndWarnings(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	out := w.must("usage")
	for _, want := range []string{
		"total est_usd 0.0527 (1 messages unpriced)",
		"warning: session s-one mentions several tasks; counted on one",
		"estimate at API list prices, not a subscription bill",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage output lacks %q:\n%s", want, out)
		}
	}
}

func TestUsageFiltersAndGroupings(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	byDay := w.must("usage", "--by", "day", "--since", "2026-11-03", "--csv")
	if !strings.HasSuffix(byDay, "2026-11-03,2,90001,52,0,0,0,20003,0.0462,0\n") || strings.Count(byDay, "\n") != 2 {
		t.Fatalf("by day since 11-03 =\n%s", byDay)
	}
	byTask := w.must("usage", "--task", "XYZ-3", "--csv")
	if strings.Contains(byTask, "ABC-12") || !strings.Contains(byTask, "XYZ-3,2,") {
		t.Fatalf("--task XYZ-3 =\n%s", byTask)
	}
	if bySession := w.must("usage", "--by", "session", "--csv"); !strings.Contains(bySession, "\ns-one,4,") || !strings.Contains(bySession, "\ns-two,2,") {
		t.Fatalf("by session =\n%s", bySession)
	}
	if byModel := w.must("usage", "--by", "model", "--csv"); !strings.Contains(byModel, "\nclaude-unknown-9,1,100,200,0,400,0,300,,1\n") {
		t.Fatalf("an unpriced model must have an empty est_usd and count its messages:\n%s", byModel)
	}
}

func TestSnapshotTwiceChangesNothing(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	first := w.must("usage", "--by", "session", "--csv")
	w.must("snapshot")
	if second := w.must("usage", "--by", "session", "--csv"); second != first {
		t.Fatalf("second snapshot changed usage:\n%s\nvs\n%s", first, second)
	}
	s, err := sqlitestore.Open(w.data)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if rows, err := s.Rows(context.Background()); err != nil || len(rows) != 6 {
		t.Fatalf("rows = %d, err %v; want 6", len(rows), err)
	}
}

func TestSnapshotKeepsRowsOfPurgedTranscripts(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	if err := os.RemoveAll(filepath.Join(w.home, ".claude", "projects", "-home-dev-demo-sub")); err != nil {
		t.Fatal(err)
	}
	w.must("snapshot")
	if out := w.must("usage", "--csv"); !strings.Contains(out, "XYZ-3,2,") {
		t.Fatalf("usage of a purged session must stay:\n%s", out)
	}
}

func TestNoTranscriptsFolder(t *testing.T) {
	w := newWorld(t)
	if err := os.RemoveAll(filepath.Join(w.home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if out := w.must("snapshot"); !strings.HasPrefix(out, "snapshot: 0 sessions, 0 rows, 0 lines skipped") {
		t.Fatalf("snapshot output = %q", out)
	}
	if out := w.must("usage"); !strings.Contains(out, "no data; run kanjo snapshot") {
		t.Fatalf("usage output = %q", out)
	}
}

func TestUsageBeforeAnySnapshotCreatesNothing(t *testing.T) {
	w := newWorld(t)
	if out := w.must("usage"); !strings.Contains(out, "no data; run kanjo snapshot") {
		t.Fatalf("usage output = %q", out)
	}
	if _, err := os.Stat(w.data); err == nil {
		t.Fatal("usage must not create the data folder")
	}
}

func TestSnapshotWarnsAboutSkippedLines(t *testing.T) {
	w := newWorld(t)
	_, _, errb := w.run("snapshot")
	if !strings.Contains(errb, "warning: 4 transcript lines skipped") {
		t.Fatalf("stderr = %q", errb)
	}
}

func TestTaskMapFileIsUsed(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.data, "claude-tasks.tsv"), "# manual\ns-one\tMAP-1\n")
	w.must("snapshot")
	if out := w.must("usage", "--csv"); !strings.Contains(out, "\nMAP-1,4,") || strings.Contains(out, "ABC-12") {
		t.Fatalf("usage =\n%s", out)
	}
}

func TestBadTaskMapExits1NamingFileAndLine(t *testing.T) {
	w := newWorld(t)
	mapFile := filepath.Join(w.data, "claude-tasks.tsv")
	write(t, mapFile, "s-one\t../etc\n")
	code, _, errb := w.run("snapshot")
	if code != 1 || !strings.Contains(errb, mapFile) || !strings.Contains(errb, "line 1") {
		t.Fatalf("code %d stderr %q", code, errb)
	}
}

func TestBadConfigExits1(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.cwd, "kanjo.toml"), "bogus = 1\n")
	code, _, errb := w.run("snapshot")
	if code != 1 || !strings.Contains(errb, "bogus") {
		t.Fatalf("code %d stderr %q", code, errb)
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{
		{"usage", "--by", "bogus"},
		{"usage", "--since", "yesterday"},
		{"usage", "--task", "../x"},
		{"usage", "extra"},
		{"usage", "--nope"},
		{"snapshot", "extra"},
		{"snapshot", "--nope"},
	} {
		if code, _, errb := w.run(args...); code != 2 || errb == "" {
			t.Errorf("%v: code %d stderr %q, want 2 and a message", args, code, errb)
		}
	}
	if _, _, errb := w.run("usage", "stray"); !strings.Contains(errb, `"stray"`) {
		t.Errorf("the message must name the stray argument: %q", errb)
	}
}

func TestHelpFlagExits0(t *testing.T) {
	w := newWorld(t)
	for _, cmd := range []string{"snapshot", "usage"} {
		code, out, errb := w.run(cmd, "-h")
		if code != 0 || !strings.Contains(errb, "Usage") || out != "" {
			t.Errorf("%s -h: code %d stdout %q stderr %q", cmd, code, out, errb)
		}
	}
	if _, err := os.Stat(w.data); err == nil {
		t.Error("-h must not run the command")
	}
}

func TestNoArgumentsExits2(t *testing.T) {
	w := newWorld(t)
	if code, out, errb := w.run(); code != 2 || out != "" || !strings.Contains(errb, "commands: prices, self-update, serve, snapshot, usage, version") {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errb)
	}
}

func TestSkippedWarningAppearsWithOneSkippedLineOnly(t *testing.T) {
	w := newWorld(t)
	projects := filepath.Join(w.home, ".claude", "projects")
	write(t, filepath.Join(projects, "-clean-a", "s-ok.jsonl"), `{"type":"summary"}`+"\n")
	write(t, filepath.Join(projects, "-clean-b", "s-ok.jsonl"), `{"type":"summary"}`+"\nnot json\n")
	for prefix, wantWarning := range map[string]bool{"-clean-a": false, "-clean-b": true} {
		write(t, filepath.Join(w.home, ".config", "kanjo", "config.toml"),
			"data_dir = \""+filepath.Join(w.data, prefix)+"\"\n[claude]\nproject_prefixes = [\""+prefix+"\"]\n")
		_, _, errb := w.run("snapshot")
		if got := strings.Contains(errb, "warning:"); got != wantWarning {
			t.Errorf("%s: warning = %v, want %v (stderr %q)", prefix, got, wantWarning, errb)
		}
	}
}

func TestDataDirFlagBeatsConfig(t *testing.T) {
	w := newWorld(t)
	other := filepath.Join(t.TempDir(), "other")
	w.must("snapshot", "--data-dir", other)
	if _, err := os.Stat(filepath.Join(other, "kanjo.db")); err != nil {
		t.Fatalf("database not in the flag folder: %v", err)
	}
	if _, err := os.Stat(w.data); err == nil {
		t.Fatal("the configured folder must stay untouched")
	}
	if out := w.must("usage", "--csv", "--data-dir", other); !strings.Contains(out, "ABC-12") {
		t.Fatalf("usage =\n%s", out)
	}
}

func TestPricesFileIsUsed(t *testing.T) {
	w := newWorld(t)
	prices := filepath.Join(t.TempDir(), "prices.json")
	write(t, prices, `{"source":"s","fetched":"2026-11-03","unit":"u","models":{"claude-opus-5-5":{"input":1000000,"output":0,"cache_write_5m":0,"cache_write_1h":0,"cache_read":0}}}`)
	cfg, _ := os.ReadFile(filepath.Join(w.home, ".config", "kanjo", "config.toml"))
	write(t, filepath.Join(w.home, ".config", "kanjo", "config.toml"), "prices_file = \""+prices+"\"\n"+string(cfg))
	w.must("snapshot")
	// opus input tokens: 10 + 1 = 11, at $1 per token.
	if out := w.must("usage", "--by", "model", "--csv"); !strings.Contains(out, "claude-opus-5-5,2,11,122,0,200,300,1003,11.0000,0") {
		t.Fatalf("usage =\n%s", out)
	}
}

func TestCanaryNeverInOutputOrDatabase(t *testing.T) {
	w := newWorld(t)
	_, snapOut, snapErr := w.run("snapshot")
	var all strings.Builder
	all.WriteString(snapOut + snapErr)
	for _, by := range []string{"task", "model", "day", "session"} {
		_, out, errb := w.run("usage", "--by", by)
		all.WriteString(out + errb)
		_, out, errb = w.run("usage", "--by", by, "--csv")
		all.WriteString(out + errb)
	}
	db, err := os.ReadFile(filepath.Join(w.data, "kanjo.db"))
	if err != nil {
		t.Fatal(err)
	}
	all.Write(db)
	if strings.Contains(all.String(), "CANARY") {
		t.Fatal("transcript text leaked into output or the database")
	}
}

func TestPartlyPurgedSessionNeverShrinksSavedUsage(t *testing.T) {
	w := newWorld(t)
	msg := func(id string, in int) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-11-03T10:00:00Z","message":{"id":%q,"model":"claude-opus-5-5","usage":{"input_tokens":%d,"output_tokens":1}}}`, id, in)
	}
	file := filepath.Join(w.home, ".claude", "projects", "-home-dev-demo", "s-big.jsonl")
	write(t, file, msg("a", 10)+"\n"+msg("b", 20)+"\n"+msg("c", 30)+"\n")
	w.must("snapshot")
	write(t, file, msg("c", 30)+"\n") // the older messages were purged
	w.must("snapshot")
	if out := w.must("usage", "--by", "session", "--csv"); !strings.Contains(out, "\ns-big,3,60,3,") {
		t.Fatalf("saved usage shrank:\n%s", out)
	}
}

func TestUsageCSVWithNoDataKeepsStdoutMachineReadable(t *testing.T) {
	w := newWorld(t)
	code, out, errb := w.run("usage", "--by", "model", "--csv")
	wantHeader := "model,messages,input,output,reasoning,cache_write_5m,cache_write_1h,cache_read,est_usd,unpriced_messages\n"
	if code != 0 || out != wantHeader || !strings.Contains(errb, "no data; run kanjo snapshot") {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errb)
	}
}

func TestUsageWorksOnAReadOnlyDataFolder(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	before := w.must("usage", "--csv")
	if err := os.Chmod(filepath.Join(w.data, "kanjo.db"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(w.data, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(w.data, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	code, out, errb := w.run("usage", "--csv")
	if code != 0 || out != before {
		t.Fatalf("reading must not need write access: code %d stderr %q", code, errb)
	}
	entries, _ := os.ReadDir(w.data)
	if len(entries) != 2 {
		t.Fatalf("usage left %d entries in the data folder, want only kanjo.db and .gitignore", len(entries))
	}
}

// Paths come from config and may hold terminal control sequences (for example
// an OSC 52 clipboard write): none may reach the terminal raw.
func TestPrintedPathsCannotCarryTerminalEscapes(t *testing.T) {
	w := newWorld(t)
	evil := filepath.Join(filepath.Dir(w.data), "x\x1b[31m\x07")
	cfg := "data_dir = \"" + strings.NewReplacer("\x1b", `\u001b`, "\x07", `\u0007`).Replace(evil) + "\"\n[claude]\nproject_prefixes = [\"-home-dev-demo\"]\n"
	write(t, filepath.Join(w.home, ".config", "kanjo", "config.toml"), cfg)
	_, out, errb := w.run("snapshot")
	if strings.ContainsAny(out+errb, "\x1b\x07") || !strings.Contains(out, "x?[31m?") {
		t.Fatalf("stdout %q stderr %q", out, errb)
	}
	// An error message that names the path is cleaned too.
	if err := os.WriteFile(filepath.Join(evil, ".gitignore"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb := w.run("snapshot")
	if code != 1 || strings.ContainsAny(out+errb, "\x1b\x07") || !strings.Contains(errb, "x?[31m?") {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errb)
	}
}

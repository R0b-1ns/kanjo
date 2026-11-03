package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

const fixtures = "../../testdata/transcripts"

var defaultPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+(\.[0-9]+)?\b`)

func longHaiku(model string, prompt int64) bool {
	return model == "claude-haiku-5-5" && prompt > 100000
}

func fixtureSource() Source {
	return Source{
		ProjectsDir:  fixtures,
		Prefixes:     []string{"-home-dev-demo"},
		TaskPattern:  defaultPattern,
		MaxLineBytes: 1 << 20,
		LongContext:  longHaiku,
	}
}

func readOK(t *testing.T, s Source) core.Batch {
	t.Helper()
	b, err := s.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return b
}

func key(session, day, task, model string, long bool) core.Key {
	return core.Key{Source: "claude", Session: session, Day: day, Task: task, Model: model, LongContext: long}
}

func TestSourceContract(t *testing.T) {
	kanjotest.UsageSourceContract(t, fixtureSource())
}

func TestReadFixturesExactRows(t *testing.T) {
	b := readOK(t, fixtureSource())
	want := []core.UsageRow{
		{Key: key("s-one", "2026-11-02", "ABC-12", "claude-haiku-5-5", false), Messages: 1, Tokens: core.Tokens{Input: 5, Output: 7, CacheWrite1h: 40}},
		{Key: key("s-one", "2026-11-02", "ABC-12", "claude-opus-5-5", false), Messages: 1, Tokens: core.Tokens{Input: 10, Output: 120, CacheWrite5m: 200, CacheWrite1h: 300, CacheRead: 1000}},
		{Key: key("s-one", "2026-11-02", "ABC-12", "claude-sonnet-5-5", false), Messages: 1, Tokens: core.Tokens{Input: 20, Output: 35, CacheWrite5m: 50}},
		{Key: key("s-one", "2026-11-03", "ABC-12", "claude-opus-5-5", false), Messages: 1, Tokens: core.Tokens{Input: 1, Output: 2, CacheRead: 3}},
		{Key: key("s-two", "2026-11-02", "XYZ-3", "claude-unknown-9", false), Messages: 1, Tokens: core.Tokens{Input: 100, Output: 200, CacheWrite5m: 400, CacheRead: 300}},
		{Key: key("s-two", "2026-11-03", "XYZ-3", "claude-haiku-5-5", true), Messages: 1, Tokens: core.Tokens{Input: 90000, Output: 50, CacheRead: 20000}},
	}
	if !reflect.DeepEqual(b.Rows, want) {
		t.Fatalf("rows:\n got %+v\nwant %+v", b.Rows, want)
	}
	wantSessions := []core.SessionInfo{
		{Source: "claude", Session: "s-one", TaskSource: "heuristic", LabelsSeen: 2},
		{Source: "claude", Session: "s-two", TaskSource: "heuristic", LabelsSeen: 1},
	}
	if !reflect.DeepEqual(b.Sessions, wantSessions) {
		t.Fatalf("sessions: got %+v, want %+v", b.Sessions, wantSessions)
	}
	// s-one: corrupt line, message without timestamp, negative tokens; s-two: truncated last line.
	if b.Skipped != 4 {
		t.Fatalf("Skipped = %d, want 4", b.Skipped)
	}
}

func TestPrefixMismatchIsNotRead(t *testing.T) {
	for _, r := range readOK(t, fixtureSource()).Rows {
		if r.Session == "s-three" {
			t.Fatal("a project folder outside the prefixes was read")
		}
	}
}

func TestCanaryNeverInBatch(t *testing.T) {
	if out := fmt.Sprintf("%+v", readOK(t, fixtureSource())); strings.Contains(out, "CANARY") {
		t.Fatal("transcript text leaked into the batch")
	}
}

func TestTaskMapBeatsHeuristic(t *testing.T) {
	s := fixtureSource()
	s.TaskMap = map[string]string{"s-": "MAP-1", "s-t": "MAP-2"}
	b := readOK(t, s)
	got := map[string]string{}
	for _, r := range b.Rows {
		got[r.Session] = r.Task
	}
	if got["s-one"] != "MAP-1" || got["s-two"] != "MAP-2" {
		t.Fatalf("tasks = %v; the longest matching prefix must win", got)
	}
	if b.Sessions[0].TaskSource != "map" || b.Sessions[0].LabelsSeen != 2 {
		t.Fatalf("session = %+v: a mapped session still reports the labels it mentioned", b.Sessions[0])
	}
}

func TestNoPatternMeansNoTask(t *testing.T) {
	s := fixtureSource()
	s.TaskPattern = nil
	b := readOK(t, s)
	if b.Rows[0].Task != core.NoTask || b.Sessions[0].TaskSource != "none" || b.Sessions[0].LabelsSeen != 0 {
		t.Fatalf("row %+v session %+v", b.Rows[0], b.Sessions[0])
	}
}

func TestInvalidTaskMapEntryFails(t *testing.T) {
	s := fixtureSource()
	s.TaskMap = map[string]string{"s-": "../etc"}
	if _, err := s.Read(context.Background()); err == nil {
		t.Fatal("expected an error for an unsafe task label in the map")
	}
}

func TestZeroMaxLineBytesFails(t *testing.T) {
	s := fixtureSource()
	s.MaxLineBytes = 0
	if _, err := s.Read(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestMissingProjectsDirIsEmpty(t *testing.T) {
	s := fixtureSource()
	s.ProjectsDir = filepath.Join(t.TempDir(), "absent")
	b := readOK(t, s)
	if len(b.Rows) != 0 || len(b.Sessions) != 0 || b.Skipped != 0 {
		t.Fatalf("batch = %+v", b)
	}
}

func TestCancelledContextFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixtureSource().Read(ctx); err == nil {
		t.Fatal("expected a context error")
	}
}

// --- generated transcripts ---

func asst(ts, id, model string, in int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":1}}}`, ts, id, model, in)
}

func write(t *testing.T, root, rel string, lines ...string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func tempSource(root string) Source {
	return Source{ProjectsDir: root, Prefixes: []string{"-p"}, MaxLineBytes: 4096}
}

func TestSameSessionUnderTwoPrefixes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s-dup.jsonl", asst("2026-11-03T10:00:00Z", "m1", "m", 1))
	write(t, root, "-p-sub/s-dup.jsonl", asst("2026-11-03T10:00:00Z", "m1", "m", 1), asst("2026-11-03T10:01:00Z", "m2", "m", 2))
	b := readOK(t, tempSource(root))
	if len(b.Sessions) != 1 || len(b.Rows) != 1 || b.Rows[0].Messages != 2 || b.Rows[0].Input != 3 {
		t.Fatalf("batch = %+v: one session, message ids deduped across both folders", b)
	}
}

func TestDayIsUTC(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s-tz.jsonl", asst("2026-11-02T23:30:00-05:00", "m1", "m", 1))
	if got := readOK(t, tempSource(root)).Rows[0].Day; got != "2026-11-03" {
		t.Fatalf("day = %s, want the UTC day 2026-11-03", got)
	}
}

func TestSkipsSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	real := write(t, outside, "real.jsonl", asst("2026-11-03T10:00:00Z", "m1", "m", 1))
	write(t, outside, "-p-linked/s-x.jsonl", asst("2026-11-03T10:00:00Z", "m2", "m", 1))
	if err := os.MkdirAll(filepath.Join(root, "-p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "-p", "s-link.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "-p-linked"), filepath.Join(root, "-p-linked")); err != nil {
		t.Fatal(err)
	}
	if b := readOK(t, tempSource(root)); len(b.Rows) != 0 {
		t.Fatalf("symlinked files or folders were followed: %+v", b.Rows)
	}
}

func TestOversizedLineIsSkippedAndCounted(t *testing.T) {
	root := t.TempDir()
	s := tempSource(root)
	s.MaxLineBytes = 300
	pad := `{"type":"summary","pad":"` + strings.Repeat("x", 400) + `"}`
	write(t, root, "-p/s-big.jsonl", pad, asst("2026-11-03T10:00:00Z", "m1", "m", 1))
	b := readOK(t, s)
	if len(b.Rows) != 1 || b.Skipped != 1 {
		t.Fatalf("rows %d skipped %d", len(b.Rows), b.Skipped)
	}
}

func TestInvalidSessionAndModelAreSkipped(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/bad name.jsonl", asst("2026-11-03T10:00:00Z", "m1", "m", 1))
	write(t, root, "-p/s-ok.jsonl", asst("2026-11-03T10:00:00Z", "m2", "bad model!", 1), asst("2026-11-03T10:00:00Z", "m3", "good", 1))
	b := readOK(t, tempSource(root))
	if len(b.Sessions) != 1 || len(b.Rows) != 1 || b.Rows[0].Model != "good" || b.Skipped != 2 {
		t.Fatalf("batch = %+v", b)
	}
}

func TestUnreadableFileIsSkippedAndCounted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	p := write(t, root, "-p/s-locked.jsonl", asst("2026-11-03T10:00:00Z", "m1", "m", 1))
	write(t, root, "-p/s-fine.jsonl", asst("2026-11-03T10:00:00Z", "m2", "m", 1))
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o644) })
	b := readOK(t, tempSource(root))
	if len(b.Rows) != 1 || b.Rows[0].Session != "s-fine" || b.Skipped != 1 {
		t.Fatalf("batch = %+v", b)
	}
}

func TestTieBetweenLabelsPicksSmallest(t *testing.T) {
	root := t.TempDir()
	s := tempSource(root)
	s.TaskPattern = defaultPattern
	write(t, root, "-p/s-tie.jsonl",
		`{"type":"user","timestamp":"2026-11-03T10:00:00Z","message":{"content":[{"type":"text","text":"ZZZ-1 and AAA-2"}]}}`,
		asst("2026-11-03T10:00:01Z", "m1", "m", 1))
	if got := readOK(t, s).Rows[0].Task; got != "AAA-2" {
		t.Fatalf("task = %s, want AAA-2", got)
	}
}

func TestSessionWithoutUsableRowsIsNotListed(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s-empty.jsonl", `{"type":"summary"}`, "not json")
	b := readOK(t, tempSource(root))
	if len(b.Sessions) != 0 || b.Skipped != 1 {
		t.Fatalf("a session with no rows must not be listed (it would be replaced by nothing): %+v", b)
	}
}

func TestReadTaskMap(t *testing.T) {
	m, err := ReadTaskMap(strings.NewReader("# comment\n\nabc12\tABC-12\ndef\tXYZ-3/part\n"))
	if err != nil || !reflect.DeepEqual(m, map[string]string{"abc12": "ABC-12", "def": "XYZ-3/part"}) {
		t.Fatalf("map = %v, err %v", m, err)
	}
}

func TestReadTaskMapErrorNamesTheLine(t *testing.T) {
	_, err := ReadTaskMap(strings.NewReader("# comment\nabc ABC-12\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2:") {
		t.Fatalf("err = %v, want it to name line 2", err)
	}
}

func TestReadTaskMapRejectsBadLines(t *testing.T) {
	for name, in := range map[string]string{
		"unsafe task":    "abc\t../etc\n",
		"no tab":         "abc ABC-12\n",
		"three fields":   "abc\tABC-12\textra\n",
		"bad session":    "a b\tABC-12\n",
		"empty prefix":   "\tABC-12\n",
		"duplicate line": "abc\tABC-12\nabc\tXYZ-3\n",
		"dash task":      "abc\t-\n",
	} {
		_, err := ReadTaskMap(strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), "line") {
			t.Errorf("%s: err = %v, want an error naming the line", name, err)
		}
	}
}

func TestAssistantLinesWithoutUsageOrIDAreIgnoredNotSkipped(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s-1.jsonl",
		`{"type":"assistant","timestamp":"2026-11-03T10:00:00Z"}`,
		`{"type":"assistant","timestamp":"2026-11-03T10:00:00Z","message":{"id":"m1","model":"m"}}`,
		`{"type":"assistant","timestamp":"2026-11-03T10:00:00Z","message":{"model":"m","usage":{"input_tokens":1}}}`,
		`{"type":"assistant","timestamp":"2026-11-03T10:00:00Z","message":{"id":"m3","model":"<synthetic>","usage":{"input_tokens":1}}}`,
		asst("2026-11-03T10:00:00Z", "m4", "m", 1))
	b := readOK(t, tempSource(root))
	if len(b.Rows) != 1 || b.Rows[0].Messages != 1 || b.Skipped != 0 {
		t.Fatalf("batch = %+v", b)
	}
}

func TestPromptCountsEveryInputKind(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s-1.jsonl", `{"type":"assistant","timestamp":"2026-11-03T10:00:00Z","message":{"id":"m1","model":"m","usage":{"input_tokens":1,"cache_read_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":1000}}}}`)
	var prompt int64
	s := tempSource(root)
	s.LongContext = func(_ string, n int64) bool { prompt = n; return false }
	readOK(t, s)
	if prompt != 1111 {
		t.Fatalf("prompt = %d, want 1111 (input + cache read + both cache writes)", prompt)
	}
}

func TestOnlyJSONLFilesAreRead(t *testing.T) {
	root := t.TempDir()
	line := asst("2026-11-03T10:00:00Z", "m1", "m", 1)
	write(t, root, "-p/s-1.jsonl", line)
	write(t, root, "-p/s-1.meta.json", asst("2026-11-03T10:00:00Z", "m2", "m", 1))
	write(t, root, "-p/s-1/subagents/agent-a.jsonl", asst("2026-11-03T10:00:00Z", "m3", "m", 1))
	write(t, root, "-p/s-1/subagents/agent-a.meta.json", asst("2026-11-03T10:00:00Z", "m4", "m", 1))
	if b := readOK(t, tempSource(root)); b.Rows[0].Messages != 2 {
		t.Fatalf("messages = %d, want 2 (only .jsonl files)", b.Rows[0].Messages)
	}
}

func TestSmallestLineLimitIsAccepted(t *testing.T) {
	root := t.TempDir()
	write(t, root, "-p/s-1.jsonl", asst("2026-11-03T10:00:00Z", "m1", "m", 1))
	s := tempSource(root)
	s.MaxLineBytes = 1
	if b := readOK(t, s); b.Skipped != 1 {
		t.Fatalf("Skipped = %d, want the one oversized line", b.Skipped)
	}
}

func TestMostFrequentLabelWins(t *testing.T) {
	root := t.TempDir()
	s := tempSource(root)
	s.TaskPattern = defaultPattern
	write(t, root, "-p/s-1.jsonl",
		`{"type":"user","timestamp":"2026-11-03T10:00:00Z","message":{"content":"AAA-1 ZZZ-9 ZZZ-9"}}`,
		asst("2026-11-03T10:00:01Z", "m1", "m", 1))
	if got := readOK(t, s).Rows[0].Task; got != "ZZZ-9" {
		t.Fatalf("task = %s, want the most frequent label ZZZ-9", got)
	}
}

func TestTaskMapEntriesMustBeUsable(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"empty prefix": {"": "ABC-1"},
		"dash task":    {"s-": core.NoTask},
		"unsafe task":  {"s-": "a b"},
	} {
		s := fixtureSource()
		s.TaskMap = m
		if _, err := s.Read(context.Background()); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestPrefixMatchesWholeNameOrSubFolderNames(t *testing.T) {
	root := t.TempDir()
	for i, dir := range []string{"-p", "-p-sub", "-p2", "-pp-sub", "-q"} {
		write(t, root, dir+"/s-"+strconv.Itoa(i)+".jsonl", asst("2026-11-03T10:00:00Z", "m"+strconv.Itoa(i), "m", 1))
	}
	read := func(prefix string) []string {
		s := tempSource(root)
		s.Prefixes = []string{prefix}
		var sessions []string
		for _, si := range readOK(t, s).Sessions {
			sessions = append(sessions, si.Session)
		}
		return sessions
	}
	if got := read("-p"); !reflect.DeepEqual(got, []string{"s-0", "s-1"}) {
		t.Errorf("prefix -p read %v, want the folder itself and -p-sub only (not -p2 or -pp-sub)", got)
	}
	if got := read("-p-"); !reflect.DeepEqual(got, []string{"s-1"}) {
		t.Errorf("prefix -p- read %v, want only -p-sub", got)
	}
	if got := read("-"); len(got) != 5 {
		t.Errorf("prefix - must read every folder, read %v", got)
	}
}

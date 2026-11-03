package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type env struct{ home, cwd, xdg string }

func newEnv(t *testing.T) env {
	t.Helper()
	root := t.TempDir()
	return env{home: filepath.Join(root, "home"), cwd: filepath.Join(root, "work", "my.proj"), xdg: ""}
}

func (e env) load(t *testing.T) (Config, error) {
	t.Helper()
	return Load(e.home, e.cwd, e.xdg, Overrides{})
}

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	e := newEnv(t)
	c, err := e.load(t)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		DataDir:    filepath.Join(e.cwd, ".ai-usage"),
		PricesFile: "",
		Claude: ClaudeConfig{
			ProjectsDir:     filepath.Join(e.home, ".claude", "projects"),
			ProjectPrefixes: []string{ProjectFolderName(e.cwd)},
			MaxLineBytes:    67108864,
		},
		Task: TaskConfig{Pattern: DefaultTaskPattern, MapFile: filepath.Join(e.cwd, ".ai-usage", "claude-tasks.tsv")},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got  %+v\nwant %+v", c, want)
	}
}

func TestProjectFolderName(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/a/Project/x": "-Users-a-Project-x",
		"/a/.hidden/b_c d":   "-a--hidden-b-c-d",
		"/already-dashed/p":  "-already-dashed-p",
		"/ünï/p":             "--n--p",
	} {
		if got := ProjectFolderName(in); got != want {
			t.Errorf("ProjectFolderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepoFileOverridesUserFile(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"),
		"data_dir = \"/user/data\"\n[claude]\nmax_line_bytes = 1000\nproject_prefixes = [\"-a\", \"-b\"]\n")
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "data_dir = \"/repo/data\"\n")
	c, err := e.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/repo/data" || c.Claude.MaxLineBytes != 1000 || !reflect.DeepEqual(c.Claude.ProjectPrefixes, []string{"-a", "-b"}) {
		t.Fatalf("config = %+v", c)
	}
}

func TestXDGConfigHomeReplacesDotConfig(t *testing.T) {
	e := newEnv(t)
	e.xdg = filepath.Join(t.TempDir(), "xdg")
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "data_dir = \"/ignored\"\n")
	put(t, filepath.Join(e.xdg, "kanjo", "config.toml"), "data_dir = \"/from-xdg\"\n")
	c, err := e.load(t)
	if err != nil || c.DataDir != "/from-xdg" {
		t.Fatalf("data dir = %q, err %v", c.DataDir, err)
	}
}

func TestRelativePathsResolveAgainstCwd(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "data_dir = \"d\"\nprices_file = \"p/prices.json\"\n[task]\nmap_file = \"m.tsv\"\n")
	c, err := e.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != filepath.Join(e.cwd, "d") || c.PricesFile != filepath.Join(e.cwd, "p", "prices.json") || c.Task.MapFile != filepath.Join(e.cwd, "m.tsv") {
		t.Fatalf("config = %+v", c)
	}
}

func TestMapFileDefaultsUnderDataDir(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "data_dir = \"/abs/usage\"\n")
	c, err := e.load(t)
	if err != nil || c.Task.MapFile != "/abs/usage/claude-tasks.tsv" {
		t.Fatalf("map file = %q, err %v", c.Task.MapFile, err)
	}
}

func TestEmptyPatternDisablesHeuristic(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "[task]\npattern = \"\"\n")
	c, err := e.load(t)
	if err != nil || c.Task.Pattern != "" {
		t.Fatalf("pattern = %q, err %v", c.Task.Pattern, err)
	}
}

func TestInvalidConfigIsRejected(t *testing.T) {
	cases := map[string]string{
		"unknown key":       "bogus = 1\n",
		"unknown table key": "[claude]\nnope = 1\n",
		"bad pattern":       "[task]\npattern = \"(\"\n",
		"zero line limit":   "[claude]\nmax_line_bytes = 0\n",
		"negative limit":    "[claude]\nmax_line_bytes = -5\n",
		"empty prefix":      "[claude]\nproject_prefixes = [\"\"]\n",
		"no prefixes":       "[claude]\nproject_prefixes = []\n",
		"empty data dir":    "data_dir = \"\"\n",
		"malformed toml":    "data_dir = \n",
		"wrong type":        "data_dir = 5\n",
	}
	for name, content := range cases {
		e := newEnv(t)
		file := filepath.Join(e.cwd, "kanjo.toml")
		put(t, file, content)
		_, err := e.load(t)
		if err == nil {
			t.Errorf("%s: expected an error", name)
		} else if !strings.Contains(err.Error(), file) {
			t.Errorf("%s: error %q must name the file", name, err)
		}
	}
}

func TestUnknownKeyErrorNamesTheKey(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "[claude]\nnope = 1\n")
	if _, err := e.load(t); err == nil || !strings.Contains(err.Error(), "claude.nope") {
		t.Fatalf("err = %v, want it to name claude.nope", err)
	}
}

func TestOversizedConfigFileIsRefused(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "# "+strings.Repeat("x", maxFileBytes)+"\n")
	if _, err := e.load(t); err == nil {
		t.Fatal("expected an error")
	}
}

func TestUnreadableConfigFileIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	e := newEnv(t)
	file := filepath.Join(e.cwd, "kanjo.toml")
	put(t, file, "data_dir = \"x\"\n")
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(file, 0o644) })
	if _, err := e.load(t); err == nil {
		t.Fatal("a config file that exists but cannot be read must not be ignored")
	}
}

func TestProjectFolderNameBoundaries(t *testing.T) {
	if got := ProjectFolderName("azAZ09"); got != "azAZ09" {
		t.Errorf("letters and digits must be kept, got %q", got)
	}
	if got := ProjectFolderName("`{@[/:"); got != "------" {
		t.Errorf("characters next to the letter and digit ranges must be replaced, got %q", got)
	}
}

func TestLimitsAreInclusive(t *testing.T) {
	e := newEnv(t)
	body := "[claude]\nmax_line_bytes = 1\n"
	pad := "#" + strings.Repeat("x", maxFileBytes-len(body)-2) + "\n"
	put(t, filepath.Join(e.cwd, "kanjo.toml"), body+pad)
	if fi, _ := os.Stat(filepath.Join(e.cwd, "kanjo.toml")); fi.Size() != maxFileBytes {
		t.Fatalf("test file is %d bytes, want exactly %d", fi.Size(), maxFileBytes)
	}
	c, err := e.load(t)
	if err != nil || c.Claude.MaxLineBytes != 1 {
		t.Fatalf("a file of exactly the size limit and max_line_bytes = 1 must be accepted: %v", err)
	}
}

func TestDataDirOverrideBeatsFilesAndMovesTaskMap(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "data_dir = \"/from/file\"\n")
	c, err := Load(e.home, e.cwd, e.xdg, Overrides{DataDir: "flag-dir"})
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != filepath.Join(e.cwd, "flag-dir") || c.Task.MapFile != filepath.Join(e.cwd, "flag-dir", "claude-tasks.tsv") {
		t.Fatalf("config = %+v", c)
	}
}

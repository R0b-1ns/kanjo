package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
		Task:   TaskConfig{Pattern: DefaultTaskPattern, Ignore: DefaultTaskIgnore, MapFile: filepath.Join(e.cwd, ".ai-usage", "claude-tasks.tsv")},
		Serve:  ServeConfig{Port: 8787},
		Prices: PricesConfig{MaxAgeDays: 30}, Markers: MarkersConfig{MaxEvents: 100000},
		Delegate: DelegateConfig{TimeoutS: 900, CommandTimeoutS: 600, KillGraceS: 5, MaxLogBytes: 1 << 20, MaxPatchBytes: 16 << 20, MaxPromptBytes: 1 << 20, EnvPass: []string{}},
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
	repoData := filepath.Join(e.cwd, "repo-data")
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "data_dir = \""+repoData+"\"\n")
	c, err := e.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != repoData || c.Claude.MaxLineBytes != 1000 || !reflect.DeepEqual(c.Claude.ProjectPrefixes, []string{"-a", "-b"}) {
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
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "data_dir = \"/abs/usage\"\n")
	c, err := e.load(t)
	if err != nil || c.Task.MapFile != "/abs/usage/claude-tasks.tsv" {
		t.Fatalf("map file = %q, err %v", c.Task.MapFile, err)
	}
}

func TestEmptyPatternDisablesHeuristic(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[task]\npattern = \"\"\n")
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
		file := filepath.Join(e.home, ".config", "kanjo", "config.toml")
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
	userFile := filepath.Join(e.home, ".config", "kanjo", "config.toml")
	put(t, userFile, body+pad)
	if fi, _ := os.Stat(userFile); fi.Size() != maxFileBytes {
		t.Fatalf("test file is %d bytes, want exactly %d", fi.Size(), maxFileBytes)
	}
	c, err := e.load(t)
	if err != nil || c.Claude.MaxLineBytes != 1 {
		t.Fatalf("a file of exactly the size limit and max_line_bytes = 1 must be accepted: %v", err)
	}
}

func TestDataDirOverrideBeatsFilesAndMovesTaskMap(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "data_dir = \"/from/file\"\n")
	c, err := Load(e.home, e.cwd, e.xdg, Overrides{DataDir: "flag-dir"})
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != filepath.Join(e.cwd, "flag-dir") || c.Task.MapFile != filepath.Join(e.cwd, "flag-dir", "claude-tasks.tsv") {
		t.Fatalf("config = %+v", c)
	}
}

// A kanjo.toml in the current folder comes from whatever repository the user
// is standing in: it must not redirect where data goes or what is read.
func TestRepoConfigCannotReachOutsideItsFolder(t *testing.T) {
	cases := map[string]string{
		"absolute data dir":  "data_dir = \"/tmp/elsewhere\"\n",
		"parent data dir":    "data_dir = \"../elsewhere\"\n",
		"escaping data dir":  "data_dir = \"a/../../elsewhere\"\n",
		"absolute prices":    "prices_file = \"/etc/passwd\"\n",
		"absolute task map":  "[task]\nmap_file = \"/tmp/map.tsv\"\n",
		"projects dir":       "[claude]\nprojects_dir = \"/somewhere\"\n",
		"label pattern":      "[task]\npattern = \"ghp_[A-Za-z0-9]{36}\"\n",
		"line limit":         "[claude]\nmax_line_bytes = 9223372036854775807\n",
		"generic prefix":     "[claude]\nproject_prefixes = [\"-\"]\n",
		"foreign prefix":     "[claude]\nproject_prefixes = [\"-other-project\"]\n",
		"one foreign of two": "[claude]\nproject_prefixes = [\"-other\"]\n",
	}
	for name, content := range cases {
		e := newEnv(t)
		file := filepath.Join(e.cwd, "kanjo.toml")
		put(t, file, content)
		_, err := e.load(t)
		if err == nil || !strings.Contains(err.Error(), file) {
			t.Errorf("%s: err = %v, want a refusal naming %s", name, err, file)
		}
	}
}

func TestRepoConfigMayStayInsideItsFolder(t *testing.T) {
	e := newEnv(t)
	own := ProjectFolderName(e.cwd)
	put(t, filepath.Join(e.cwd, "kanjo.toml"),
		"data_dir = \"usage\"\nprices_file = \""+filepath.Join(e.cwd, "p.json")+"\"\n[task]\nmap_file = \"a/b.tsv\"\n[claude]\nproject_prefixes = [\""+own+"\", \""+own+"-sub\"]\n")
	c, err := e.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Claude.ProjectPrefixes, []string{own, own + "-sub"}) || c.DataDir != filepath.Join(e.cwd, "usage") {
		t.Fatalf("config = %+v", c)
	}
}

func TestUserConfigAndFlagsAreTrusted(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"),
		"data_dir = \"/srv/usage\"\nprices_file = \"/srv/p.json\"\n[task]\nmap_file = \"/srv/m.tsv\"\n[claude]\nprojects_dir = \"/srv/projects\"\nproject_prefixes = [\"-\"]\n")
	c, err := e.load(t)
	if err != nil {
		t.Fatalf("the user's own config may point anywhere: %v", err)
	}
	if c.Claude.ProjectsDir != "/srv/projects" || c.DataDir != "/srv/usage" {
		t.Fatalf("config = %+v", c)
	}
	if c, err = Load(e.home, e.cwd, e.xdg, Overrides{DataDir: "/tmp/flag"}); err != nil || c.DataDir != "/tmp/flag" {
		t.Fatalf("a flag may point anywhere: %+v, %v", c, err)
	}
}

func TestRepoPathMayNameItsOwnFolder(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "data_dir = \".\"\n")
	if c, err := e.load(t); err != nil || c.DataDir != e.cwd {
		t.Fatalf("data_dir = . must be accepted: %+v, %v", c, err)
	}
}

func TestRepoConfigCannotEscapeThroughASymlink(t *testing.T) {
	cases := map[string]string{
		"link as data dir":      "data_dir = \"link\"\n",
		"under a link":          "data_dir = \"link/data\"\n",
		"prices under a link":   "prices_file = \"link/prices.json\"\n",
		"task map under a link": "[task]\nmap_file = \"link/m.tsv\"\n",
		"dangling link":         "data_dir = \"dangling/data\"\n",
	}
	for name, content := range cases {
		e := newEnv(t)
		outside := t.TempDir()
		if err := os.MkdirAll(e.cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(e.cwd, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "not-yet"), filepath.Join(e.cwd, "dangling")); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(e.cwd, "kanjo.toml")
		put(t, file, content)
		if _, err := e.load(t); err == nil || !strings.Contains(err.Error(), file) {
			t.Errorf("%s: err = %v, want a refusal naming %s", name, err, file)
		}
	}
}

func TestRepoConfigMayUseASymlinkThatStaysInside(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Join(e.cwd, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.cwd, "real"), filepath.Join(e.cwd, "inner")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "data_dir = \"inner/data\"\n")
	if _, err := e.load(t); err != nil {
		t.Fatalf("a link that stays inside the folder is fine: %v", err)
	}
}

func TestRepoPrefixMustBeTheFolderNameOrASubName(t *testing.T) {
	for prefix, ok := range map[string]bool{"": true, "-sub": true, "2": false, "x": false, "-": true} {
		e := newEnv(t)
		own := ProjectFolderName(e.cwd)
		file := filepath.Join(e.cwd, "kanjo.toml")
		put(t, file, "[claude]\nproject_prefixes = [\""+own+prefix+"\"]\n")
		_, err := e.load(t)
		if (err == nil) != ok {
			t.Errorf("prefix %q+%q: err = %v, want accepted=%v", own, prefix, err, ok)
		}
	}
}

func TestRepoConfigMayNotSetUserOnlyKeys(t *testing.T) {
	// The label pattern could copy a secret typed in a prompt into the database.
	for _, content := range []string{"[task]\npattern = \"x\"\n", "[claude]\nmax_line_bytes = 5\n", "[claude]\nprojects_dir = \"x\"\n"} {
		e := newEnv(t)
		file := filepath.Join(e.cwd, "kanjo.toml")
		put(t, file, content)
		if _, err := e.load(t); err == nil || !strings.Contains(err.Error(), "user config") {
			t.Errorf("%q: err = %v, want it to say the key is for the user config", content, err)
		}
	}
	e := newEnv(t)
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[task]\npattern = \"TASK-[0-9]+\"\n[claude]\nmax_line_bytes = 5\n")
	if c, err := e.load(t); err != nil || c.Task.Pattern != "TASK-[0-9]+" || c.Claude.MaxLineBytes != 5 {
		t.Fatalf("the user config may set them: %+v, %v", c, err)
	}
}

func TestTaskIgnoreDefaultsUserOnlyAndNotEmptyEntries(t *testing.T) {
	e := newEnv(t)
	c, err := e.load(t)
	if err != nil || !slices.Contains(c.Task.Ignore, "UTF-8") || !slices.Contains(c.Task.Ignore, "SHA-256") {
		t.Fatalf("the default ignore list must hold the usual false positives: %v, %v", c.Task.Ignore, err)
	}
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[task]\nignore = [\"CVE-2024\"]\n")
	if c, err = e.load(t); err != nil || !slices.Equal(c.Task.Ignore, []string{"CVE-2024"}) {
		t.Fatalf("the user list replaces the default: %v, %v", c.Task.Ignore, err)
	}
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[task]\nignore = [\"\"]\n")
	if _, err = e.load(t); err == nil || !strings.Contains(err.Error(), "task.ignore") {
		t.Fatalf("an empty entry must be refused: %v", err)
	}
	// A cloned repository could hide the labels of real tasks.
	e = newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "[task]\nignore = []\n")
	if _, err = e.load(t); err == nil || !strings.Contains(err.Error(), "user config") {
		t.Fatalf("a repository may not set task.ignore: %v", err)
	}
}

func TestMaxAgeDaysBoundary(t *testing.T) {
	for days, ok := range map[string]bool{"0": false, "-1": false, "1": true} {
		e := newEnv(t)
		put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[prices]\nmax_age_days = "+days+"\n")
		if _, err := e.load(t); (err == nil) != ok {
			t.Errorf("max_age_days = %s: err = %v, want accepted = %v", days, err, ok)
		}
	}
}

func TestDefaultDataDirCannotBeASymlinkOutside(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	if err := os.MkdirAll(e.cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.cwd, ".ai-usage")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.load(t); err == nil || !strings.Contains(err.Error(), "data_dir") {
		t.Fatalf("err = %v, want a data_dir refusal (a committed .ai-usage link could redirect writes)", err)
	}
	// The user's own choices stay free.
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "data_dir = \""+filepath.Join(e.cwd, ".ai-usage")+"\"\n")
	if _, err := e.load(t); err != nil {
		t.Fatalf("user config may use a link: %v", err)
	}
	if err := os.Remove(filepath.Join(e.home, ".config", "kanjo", "config.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(e.home, e.cwd, e.xdg, Overrides{DataDir: filepath.Join(e.cwd, ".ai-usage")}); err != nil {
		t.Fatalf("--data-dir may use a link: %v", err)
	}
}

func (e env) userConfig(t *testing.T, content string) {
	t.Helper()
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), content)
}

func TestUserConfigSetsProjectsPortAndMaxAge(t *testing.T) {
	e := newEnv(t)
	e.userConfig(t, "projects = [\"/work/app-a\", \"/work/app.b\"]\n[serve]\nport = 9000\n[prices]\nmax_age_days = 7\n")
	c, err := e.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Projects, []string{"/work/app-a", "/work/app.b"}) || c.Serve.Port != 9000 || c.Prices.MaxAgeDays != 7 {
		t.Fatalf("got %+v", c)
	}
}

func TestInvalidProjectsPortAndAgeAreRejected(t *testing.T) {
	for name, content := range map[string]string{
		"relative project":  "projects = [\"app\"]\n",
		"empty project":     "projects = [\"\"]\n",
		"duplicate name":    "projects = [\"/a/app\", \"/b/app\"]\n",
		"same folder twice": "projects = [\"/a/app\", \"/a/app/\"]\n",
		"unsafe name":       "projects = [\"/a/=cmd\"]\n",
		"dash name":         "projects = [\"/a/-\"]\n",
		"root":              "projects = [\"/\"]\n",
		"port zero":         "[serve]\nport = 0\n",
		"port too high":     "[serve]\nport = 65536\n",
		"zero max age":      "[prices]\nmax_age_days = 0\n",
		"negative max age":  "[prices]\nmax_age_days = -3\n",
	} {
		e := newEnv(t)
		e.userConfig(t, content)
		if _, err := e.load(t); err == nil || !strings.Contains(err.Error(), "config.toml") {
			t.Errorf("%s: err = %v, want an error naming the file", name, err)
		}
	}
}

func TestPortLimitsAreInclusive(t *testing.T) {
	for _, port := range []string{"1", "65535"} {
		e := newEnv(t)
		e.userConfig(t, "[serve]\nport = "+port+"\n")
		if _, err := e.load(t); err != nil {
			t.Errorf("port %s refused: %v", port, err)
		}
	}
}

func TestRepoConfigMayNotSetProjectsServeOrPrices(t *testing.T) {
	for key, content := range map[string]string{
		"projects": "projects = [\"/etc\"]\n",
		"serve":    "[serve]\nport = 9000\n",
		"prices":   "[prices]\nmax_age_days = 9999\n",
	} {
		e := newEnv(t)
		put(t, filepath.Join(e.cwd, "kanjo.toml"), content)
		_, err := e.load(t)
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "user config") {
			t.Errorf("%s in a repository config: err = %v", key, err)
		}
	}
}

func TestProjectName(t *testing.T) {
	if got := ProjectName("/work/app-a"); got != "app-a" {
		t.Fatalf("ProjectName = %q", got)
	}
}

func TestMarkersMaxEventsDefault(t *testing.T) {
	c, err := newEnv(t).load(t)
	if err != nil || c.Markers.MaxEvents != 100000 {
		t.Fatalf("max_events = %d, err %v", c.Markers.MaxEvents, err)
	}
}

func TestMarkersMaxEventsMustBePositive(t *testing.T) {
	for value, ok := range map[string]bool{"0": false, "-5": false, "1": true, "250": true} {
		e := newEnv(t)
		e.userConfig(t, "[markers]\nmax_events = "+value+"\n")
		c, err := e.load(t)
		if (err == nil) != ok || ok && c.Markers.MaxEvents < 1 {
			t.Errorf("max_events = %s: err = %v", value, err)
		}
	}
}

func TestRepoConfigMayNotSetMarkers(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.cwd, "kanjo.toml"), "[markers]\nmax_events = 99999999\n")
	_, err := e.load(t)
	if err == nil || !strings.Contains(err.Error(), "markers") || !strings.Contains(err.Error(), "user config") {
		t.Fatalf("markers in a repository config: err = %v", err)
	}
}

func TestDelegateDefaults(t *testing.T) {
	c, err := newEnv(t).load(t)
	if err != nil {
		t.Fatal(err)
	}
	want := DelegateConfig{TimeoutS: 900, CommandTimeoutS: 600, KillGraceS: 5, MaxLogBytes: 1048576, MaxPatchBytes: 16777216, MaxPromptBytes: 1048576, EnvPass: []string{}}
	if !reflect.DeepEqual(c.Delegate, want) {
		t.Fatalf("got %+v, want %+v", c.Delegate, want)
	}
}

func TestDelegateValuesMustBePositive(t *testing.T) {
	for _, key := range []string{"timeout_s", "command_timeout_s", "kill_grace_s", "max_log_bytes", "max_patch_bytes", "max_prompt_bytes"} {
		for _, v := range []string{"0", "-1"} {
			e := newEnv(t)
			put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[delegate]\n"+key+" = "+v+"\n")
			if _, err := e.load(t); err == nil || !strings.Contains(err.Error(), "delegate."+key) {
				t.Errorf("%s = %s: err = %v", key, v, err)
			}
		}
	}
}

func TestDelegateEnvPassRejectsBadNames(t *testing.T) {
	long := strings.Repeat("A", 65)
	many := `"` + strings.Join(slices.Repeat([]string{"AB"}, 33), `", "`) + `"`
	for name, list := range map[string]string{
		"digit first": `["1A"]`, "dash": `["A-B"]`, "equals": `["A=B"]`, "empty": `[""]`, "space": `["A B"]`,
		"too long": `["` + long + `"]`, "too many": "[" + many + "]",
	} {
		e := newEnv(t)
		put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[delegate]\nenv_pass = "+list+"\n")
		if _, err := e.load(t); err == nil || !strings.Contains(err.Error(), "delegate.env_pass") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	e := newEnv(t)
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[delegate]\nenv_pass = [\"_A1\", \"HTTPS_PROXY\"]\n")
	if c, err := e.load(t); err != nil || !slices.Equal(c.Delegate.EnvPass, []string{"_A1", "HTTPS_PROXY"}) {
		t.Fatalf("good names: %+v, %v", c.Delegate, err)
	}
}

func TestRepoConfigMayNotSetDelegate(t *testing.T) {
	for _, content := range []string{"[delegate]\ntimeout_s = 5\n", "[delegate]\nenv_pass = [\"HOME\"]\n", "[delegate]\nmax_patch_bytes = 9\n"} {
		e := newEnv(t)
		put(t, filepath.Join(e.cwd, "kanjo.toml"), content)
		if _, err := e.load(t); err == nil || !strings.Contains(err.Error(), "user config") {
			t.Errorf("%q: err = %v, want it to say the key is for the user config", content, err)
		}
	}
}

func TestUserConfigMaySetDelegate(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, ".config", "kanjo", "config.toml"), "[delegate]\ntimeout_s = 5\nkill_grace_s = 1\nenv_pass = [\"LANG\"]\n")
	c, err := e.load(t)
	if err != nil || c.Delegate.TimeoutS != 5 || c.Delegate.KillGraceS != 1 || c.Delegate.CommandTimeoutS != 600 || !slices.Equal(c.Delegate.EnvPass, []string{"LANG"}) {
		t.Fatalf("%+v, %v", c.Delegate, err)
	}
}

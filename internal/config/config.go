// Package config loads kanjo's layered configuration: built-in defaults, the
// user file, the repository file. Command-line flags are applied by the caller.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// DefaultTaskIgnore lists labels the default pattern matches but that are not
// tasks (encodings, hashes, model names).
var DefaultTaskIgnore = []string{"UTF-8", "UTF-16", "SHA-1", "SHA-256", "SHA-512", "ISO-8601", "GPT-4"}

// DefaultTaskPattern matches issue-style ids such as ABC-12 or ABC-12.3.
const DefaultTaskPattern = `\b[A-Z][A-Z0-9]+-[0-9]+(\.[0-9]+)?\b`

const (
	defaultMaxLineBytes = 64 << 20
	maxFileBytes        = 1 << 20
)

// Config is the resolved configuration. Paths are absolute when the inputs are.
type Config struct {
	DataDir    string        `toml:"data_dir"`
	PricesFile string        `toml:"prices_file"`
	Claude     ClaudeConfig  `toml:"claude"`
	Task       TaskConfig    `toml:"task"`
	Projects   []string      `toml:"projects"`
	Serve      ServeConfig   `toml:"serve"`
	Prices     PricesConfig  `toml:"prices"`
	Markers    MarkersConfig `toml:"markers"`

	// trustedData is true when the user config set data_dir; a data_dir that
	// comes from a repository or from the default is checked against symlinks.
	trustedData bool `toml:"-"`
}

// ClaudeConfig configures the Claude Code transcript source.
type ClaudeConfig struct {
	ProjectsDir     string   `toml:"projects_dir"`
	ProjectPrefixes []string `toml:"project_prefixes"`
	MaxLineBytes    int      `toml:"max_line_bytes"`
}

// TaskConfig configures task attribution.
type TaskConfig struct {
	Pattern string   `toml:"pattern"`
	Ignore  []string `toml:"ignore"`
	MapFile string   `toml:"map_file"`
}

// ServeConfig configures kanjo serve.
type ServeConfig struct {
	Port int `toml:"port"`
}

// PricesConfig configures kanjo prices check.
type PricesConfig struct {
	MaxAgeDays int `toml:"max_age_days"`
}

// MarkersConfig configures the task marker journal.
type MarkersConfig struct {
	MaxEvents int `toml:"max_events"`
}

// ProjectName is the name a project is shown under: its folder's base name.
func ProjectName(folder string) string { return filepath.Base(folder) }

// ProjectFolderName gives the name Claude Code uses for a project's transcript
// folder: every character that is not an ASCII letter or digit becomes "-".
func ProjectFolderName(path string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, path)
}

// Overrides are command-line values that beat every file; empty means unset.
type Overrides struct{ DataDir string }

// Load resolves the configuration for a working directory. home is the user's
// home; xdgConfigHome may be empty. Relative paths in files and overrides
// resolve against cwd.
func Load(home, cwd, xdgConfigHome string, over Overrides) (Config, error) {
	c := Config{
		DataDir: ".ai-usage",
		Claude: ClaudeConfig{
			ProjectsDir:     filepath.Join(home, ".claude", "projects"),
			ProjectPrefixes: []string{ProjectFolderName(cwd)},
			MaxLineBytes:    defaultMaxLineBytes,
		},
		Task:    TaskConfig{Pattern: DefaultTaskPattern, Ignore: slices.Clone(DefaultTaskIgnore)},
		Serve:   ServeConfig{Port: 8787},
		Prices:  PricesConfig{MaxAgeDays: 30},
		Markers: MarkersConfig{MaxEvents: 100000},
	}
	userDir := xdgConfigHome
	if userDir == "" {
		userDir = filepath.Join(home, ".config")
	}
	if err := mergeFile(&c, filepath.Join(userDir, "kanjo", "config.toml"), ""); err != nil {
		return Config{}, err
	}
	if err := mergeFile(&c, filepath.Join(cwd, "kanjo.toml"), cwd); err != nil {
		return Config{}, err
	}
	if over.DataDir != "" {
		c.DataDir = over.DataDir
	}
	c.resolve(cwd)
	if over.DataDir == "" && !c.trustedData && !within(cwd, c.DataDir) {
		return Config{}, fmt.Errorf("data_dir %s must stay inside %s unless it is set in the user config or by --data-dir", c.DataDir, cwd)
	}
	return c, nil
}

// mergeFile applies one config file on top of c. A missing file changes nothing; an
// unreadable, oversized, malformed or unknown-key file is an error. A non-empty
// repoDir marks the file as untrusted (it comes from the repository the user
// is standing in): see checkRepo.
func mergeFile(c *Config, path, repoDir string) error {
	data, err := readFile(path)
	if err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	md, err := toml.Decode(string(data), c)
	if err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return fmt.Errorf("config %s: unknown key %s", path, keys[0])
	}
	if err := c.check(); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if md.IsDefined("data_dir") {
		c.trustedData = repoDir == ""
	}
	if repoDir != "" {
		if err := c.checkRepo(md, repoDir); err != nil {
			return fmt.Errorf("config %s: %w", path, err)
		}
	}
	return nil
}

// readFile reads a config file of at most maxFileBytes. A missing file reads
// as empty, which changes nothing.
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err == nil && len(data) > maxFileBytes {
		err = fmt.Errorf("larger than %d bytes", maxFileBytes)
	}
	return data, err
}

// repoKeys are the only keys a repository's kanjo.toml may set (repoTables are
// their parent tables). Everything else could copy secrets into the database,
// exhaust memory or read other projects, so it is for the user config or flags.
var (
	repoKeys   = map[string]bool{"data_dir": true, "prices_file": true, "task.map_file": true, "claude.project_prefixes": true}
	repoTables = map[string]bool{"claude": true, "task": true}
)

// checkRepo limits what a repository's kanjo.toml may set: only repoKeys; its
// paths must stay inside its own folder (symbolic links resolved); and
// project_prefixes may only widen to sub-names of the folder's own name.
// The user config and command-line flags are not restricted.
func (c Config) checkRepo(md toml.MetaData, cwd string) error {
	return errors.Join(checkRepoKeys(md), c.checkRepoPrefixes(md, cwd), c.checkRepoPaths(md, cwd))
}

func checkRepoKeys(md toml.MetaData) error {
	for _, k := range md.Keys() {
		if name := strings.Join(k, "."); !repoKeys[name] && !repoTables[name] {
			return fmt.Errorf("%s may only be set in the user config or by flag", name)
		}
	}
	return nil
}

func (c Config) checkRepoPrefixes(md toml.MetaData, cwd string) error {
	own := ProjectFolderName(cwd)
	if !md.IsDefined("claude", "project_prefixes") {
		return nil
	}
	for _, p := range c.Claude.ProjectPrefixes {
		if p != own && !strings.HasPrefix(p, own+"-") {
			return fmt.Errorf("claude.project_prefixes in a repository config must be %q (this folder's name) or start with %q; widen it in the user config", own, own+"-")
		}
	}
	return nil
}

func (c Config) checkRepoPaths(md toml.MetaData, cwd string) error {
	for _, f := range []struct {
		key  []string
		path string
	}{{[]string{"data_dir"}, c.DataDir}, {[]string{"prices_file"}, c.PricesFile}, {[]string{"task", "map_file"}, c.Task.MapFile}} {
		if md.IsDefined(f.key...) && !within(cwd, f.path) {
			return fmt.Errorf("%s in a repository config must stay inside %s", strings.Join(f.key, "."), cwd)
		}
	}
	return nil
}

// within reports whether p, resolved against cwd and through any symlinks,
// is cwd or below it. A path through a dangling symlink is refused: creating
// it would write wherever the link points.
func within(cwd, p string) bool {
	root, rootErr := realPath(cwd)
	target, targetErr := realPath(abs(cwd, p))
	if errors.Join(rootErr, targetErr) != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realPath resolves the symlinks of the longest existing prefix of p and
// appends the components that do not exist yet.
func realPath(p string) (string, error) {
	var rest []string
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...), nil
		}
		if _, statErr := os.Lstat(p); statErr == nil || !errors.Is(err, fs.ErrNotExist) {
			return "", err // a dangling link, or a real error
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}

// check validates values after each file, so errors can name the file.
func (c Config) check() error {
	switch {
	case c.DataDir == "":
		return errors.New("data_dir must not be empty")
	case c.Claude.MaxLineBytes <= 0:
		return errors.New("claude.max_line_bytes must be positive")
	case len(c.Claude.ProjectPrefixes) == 0 || slices.Contains(c.Claude.ProjectPrefixes, ""):
		return errors.New("claude.project_prefixes needs at least one non-empty prefix (an empty one would match every project)")
	}
	if c.Serve.Port < 1 || c.Serve.Port > 65535 {
		return errors.New("serve.port must be between 1 and 65535")
	}
	if c.Prices.MaxAgeDays <= 0 {
		return errors.New("prices.max_age_days must be positive")
	}
	if c.Markers.MaxEvents <= 0 {
		return errors.New("markers.max_events must be positive")
	}
	if slices.Contains(c.Task.Ignore, "") {
		return errors.New("task.ignore must not hold an empty entry")
	}
	if _, err := regexp.Compile(c.Task.Pattern); err != nil {
		return fmt.Errorf("task.pattern: %w", err)
	}
	return c.checkProjects()
}

// checkProjects wants absolute folders whose base names are safe, distinct
// display names: the name is the only thing the page shows of a project.
func (c Config) checkProjects() error {
	names := map[string]bool{}
	for _, p := range c.Projects {
		name := ProjectName(p)
		switch {
		case !filepath.IsAbs(p):
			return fmt.Errorf("projects: %q is not an absolute path", p)
		case !core.ValidTask(name) || name == core.NoTask || strings.Contains(name, "/"):
			return fmt.Errorf("projects: folder name %q is not a valid project name", name)
		case names[name]:
			return fmt.Errorf("projects: two folders are named %q", name)
		}
		names[name] = true
	}
	return nil
}

// resolve makes paths absolute against cwd and derives the task map path.
func (c *Config) resolve(cwd string) {
	c.DataDir = abs(cwd, c.DataDir)
	if c.PricesFile != "" {
		c.PricesFile = abs(cwd, c.PricesFile)
	}
	if c.Task.MapFile == "" {
		c.Task.MapFile = filepath.Join(c.DataDir, "claude-tasks.tsv")
	}
	c.Task.MapFile = abs(cwd, c.Task.MapFile)
}

func abs(cwd, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}

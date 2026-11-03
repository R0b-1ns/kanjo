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
)

// DefaultTaskPattern matches issue-style ids such as ABC-12 or ABC-12.3.
const DefaultTaskPattern = `\b[A-Z][A-Z0-9]+-[0-9]+(\.[0-9]+)?\b`

const (
	defaultMaxLineBytes = 64 << 20
	maxFileBytes        = 1 << 20
)

// Config is the resolved configuration. Paths are absolute when the inputs are.
type Config struct {
	DataDir    string       `toml:"data_dir"`
	PricesFile string       `toml:"prices_file"`
	Claude     ClaudeConfig `toml:"claude"`
	Task       TaskConfig   `toml:"task"`
}

// ClaudeConfig configures the Claude Code transcript source.
type ClaudeConfig struct {
	ProjectsDir     string   `toml:"projects_dir"`
	ProjectPrefixes []string `toml:"project_prefixes"`
	MaxLineBytes    int      `toml:"max_line_bytes"`
}

// TaskConfig configures task attribution.
type TaskConfig struct {
	Pattern string `toml:"pattern"`
	MapFile string `toml:"map_file"`
}

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

// Load resolves the configuration for a working directory. home is the user's
// home; xdgConfigHome may be empty. Relative paths in files resolve against cwd.
func Load(home, cwd, xdgConfigHome string) (Config, error) {
	c := Config{
		DataDir: ".ai-usage",
		Claude: ClaudeConfig{
			ProjectsDir:     filepath.Join(home, ".claude", "projects"),
			ProjectPrefixes: []string{ProjectFolderName(cwd)},
			MaxLineBytes:    defaultMaxLineBytes,
		},
		Task: TaskConfig{Pattern: DefaultTaskPattern},
	}
	userDir := xdgConfigHome
	if userDir == "" {
		userDir = filepath.Join(home, ".config")
	}
	for _, path := range []string{filepath.Join(userDir, "kanjo", "config.toml"), filepath.Join(cwd, "kanjo.toml")} {
		if err := mergeFile(&c, path); err != nil {
			return Config{}, err
		}
	}
	c.resolve(cwd)
	return c, nil
}

// mergeFile applies one config file on top of c. A missing file is fine; an
// unreadable, oversized, malformed or unknown-key file is an error.
func mergeFile(c *Config, path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if len(data) > maxFileBytes {
		return fmt.Errorf("config %s is larger than %d bytes", path, maxFileBytes)
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
	return nil
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
	if _, err := regexp.Compile(c.Task.Pattern); err != nil {
		return fmt.Errorf("task.pattern: %w", err)
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

// Package claudecode is the core.UsageSource adapter for Claude Code
// transcripts (~/.claude/projects/<project>/<session>.jsonl plus sub-agents).
//
// Only usage numbers, model names, ids and timestamps are read. User message
// text is scanned in memory for task labels; the text itself is never kept.
package claudecode

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// SourceName is the value of the source column for these rows.
const SourceName = "claude"

// Source reads Claude Code transcripts.
type Source struct {
	ProjectsDir  string            // usually ~/.claude/projects
	Prefixes     []string          // project folders to read: a name equal to one, or one plus "-" and more
	TaskPattern  *regexp.Regexp    // nil disables the heuristic
	TaskIgnore   []string          // labels the heuristic never reports (exact match)
	TaskMap      map[string]string // session id prefix -> task; the longest prefix wins
	MaxLineBytes int               // longer lines are skipped and counted
	// Markers assigns messages to tasks by time (spec section 15); the zero
	// value assigns nothing.
	Markers core.Timeline
	// LongContext tells whether a prompt of promptTokens is in the model's
	// long-context price tier; nil means never.
	LongContext func(model string, promptTokens int64) bool
}

// Read implements core.UsageSource. Unreadable or invalid input is skipped
// and counted in Batch.Skipped; only a bad configuration, an unreadable
// projects folder or a cancelled context are errors.
func (s Source) Read(ctx context.Context) (core.Batch, error) {
	if err := s.validate(); err != nil {
		return core.Batch{}, err
	}
	d, err := s.discover()
	if err != nil {
		return core.Batch{}, err
	}
	b := core.Batch{Skipped: d.skipped}
	for _, session := range slices.Sorted(maps.Keys(d.files)) {
		if err := ctx.Err(); err != nil {
			return core.Batch{}, err
		}
		rows, info, skipped := s.readSession(session, d.files[session].all())
		b.Skipped += skipped
		if len(rows) > 0 {
			b.Rows = append(b.Rows, rows...)
			b.Sessions = append(b.Sessions, info)
		}
	}
	return b, nil
}

func (s Source) validate() error {
	if s.MaxLineBytes <= 0 {
		return errors.New("claude source: MaxLineBytes must be positive")
	}
	for prefix, task := range s.TaskMap {
		if prefix == "" || !validMappedTask(task) {
			return fmt.Errorf("claude source: invalid task map entry for prefix %q", prefix)
		}
	}
	return nil
}

// sessionFiles lists a session's transcript files: the main ones first, then
// the sub-agents', so that on a duplicate message id the last copy read wins.
type sessionFiles struct{ main, sub []string }

func (f sessionFiles) all() []string { return append(slices.Clone(f.main), f.sub...) }

type discovery struct {
	files   map[string]*sessionFiles
	skipped int
}

func (d *discovery) session(id string) *sessionFiles {
	if d.files[id] == nil {
		d.files[id] = &sessionFiles{}
	}
	return d.files[id]
}

func (s Source) discover() (*discovery, error) {
	d := &discovery{files: map[string]*sessionFiles{}}
	entries, err := os.ReadDir(s.ProjectsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read projects folder %s: %w", s.ProjectsDir, err)
	}
	for _, e := range entries {
		if e.IsDir() && s.matches(e.Name()) { // IsDir is false for a symlink
			d.addProject(filepath.Join(s.ProjectsDir, e.Name()))
		}
	}
	return d, nil
}

func (s Source) matches(name string) bool {
	return slices.ContainsFunc(s.Prefixes, func(p string) bool { return projectMatches(name, p) })
}

// projectMatches reports whether a project folder name is the prefix itself or
// a sub-name of it (prefix, a dash, more). "-app" reads "-app" and "-app-api"
// but not "-app2". Claude Code writes both "/" and "-" as "-", so a sibling
// folder "app-legacy" cannot be told apart from a sub-folder.
func projectMatches(name, prefix string) bool {
	return name == prefix || strings.HasPrefix(name, strings.TrimSuffix(prefix, "-")+"-")
}

func (d *discovery) addProject(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		d.skipped++
		return
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.Type().IsRegular() && strings.HasSuffix(name, ".jsonl"):
			f := d.session(strings.TrimSuffix(name, ".jsonl"))
			f.main = append(f.main, filepath.Join(dir, name))
		case e.IsDir():
			d.addSubagents(name, filepath.Join(dir, name, "subagents"))
		}
	}
}

func (d *discovery) addSubagents(session, dir string) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		d.skipped++
		return
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl") {
			f := d.session(session)
			f.sub = append(f.sub, filepath.Join(dir, e.Name()))
		}
	}
}

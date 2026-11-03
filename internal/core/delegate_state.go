package core

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// ChangeKind says how a file differs between two states.
type ChangeKind int

const (
	ChangeAdded ChangeKind = iota + 1
	ChangeModified
	ChangeDeleted
)

// Change is one file that differs. Mode and Link are those after the change
// (before it, for a deleted file).
type Change struct {
	Path       string
	Kind       ChangeKind
	Mode, Link string
}

// ChangedPaths lists the files that differ between two states, sorted by
// path. Equal trees mean no change, without comparing the entries.
func ChangedPaths(before, after State) []Change {
	if before.Tree == after.Tree {
		return nil
	}
	old := make(map[string]Entry, len(before.Entries))
	for _, e := range before.Entries {
		old[e.Path] = e
	}
	var out []Change
	for _, e := range after.Entries {
		b, ok := old[e.Path]
		delete(old, e.Path)
		switch {
		case !ok:
			out = append(out, Change{Path: e.Path, Kind: ChangeAdded, Mode: e.Mode, Link: e.Link})
		case b != e:
			out = append(out, Change{Path: e.Path, Kind: ChangeModified, Mode: e.Mode, Link: e.Link})
		}
	}
	for _, b := range old {
		out = append(out, Change{Path: b.Path, Kind: ChangeDeleted, Mode: b.Mode, Link: b.Link})
	}
	slices.SortFunc(out, func(a, b Change) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// missingExpected lists the expected paths that the changes did not add or
// modify, in the order given and without duplicates.
func missingExpected(changes []Change, expect []string) []string {
	touched := make(map[string]bool, len(changes))
	for _, c := range changes {
		touched[c.Path] = c.Kind != ChangeDeleted
	}
	var out []string
	for _, e := range expect {
		if !touched[e] && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out
}

// Findings are what the changes of a run say about the task's limits. Names
// are safe to print: a name with a control character or invalid UTF-8 is
// reported as <invalid-name-N>.
type Findings struct {
	Missing, ForbiddenTouched, Unexpected, UnsafeSymlinks []string
	Invalid                                               int
}

// JudgeChanges compares the changes with the paths the task expects and
// forbids. A forbidden path matches itself and everything under it,
// case-insensitively; any ".git" segment is forbidden. Files outside expect
// are unexpected only when expect is not empty. A link added or changed is
// unsafe when its target is absolute or leaves the worktree.
func JudgeChanges(changes []Change, expect, forbid []string) Findings {
	var f Findings
	names := namer{}
	for _, c := range changes {
		switch {
		case forbidden(c.Path, forbid):
			f.ForbiddenTouched = append(f.ForbiddenTouched, names.label(c.Path))
		case len(expect) > 0 && !slices.Contains(expect, c.Path):
			f.Unexpected = append(f.Unexpected, names.label(c.Path))
		}
		if c.Kind != ChangeDeleted && c.Mode == "120000" && unsafeLink(c.Path, c.Link) {
			f.UnsafeSymlinks = append(f.UnsafeSymlinks, names.label(c.Path))
		}
	}
	f.Missing = missingExpected(changes, expect)
	f.Invalid = len(names)
	return f
}

func forbidden(p string, forbid []string) bool {
	low := strings.ToLower(p)
	for _, seg := range strings.Split(low, "/") {
		if seg == ".git" {
			return true
		}
	}
	for _, f := range forbid {
		f = strings.ToLower(f)
		if low == f || strings.HasPrefix(low, f+"/") {
			return true
		}
	}
	return false
}

func unsafeLink(linkPath, target string) bool {
	if target == "" || path.IsAbs(target) {
		return true
	}
	resolved := path.Join(path.Dir(linkPath), target)
	return resolved == ".." || strings.HasPrefix(resolved, "../")
}

// namer replaces names that are not safe to print.
type namer map[string]string

func (n namer) label(name string) string {
	if utf8.ValidString(name) && !strings.ContainsFunc(name, isControl) {
		return name
	}
	if l, ok := n[name]; ok {
		return l
	}
	n[name] = fmt.Sprintf("<invalid-name-%d>", len(n)+1)
	return n[name]
}

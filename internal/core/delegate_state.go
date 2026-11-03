package core

import "slices"

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
	slices.SortFunc(out, func(a, b Change) int { return compareStrings(a.Path, b.Path) })
	return out
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
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

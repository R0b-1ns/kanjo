package core_test

import (
	"reflect"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
)

func entry(path, blob string) core.Entry { return core.Entry{Path: path, Mode: "100644", Blob: blob} }

func TestChangedPathsAddedModifiedDeleted(t *testing.T) {
	before := core.State{Tree: "t1", Entries: []core.Entry{entry("keep", "1"), entry("mod", "1"), entry("del", "1"), {Path: "ln", Mode: "120000", Blob: "9", Link: "keep"}}}
	after := core.State{Tree: "t2", Entries: []core.Entry{entry("keep", "1"), entry("mod", "2"), entry("new", "3"), {Path: "ln", Mode: "120000", Blob: "8", Link: "/etc"}}}
	got := core.ChangedPaths(before, after)
	want := []core.Change{
		{Path: "del", Kind: core.ChangeDeleted, Mode: "100644"},
		{Path: "ln", Kind: core.ChangeModified, Mode: "120000", Link: "/etc"},
		{Path: "mod", Kind: core.ChangeModified, Mode: "100644"},
		{Path: "new", Kind: core.ChangeAdded, Mode: "100644"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestChangedPathsEqualTreesAreEmptyWithoutComparingEntries(t *testing.T) {
	a := core.State{Tree: "same", Entries: []core.Entry{entry("a", "1")}}
	b := core.State{Tree: "same", Entries: []core.Entry{entry("b", "2")}} // inconsistent on purpose
	if got := core.ChangedPaths(a, b); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestChangedPathsModeChangeIsModified(t *testing.T) {
	before := core.State{Tree: "t1", Entries: []core.Entry{{Path: "x", Mode: "100644", Blob: "1"}}}
	after := core.State{Tree: "t2", Entries: []core.Entry{{Path: "x", Mode: "100755", Blob: "1"}}}
	got := core.ChangedPaths(before, after)
	if len(got) != 1 || got[0].Kind != core.ChangeModified || got[0].Mode != "100755" {
		t.Fatalf("got %+v", got)
	}
}

func added(paths ...string) []core.Change {
	var cs []core.Change
	for _, p := range paths {
		cs = append(cs, core.Change{Path: p, Kind: core.ChangeAdded, Mode: "100644"})
	}
	return cs
}

func TestForbiddenMatchesItselfAndChildren(t *testing.T) {
	f := core.JudgeChanges(added("secrets", "secrets/key", "secrets/a/b", "ok.txt"), nil, []string{"secrets"})
	if !reflect.DeepEqual(f.ForbiddenTouched, []string{"secrets", "secrets/a/b", "secrets/key"}) && !reflect.DeepEqual(f.ForbiddenTouched, []string{"secrets", "secrets/key", "secrets/a/b"}) {
		t.Fatalf("forbidden = %v", f.ForbiddenTouched)
	}
}

func TestForbiddenIsCaseInsensitive(t *testing.T) {
	f := core.JudgeChanges(added("SECRETS/Key", "Config.YAML"), nil, []string{"secrets", "config.yaml"})
	if len(f.ForbiddenTouched) != 2 {
		t.Fatalf("forbidden = %v", f.ForbiddenTouched)
	}
}

func TestForbiddenDoesNotMatchASiblingPrefix(t *testing.T) {
	f := core.JudgeChanges(added("src2/a.go", "srcfile"), nil, []string{"src"})
	if len(f.ForbiddenTouched) != 0 {
		t.Fatalf("forbidden = %v", f.ForbiddenTouched)
	}
}

func TestDotGitIsAlwaysForbidden(t *testing.T) {
	f := core.JudgeChanges(added(".git/config", "sub/.GIT/hooks/x", ".gitignore", "sub/.github/ci.yml"), nil, nil)
	if len(f.ForbiddenTouched) != 2 {
		t.Fatalf("forbidden = %v", f.ForbiddenTouched)
	}
}

func TestStrayFilesAreChangesOutsideExpect(t *testing.T) {
	f := core.JudgeChanges(added("src/a.go", "notes.txt", "src/b.go"), []string{"src/a.go"}, nil)
	if !reflect.DeepEqual(f.Unexpected, []string{"notes.txt", "src/b.go"}) || len(f.Missing) != 0 {
		t.Fatalf("findings = %+v", f)
	}
	if f := core.JudgeChanges(added("anything"), nil, nil); len(f.Unexpected) != 0 {
		t.Fatalf("without --expect nothing is stray: %+v", f)
	}
}

func TestExpectedAndForbiddenTouchedIsForbidden(t *testing.T) {
	f := core.JudgeChanges(added("keys/id"), []string{"keys/id"}, []string{"keys"})
	if len(f.ForbiddenTouched) != 1 || len(f.Unexpected) != 0 {
		t.Fatalf("findings = %+v", f)
	}
}

func TestMissingExpectedFile(t *testing.T) {
	changes := append(added("a"), core.Change{Path: "c", Kind: core.ChangeDeleted, Mode: "100644"})
	f := core.JudgeChanges(changes, []string{"a", "b", "c", "b"}, nil)
	if !reflect.DeepEqual(f.Missing, []string{"b", "c"}) {
		t.Fatalf("missing = %v", f.Missing)
	}
}

func link(path, target string) core.Change {
	return core.Change{Path: path, Kind: core.ChangeAdded, Mode: "120000", Link: target}
}

func TestUnsafeSymlinkAbsoluteTarget(t *testing.T) {
	f := core.JudgeChanges([]core.Change{link("ln", "/etc/passwd")}, nil, nil)
	if !reflect.DeepEqual(f.UnsafeSymlinks, []string{"ln"}) {
		t.Fatalf("findings = %+v", f)
	}
}

func TestUnsafeSymlinkEscapingTarget(t *testing.T) {
	f := core.JudgeChanges([]core.Change{link("ln", "../outside"), link("a/b/ln", "../../../x"), link("e", "")}, nil, nil)
	if len(f.UnsafeSymlinks) != 3 {
		t.Fatalf("findings = %+v", f)
	}
}

func TestSafeRelativeSymlinkInsideIsAllowed(t *testing.T) {
	f := core.JudgeChanges([]core.Change{link("ln", "a/b"), link("a/ln", "../b/c"), link("a/b/ln", "../../a/x")}, nil, nil)
	if len(f.UnsafeSymlinks) != 0 {
		t.Fatalf("findings = %+v", f)
	}
	gone := core.Change{Path: "ln", Kind: core.ChangeDeleted, Mode: "120000", Link: "/etc/passwd"}
	if f := core.JudgeChanges([]core.Change{gone}, nil, nil); len(f.UnsafeSymlinks) != 0 {
		t.Fatalf("a deleted link is not unsafe: %+v", f)
	}
}

func TestHostileFileNameIsReportedWithoutControlCharacters(t *testing.T) {
	hostile := "evil\x1b[31m\nname"
	f := core.JudgeChanges(added(hostile, "\xff\xfe", "good.txt"), []string{"good.txt"}, nil)
	if f.Invalid != 2 || len(f.Unexpected) != 2 {
		t.Fatalf("findings = %+v", f)
	}
	for _, n := range f.Unexpected {
		if n != "<invalid-name-1>" && n != "<invalid-name-2>" {
			t.Errorf("name %q was not replaced", n)
		}
	}
	// the same hostile name in two lists keeps one label
	g := core.JudgeChanges(append(added(hostile), link(hostile+"2", "/x")), nil, []string{"evil"})
	for _, n := range append(append([]string{}, g.ForbiddenTouched...), g.UnsafeSymlinks...) {
		if len(n) > 0 && n[0] != '<' {
			t.Errorf("name %q leaked", n)
		}
	}
}

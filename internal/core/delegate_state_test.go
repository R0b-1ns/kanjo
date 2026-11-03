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

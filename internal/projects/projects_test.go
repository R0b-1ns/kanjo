package projects_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/projects"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

func row(task string) core.UsageRow {
	return core.UsageRow{
		Key:      core.Key{Source: "claude", Session: "s1", Day: "2026-11-03", Task: task, Model: "claude-sonnet-5-5"},
		Messages: 2, Tokens: core.Tokens{Output: 10},
	}
}

// project saves rows in <root>/<name>/.ai-usage and returns the folder.
func project(t *testing.T, root, name string, rows ...core.UsageRow) string {
	t.Helper()
	folder := filepath.Join(root, name)
	st, err := sqlitestore.Open(filepath.Join(folder, ".ai-usage"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := core.Batch{Rows: rows, Sessions: []core.SessionInfo{{Source: "claude", Session: "s1", TaskSource: "none"}}}
	if err := st.Merge(context.Background(), b, core.SnapshotLog{At: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	return folder
}

func TestReadsEachProjectIndependently(t *testing.T) {
	root := t.TempDir()
	a := project(t, root, "app-a", row("ABC-1"))
	other := row("-")
	other.Day = "2026-11-02" // another day: a distinct stored row
	b := project(t, root, "app-b", row("XYZ-3"), other)
	res := projects.Read(context.Background(), []string{a, b})
	if len(res.Failed) != 0 || len(res.Projects) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if res.Projects[0].Name != "app-a" || len(res.Projects[0].Rows) != 1 || res.Projects[1].Name != "app-b" || len(res.Projects[1].Rows) != 2 {
		t.Fatalf("projects = %+v", res.Projects)
	}
	if len(res.Projects[0].Sessions) != 1 {
		t.Fatalf("sessions = %+v", res.Projects[0].Sessions)
	}
}

func TestMissingDatabaseIsAnEmptyProject(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "never-snapshotted")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	res := projects.Read(context.Background(), []string{folder})
	if len(res.Failed) != 0 || len(res.Projects) != 1 || res.Projects[0].Name != "never-snapshotted" || len(res.Projects[0].Rows) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestNoProjectsGivesEmptyNonNilLists(t *testing.T) {
	res := projects.Read(context.Background(), nil)
	if res.Projects == nil || res.Failed == nil {
		t.Fatalf("lists must not be nil: %+v", res)
	}
}

func TestCorruptDatabaseIsReportedAndOthersStillRead(t *testing.T) {
	root := t.TempDir()
	good := project(t, root, "app-a", row("ABC-1"))
	bad := project(t, root, "app-b", row("XYZ-3"))
	if err := os.WriteFile(sqlitestore.Path(filepath.Join(bad, ".ai-usage")), []byte("this is not a database, just text, long enough to fill a header....................."), 0o600); err != nil {
		t.Fatal(err)
	}
	res := projects.Read(context.Background(), []string{bad, good})
	if !reflect.DeepEqual(res.Failed, []string{"app-b"}) || len(res.Projects) != 1 || res.Projects[0].Name != "app-a" {
		t.Fatalf("result = %+v", res)
	}
}

func TestSymlinkedDatabaseIsRefusedNotFollowed(t *testing.T) {
	root := t.TempDir()
	real := project(t, root, "real", row("ABC-1"))
	linked := filepath.Join(root, "linked")
	dir := filepath.Join(linked, ".ai-usage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sqlitestore.Path(filepath.Join(real, ".ai-usage")), sqlitestore.Path(dir)); err != nil {
		t.Fatal(err)
	}
	res := projects.Read(context.Background(), []string{linked})
	if !reflect.DeepEqual(res.Failed, []string{"linked"}) || len(res.Projects) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestHostileRowsInAnotherDatabaseMakeTheProjectFail(t *testing.T) {
	root := t.TempDir()
	hostile := project(t, root, "app-x", row("=HYPERLINK(1)"))
	res := projects.Read(context.Background(), []string{hostile})
	if !reflect.DeepEqual(res.Failed, []string{"app-x"}) || len(res.Projects) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[p] = info.ModTime().String() + info.Mode().String()
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			out[p] += string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadNeverCreatesOrChangesAnything(t *testing.T) {
	root := t.TempDir()
	a := project(t, root, "app-a", row("ABC-1"))
	missing := filepath.Join(root, "missing")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root)
	projects.Read(context.Background(), []string{a, missing})
	if after := snapshotTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("Read changed the file tree")
	}
}

func TestFailureCarriesNoPath(t *testing.T) {
	root := t.TempDir()
	bad := project(t, root, "app-b", row("=x"))
	for _, name := range projects.Read(context.Background(), []string{bad}).Failed {
		if name != "app-b" {
			t.Fatalf("failed entry %q must be the bare project name", name)
		}
	}
}

func TestAMissingProjectFolderIsReportedNotEmpty(t *testing.T) {
	res := projects.Read(context.Background(), []string{filepath.Join(t.TempDir(), "typo")})
	if !reflect.DeepEqual(res.Failed, []string{"typo"}) || len(res.Projects) != 0 {
		t.Fatalf("a folder that does not exist (typo, moved project) must not read as empty: %+v", res)
	}
}

func TestASymlinkedDataFolderIsRefusedNotFollowed(t *testing.T) {
	root := t.TempDir()
	real := project(t, root, "real", row("ABC-1"))
	planted := filepath.Join(root, "planted")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, ".ai-usage"), filepath.Join(planted, ".ai-usage")); err != nil {
		t.Fatal(err)
	}
	res := projects.Read(context.Background(), []string{planted})
	if !reflect.DeepEqual(res.Failed, []string{"planted"}) || len(res.Projects) != 0 {
		t.Fatalf("a .ai-usage link from an untrusted project must be refused: %+v", res)
	}
}

package kanjotest_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

func TestFakeWorkspaceContract(t *testing.T) {
	kanjotest.WorkspaceContract(t, func(t *testing.T, commits bool) kanjotest.WorkspaceFixture {
		fw := kanjotest.NewFakeWorkspace()
		fw.HasCommit = commits
		return kanjotest.WorkspaceFixture{
			Workspace:  fw,
			Write:      func(w core.Workdir, path, content string) { fw.Put(w.Session, path, content) },
			Symlink:    func(w core.Workdir, path, target string) { fw.Link(w.Session, path, target) },
			Delete:     func(w core.Workdir, path string) { fw.Delete(w.Session, path) },
			Lock:       fw.LockExternally,
			MoveTip:    fw.MoveTip,
			ForeignDir: fw.AddForeignDir,
		}
	})
}

func TestFakeWorkspaceHoldingFollowsTheLock(t *testing.T) {
	fw := kanjotest.NewFakeWorkspace()
	ctx := context.Background()
	commit, _ := fw.Resolve(ctx, "HEAD")
	w, err := fw.Create(ctx, "ses_1", commit)
	if err != nil || !fw.Holding("ses_1") {
		t.Fatalf("err %v holding %v", err, fw.Holding("ses_1"))
	}
	fw.Release(w)
	if fw.Holding("ses_1") {
		t.Fatal("lock still held after Release")
	}
	if got := fw.Branches(); len(got) != 1 || got[0] != "delegate/ses_1" {
		t.Fatalf("branches = %v", got)
	}
}

func TestFakeWorkspaceDiffNamesEveryChange(t *testing.T) {
	fw := kanjotest.NewFakeWorkspace()
	ctx := context.Background()
	commit, _ := fw.Resolve(ctx, "HEAD")
	w, _ := fw.Create(ctx, "ses_1", commit)
	fw.Put("ses_1", "added.txt", "x")
	st, _ := fw.State(ctx, w)
	var buf bytes.Buffer
	if err := fw.Diff(ctx, w, st, &buf, 1<<20); err != nil || !bytes.Contains(buf.Bytes(), []byte("added.txt")) {
		t.Fatalf("diff = %q, err %v", buf.String(), err)
	}
}

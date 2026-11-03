package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
)

func (r *runRig) clean(args ...string) (int, string, string) {
	return r.exec(context.Background(), append([]string{"clean"}, args...)...)
}

func TestCleanWithoutYesChangesNothing(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished("--task", "ABC-12")
	before, _ := os.ReadFile(filepath.Join(r.data, "kanjo.db"))
	code, out, errb := r.clean()
	if code != 0 || !strings.Contains(out, session+" ABC-12 finished "+filepath.Join(r.data, "worktrees", session)+" delegate/"+session) || !strings.Contains(out, "clean --yes") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
	after, _ := os.ReadFile(filepath.Join(r.data, "kanjo.db"))
	if string(before) != string(after) || r.ws.Removals() != 0 {
		t.Fatal("clean without --yes changed something")
	}
}

func TestCleanWithoutYesOnAReadOnlyFolder(t *testing.T) {
	r := newRunRig(t, writeFileA)
	r.finished()
	os.Chmod(r.data, 0o500)
	defer os.Chmod(r.data, 0o700)
	if code, out, errb := r.clean(); code != 0 || !strings.Contains(out, "finished") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
}

func TestCleanYesRemovesAndRecordsAbandoned(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	code, out, errb := r.clean("--yes")
	if code != 0 || !strings.Contains(out, "removed "+session) || r.ws.Removals() != 1 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
	if _, out, _ := r.world.run("summary"); !strings.Contains(out, "abandoned  1") {
		t.Fatalf("summary:\n%s", out)
	}
	if code, out, _ := r.clean("--yes"); code != 0 || !strings.Contains(out, "nothing to clean") {
		t.Fatalf("second clean: exit %d\n%s", code, out)
	}
}

func TestCleanSkipsARunningRun(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	const s = "ses_20261103T120000_aaaaaa"
	st := r.openStore()
	st.Begin(ctx, core.Run{Session: s, Task: "-", Adapter: "script", Model: "m", StartedAt: fixedNow, Base: "1111111111111111111111111111111111111111"})
	r.ws.Create(ctx, s, "1111111111111111111111111111111111111111") // lock held
	st.Created(ctx, s)
	st.Close()
	code, out, _ := r.clean("--yes")
	if code != 0 || !strings.Contains(out, "skipped (running) "+s) || r.ws.Removals() != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, listing, _ := r.clean(); !strings.Contains(listing, s+" - running") {
		t.Fatalf("listing:\n%s", listing)
	}
}

func TestCleanNothingToDoExits0(t *testing.T) {
	r := newRunRig(t)
	for _, args := range [][]string{{}, {"--yes"}} {
		if code, out, _ := r.clean(args...); code != 0 || out != "nothing to clean\n" {
			t.Fatalf("%v: exit %d out %q", args, code, out)
		}
	}
	if _, err := os.Stat(r.data); err == nil {
		t.Fatal("clean created the data folder")
	}
}

func TestCleanListsOnlyRecordedRuns(t *testing.T) {
	r := newRunRig(t, writeFileA)
	session := r.finished()
	w, _ := r.ws.Create(context.Background(), "ses_20261103T120000_ffffff", "1111111111111111111111111111111111111111")
	r.ws.Release(w)
	_, out, _ := r.clean()
	if !strings.Contains(out, session) || strings.Contains(out, "ffffff") {
		t.Fatalf("listing:\n%s", out)
	}
	r.clean("--yes")
	if got := r.ws.Branches(); len(got) != 1 || !strings.Contains(got[0], "ffffff") {
		t.Fatalf("branches = %v", got)
	}
}

// failingRemove is a workspace whose Remove fails with a hostile message.
type failingRemove struct{ core.Workspace }

func (failingRemove) Remove(context.Context, core.Owned) error {
	return errors.New("boom \x1b[31m\nred")
}

func TestCleanOutputCannotCarryEscapes(t *testing.T) {
	r := newRunRig(t, writeFileA)
	r.finished()
	r.ports.Workspace = failingRemove{r.ws}
	code, out, errb := r.clean("--yes")
	if code != 1 || strings.ContainsAny(out+errb, "\x1b\x07\r") || !strings.Contains(out, "failed") {
		t.Fatalf("exit %d out %q err %q", code, out, errb)
	}
}

func TestCleanOneFailureExits1AndContinues(t *testing.T) {
	r := newRunRig(t, writeFileA, writeFileA)
	a := r.finished()
	// the second run starts in the same fake: give it its own session through a second command
	b := r.finished()
	if a == b {
		t.Skip("session ids collided")
	}
	r.ws.MoveTip(a)
	code, out, _ := r.clean("--yes")
	if code != 1 || !strings.Contains(out, "failed "+a) || !strings.Contains(out, "removed "+b) {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestCleanBadFlagsExit2(t *testing.T) {
	r := newRunRig(t)
	if code, _, _ := r.clean("extra"); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

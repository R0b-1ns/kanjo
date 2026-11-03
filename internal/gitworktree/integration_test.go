//go:build integration

package gitworktree_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/gitworktree"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

// gitIn runs git for the test itself, with no user configuration.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

type repo struct {
	t         *testing.T
	dir, data string
	ws        *gitworktree.Workspace
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a repository holding README.md and gone.txt; with commit false
// it has no commit.
func newRepo(t *testing.T, commit bool) *repo {
	t.Helper()
	needGit(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	writeFile(t, filepath.Join(dir, "gone.txt"), "bye\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n.ai-usage/\n")
	if commit {
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "init")
	}
	data := filepath.Join(dir, ".ai-usage")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	return &repo{t: t, dir: dir, data: data, ws: gitworktree.New(dir, data)}
}

func (r *repo) create(session string) core.Workdir {
	r.t.Helper()
	ctx := context.Background()
	commit, err := r.ws.Resolve(ctx, "HEAD")
	if err != nil {
		r.t.Fatalf("Resolve: %v", err)
	}
	w, err := r.ws.Create(ctx, session, commit)
	if err != nil {
		r.t.Fatalf("Create: %v", err)
	}
	r.t.Cleanup(func() { r.ws.Release(w) })
	return w
}

func (r *repo) state(w core.Workdir) core.State {
	r.t.Helper()
	st, err := r.ws.State(context.Background(), w)
	if err != nil {
		r.t.Fatalf("State: %v", err)
	}
	return st
}

func TestIntegrationResolve(t *testing.T) {
	r := newRepo(t, true)
	ctx := context.Background()
	c, err := r.ws.Resolve(ctx, "HEAD")
	if err != nil || !core.ValidCommitID(c) || c != strings.TrimSpace(gitIn(t, r.dir, "rev-parse", "HEAD")) {
		t.Fatalf("commit %q err %v", c, err)
	}
	if c2, err := r.ws.Resolve(ctx, "main"); err != nil || c2 != c {
		t.Fatalf("main: %q %v", c2, err)
	}
	if _, err := r.ws.Resolve(ctx, "no-such-branch"); !errors.Is(err, core.ErrBadRef) {
		t.Fatalf("err = %v", err)
	}
}

func TestIntegrationResolveOfARepoWithoutCommit(t *testing.T) {
	r := newRepo(t, false)
	if _, err := r.ws.Resolve(context.Background(), "HEAD"); !errors.Is(err, core.ErrNoCommit) {
		t.Fatalf("err = %v", err)
	}
}

func TestIntegrationHostileRefIsData(t *testing.T) {
	r := newRepo(t, true)
	for _, ref := range []string{"--upload-pack=touch pwned", "$(touch pwned)", "-x", "a b", "main;touch pwned", "HEAD\n--help"} {
		if _, err := r.ws.Resolve(context.Background(), ref); !errors.Is(err, core.ErrBadRef) {
			t.Errorf("Resolve(%q) = %v", ref, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.dir, "pwned")); err == nil {
		t.Fatal("a hostile ref ran a command")
	}
}

func TestIntegrationHooksDoNotRun(t *testing.T) {
	r := newRepo(t, true)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	script := "#!/bin/sh\necho ran >> " + marker + "\n"
	for _, h := range []string{"post-checkout", "pre-commit", "reference-transaction", "post-index-change"} {
		p := filepath.Join(r.dir, ".git", "hooks", h)
		writeFile(t, p, script)
		os.Chmod(p, 0o755)
	}
	fsm := filepath.Join(t.TempDir(), "fsm.sh")
	writeFile(t, fsm, script)
	os.Chmod(fsm, 0o755)
	gitIn(t, r.dir, "config", "core.fsmonitor", fsm)
	w := r.create("ses_hooks")
	writeFile(t, filepath.Join(w.Dir, "new.txt"), "x")
	st := r.state(w)
	var buf bytes.Buffer
	if err := r.ws.Diff(context.Background(), w, st, &buf, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		b, _ := os.ReadFile(marker)
		t.Fatalf("a hook or program ran:\n%s", b)
	}
}

func TestIntegrationStateDoesNotFollowSymlinks(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_links")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "TOP-SECRET-CONTENT")
	outDir := t.TempDir()
	writeFile(t, filepath.Join(outDir, "inner.txt"), "x")
	os.Symlink(outside, filepath.Join(w.Dir, "ln-file"))
	os.Symlink(outDir, filepath.Join(w.Dir, "ln-dir"))
	st := r.state(w)
	got := map[string]core.Entry{}
	for _, e := range st.Entries {
		got[e.Path] = e
	}
	if e := got["ln-file"]; e.Mode != "120000" || e.Link != outside {
		t.Fatalf("ln-file = %+v", e)
	}
	if e := got["ln-dir"]; e.Mode != "120000" || e.Link != outDir {
		t.Fatalf("ln-dir = %+v", e)
	}
	if _, ok := got["ln-dir/inner.txt"]; ok {
		t.Fatal("a link to a folder was followed")
	}
	for _, e := range st.Entries {
		if strings.Contains(e.Link+e.Blob, "TOP-SECRET") {
			t.Fatal("the content behind a link was read")
		}
	}
}

func TestIntegrationStateDoesNotTouchTheRealIndex(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_index")
	writeFile(t, filepath.Join(w.Dir, "new.txt"), "x")
	idx := func(dir string) []byte {
		b, _ := os.ReadFile(strings.TrimSpace(gitIn(t, dir, "rev-parse", "--path-format=absolute", "--git-path", "index")))
		return b
	}
	statusMain, statusWt := gitIn(t, r.dir, "status", "--porcelain"), gitIn(t, w.Dir, "status", "--porcelain")
	idxMain, idxWt := idx(r.dir), idx(w.Dir)
	r.state(w)
	r.state(w)
	if gitIn(t, r.dir, "status", "--porcelain") != statusMain || gitIn(t, w.Dir, "status", "--porcelain") != statusWt {
		t.Fatal("git status changed")
	}
	if !bytes.Equal(idx(r.dir), idxMain) || !bytes.Equal(idx(w.Dir), idxWt) {
		t.Fatal("an index file changed")
	}
	if !strings.Contains(statusWt, "new.txt") {
		t.Fatalf("the new file must still be untracked: %q", statusWt)
	}
}

func TestIntegrationDiffIncludesNewDeletedAndBinaryFiles(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_diff")
	writeFile(t, filepath.Join(w.Dir, "new.txt"), "fresh\n")
	writeFile(t, filepath.Join(w.Dir, "README.md"), "changed\n")
	os.WriteFile(filepath.Join(w.Dir, "bin.dat"), []byte{0, 1, 2, 0, 255}, 0o644)
	os.Remove(filepath.Join(w.Dir, "gone.txt"))
	var buf bytes.Buffer
	if err := r.ws.Diff(context.Background(), w, r.state(w), &buf, 1<<20); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"new file mode", "deleted file mode", "+changed", "GIT binary patch", "b/new.txt", "a/gone.txt"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("diff lacks %q", want)
		}
	}
	patch := filepath.Join(t.TempDir(), "p.patch")
	os.WriteFile(patch, buf.Bytes(), 0o600)
	gitIn(t, r.dir, "apply", "--check", patch) // applies cleanly on the main checkout
}

func TestIntegrationDiffOverTheCap(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_cap")
	writeFile(t, filepath.Join(w.Dir, "big.txt"), strings.Repeat("x", 4096))
	var buf bytes.Buffer
	if err := r.ws.Diff(context.Background(), w, r.state(w), &buf, 100); !errors.Is(err, core.ErrDiffTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if buf.Len() > 100 {
		t.Fatalf("wrote %d bytes", buf.Len())
	}
}

func TestIntegrationCreateRefusesAnExistingBranch(t *testing.T) {
	r := newRepo(t, true)
	gitIn(t, r.dir, "branch", "delegate/ses_taken")
	commit, _ := r.ws.Resolve(context.Background(), "HEAD")
	if _, err := r.ws.Create(context.Background(), "ses_taken", commit); !errors.Is(err, core.ErrExists) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.data, "worktrees", "ses_taken")); err == nil {
		t.Fatal("a folder was created")
	}
	if !strings.Contains(gitIn(t, r.dir, "branch", "--list", "delegate/ses_taken"), "ses_taken") {
		t.Fatal("the user's branch is gone")
	}
}

func TestIntegrationCreateRefusesAnExistingFolder(t *testing.T) {
	r := newRepo(t, true)
	dir := filepath.Join(r.data, "worktrees", "ses_taken")
	writeFile(t, filepath.Join(dir, "mine.txt"), "keep")
	commit, _ := r.ws.Resolve(context.Background(), "HEAD")
	if _, err := r.ws.Create(context.Background(), "ses_taken", commit); !errors.Is(err, core.ErrExists) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mine.txt")); string(b) != "keep" {
		t.Fatal("the folder was touched")
	}
	if strings.Contains(gitIn(t, r.dir, "branch", "--list", "delegate/*"), "ses_taken") {
		t.Fatal("a branch was created")
	}
}

func TestIntegrationIgnoredFilesAreNotInTheState(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_ignored")
	before := r.state(w)
	writeFile(t, filepath.Join(w.Dir, "debug.log"), "noise")
	if after := r.state(w); after.Tree != before.Tree {
		t.Fatal("an ignored file changed the state")
	}
}

func TestIntegrationIgnoredFilesAreCounted(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_hidden")
	if r.state(w).Ignored != 0 {
		t.Fatal("a clean worktree reports ignored files")
	}
	// The agent can write the rule that hides its own file.
	writeFile(t, filepath.Join(w.Dir, "sub", ".gitignore"), "*\n")
	writeFile(t, filepath.Join(w.Dir, "sub", "hidden"), "x")
	if got := r.state(w).Ignored; got != 2 {
		t.Fatalf("Ignored = %d, want 2 (the rule file and the file it hides)", got)
	}
}

func TestIntegrationCreateTakesTheLockAndReleaseDropsIt(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_lock")
	try := func() error {
		f, err := os.OpenFile(filepath.Join(r.data, "worktrees", "ses_lock.lock"), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err := try(); err == nil {
		t.Fatal("the lock is not held after Create")
	}
	r.ws.Release(w)
	if err := try(); err != nil {
		t.Fatalf("the lock is still held after Release: %v", err)
	}
}

func TestIntegrationWorktreeIsPrivateAndOnItsBranch(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_priv")
	fi, err := os.Stat(w.Dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v err %v", fi.Mode().Perm(), err)
	}
	if got := strings.TrimSpace(gitIn(t, w.Dir, "rev-parse", "--abbrev-ref", "HEAD")); got != "delegate/ses_priv" {
		t.Fatalf("branch = %q", got)
	}
	if st := r.state(w); st.Head != w.Base {
		t.Fatalf("head %q base %q", st.Head, w.Base)
	}
	if lf, err := os.Stat(filepath.Join(r.data, "worktrees", "ses_priv.lock")); err != nil || lf.Mode().Perm() != 0o600 {
		t.Fatalf("lock file: %v %v", lf, err)
	}
}

func fixtureFor(t *testing.T, commits bool) kanjotest.WorkspaceFixture {
	r := newRepo(t, commits)
	return kanjotest.WorkspaceFixture{
		Workspace: r.ws,
		Write:     func(w core.Workdir, path, content string) { writeFile(t, filepath.Join(w.Dir, path), content) },
		Symlink: func(w core.Workdir, path, target string) {
			if err := os.Symlink(target, filepath.Join(w.Dir, path)); err != nil {
				t.Fatal(err)
			}
		},
		Delete: func(w core.Workdir, path string) {
			if err := os.Remove(filepath.Join(w.Dir, path)); err != nil {
				t.Fatal(err)
			}
		},
		Lock:    r.holdLock,
		MoveTip: r.moveTip,
		ForeignDir: func(session string) {
			writeFile(t, filepath.Join(r.data, "worktrees", session, "theirs.txt"), "mine")
		},
	}
}

// holdLock takes the run lock of a session as another process would.
func (r *repo) holdLock(session string) func() {
	r.t.Helper()
	os.MkdirAll(filepath.Join(r.data, "worktrees"), 0o700)
	f, err := os.OpenFile(filepath.Join(r.data, "worktrees", session+".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		r.t.Fatalf("the lock is already held: %v", err)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }
}

// moveTip makes the session's branch point to a new commit.
func (r *repo) moveTip(session string) {
	r.t.Helper()
	c := strings.TrimSpace(gitIn(r.t, r.dir, "commit-tree", "-m", "moved", "HEAD^{tree}"))
	gitIn(r.t, r.dir, "update-ref", "refs/heads/delegate/"+session, c)
}

func TestIntegrationWorkspaceContract(t *testing.T) {
	kanjotest.WorkspaceContract(t, fixtureFor)
}

func owned(r *repo, w core.Workdir) core.Owned {
	return core.Owned{Session: w.Session, Branch: core.BranchOf(w.Session), Dir: core.DirOf(w.Session), Tip: r.state(w).Head}
}

func branches(t *testing.T, r *repo) string {
	return gitIn(t, r.dir, "branch", "--list", "--format=%(refname:short)")
}

func TestIntegrationRemoveRefusesForeignBranch(t *testing.T) {
	r := newRepo(t, true)
	// a branch and a worktree the user made, with the shape of ours
	gitIn(t, r.dir, "branch", "delegate/mine")
	mine := filepath.Join(t.TempDir(), "mine")
	gitIn(t, r.dir, "worktree", "add", "-q", "-b", "other", mine, "HEAD")
	tip := strings.TrimSpace(gitIn(t, r.dir, "rev-parse", "HEAD"))
	ctx := context.Background()
	for _, bad := range []core.Owned{
		{Session: "mine", Branch: "other", Dir: "worktrees/mine", Tip: tip},
		{Session: "other", Branch: "other", Dir: mine, Tip: tip},
		{Session: "x", Branch: "delegate/x", Dir: "../mine", Tip: tip},
	} {
		if err := r.ws.Remove(ctx, bad); !errors.Is(err, core.ErrNotOurs) {
			t.Errorf("Remove(%+v) = %v", bad, err)
		}
	}
	if _, err := os.Stat(mine); err != nil || !strings.Contains(branches(t, r), "other") {
		t.Fatal("the user's worktree or branch was touched")
	}
}

func TestIntegrationRemoveRefusesMovedTip(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_moved")
	r.ws.Release(w)
	o := owned(r, w)
	writeFile(t, filepath.Join(w.Dir, "work.txt"), "uncommitted")
	r.moveTip("ses_moved")
	if err := r.ws.Remove(context.Background(), o); !errors.Is(err, core.ErrMoved) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "work.txt")); err != nil || !strings.Contains(branches(t, r), "delegate/ses_moved") {
		t.Fatal("something was deleted")
	}
}

func TestIntegrationRemoveRefusesAReplacedFolder(t *testing.T) {
	r := newRepo(t, true)
	a, b := r.create("ses_a"), r.create("ses_b")
	r.ws.Release(a)
	r.ws.Release(b)
	oa := owned(r, a)
	if err := os.RemoveAll(a.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(b.Dir, a.Dir); err != nil {
		t.Fatal(err)
	}
	if err := r.ws.Remove(context.Background(), oa); !errors.Is(err, core.ErrNotOurs) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "README.md")); err != nil {
		t.Fatal("the other worktree was damaged")
	}
	if p, err := r.ws.Inspect(context.Background(), oa); !errors.Is(err, core.ErrNotOurs) {
		t.Fatalf("Inspect: %+v %v", p, err)
	}
}

func TestIntegrationRemoveDoesNotFollowLinks(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_links")
	r.ws.Release(w)
	keep := t.TempDir()
	writeFile(t, filepath.Join(keep, "canary.txt"), "alive")
	os.Symlink(keep, filepath.Join(w.Dir, "ln"))
	os.Symlink(filepath.Join(keep, "canary.txt"), filepath.Join(w.Dir, "ln-file"))
	if err := r.ws.Remove(context.Background(), owned(r, w)); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(keep, "canary.txt")); err != nil || string(b) != "alive" {
		t.Fatal("Remove followed a link")
	}
	if _, err := os.Lstat(w.Dir); err == nil {
		t.Fatal("the worktree is still there")
	}
}

func TestIntegrationRemoveDeletesDirtyWorktreeAndItsBranch(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_dirty")
	r.ws.Release(w)
	writeFile(t, filepath.Join(w.Dir, "new.txt"), "x")
	writeFile(t, filepath.Join(w.Dir, "README.md"), "edited")
	o := owned(r, w)
	if err := r.ws.Remove(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(w.Dir); err == nil {
		t.Fatal("folder remains")
	}
	if strings.Contains(branches(t, r), "delegate/ses_dirty") {
		t.Fatal("branch remains")
	}
	if _, err := os.Lstat(filepath.Join(r.data, "worktrees", "ses_dirty.lock")); err == nil {
		t.Fatal("lock file remains")
	}
	if strings.Contains(gitIn(t, r.dir, "worktree", "list", "--porcelain"), "ses_dirty") {
		t.Fatal("git still lists the worktree")
	}
}

func TestIntegrationRemoveTwiceSucceeds(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_twice")
	r.ws.Release(w)
	o := owned(r, w)
	for i := 0; i < 2; i++ {
		if err := r.ws.Remove(context.Background(), o); err != nil {
			t.Fatalf("Remove #%d: %v", i+1, err)
		}
	}
}

func TestIntegrationLockIsHeldWhileRunning(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_busy")
	o := owned(r, w)
	other := gitworktree.New(r.dir, r.data) // another process-like handle
	if err := other.Remove(context.Background(), o); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if p, _ := other.Inspect(context.Background(), o); !p.Busy {
		t.Fatalf("presence = %+v", p)
	}
}

var heldLock *os.File

// TestHelperHoldLock is a child process that holds a run lock until killed.
func TestHelperHoldLock(t *testing.T) {
	path := os.Getenv("KANJO_HOLD_LOCK")
	if path == "" {
		t.Skip("helper process")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil || syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		os.Exit(3)
	}
	heldLock = f // a file that is garbage collected is closed, and its lock with it
	os.Stdout.WriteString("locked\n")
	time.Sleep(time.Hour) // not select{}: the runtime would call that a deadlock and exit
}

func TestIntegrationLockIsFreedWhenTheHolderIsKilled(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_killed")
	r.ws.Release(w)
	o := owned(r, w)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldLock$")
	cmd.Env = append(os.Environ(), "KANJO_HOLD_LOCK="+filepath.Join(r.data, "worktrees", "ses_killed.lock"))
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	buf := make([]byte, 7)
	if _, err := io.ReadFull(out, buf); err != nil || string(buf) != "locked\n" {
		t.Fatalf("helper said %q (%v)", buf, err)
	}
	if err := r.ws.Remove(context.Background(), o); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("while held: %v", err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if err := r.ws.Remove(context.Background(), o); err != nil {
		t.Fatalf("after the holder died: %v", err)
	}
}

func TestIntegrationInspectReportsPresence(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_insp")
	r.ws.Release(w)
	o := owned(r, w)
	p, err := r.ws.Inspect(context.Background(), o)
	if err != nil || p != (core.Presence{DirExists: true, BranchExists: true, TipMatches: true}) {
		t.Fatalf("presence %+v err %v", p, err)
	}
	r.ws.Remove(context.Background(), o)
	if p, _ := r.ws.Inspect(context.Background(), o); p != (core.Presence{}) {
		t.Fatalf("after Remove %+v", p)
	}
}

func TestIntegrationMainRepositoryIsUntouched(t *testing.T) {
	r := newRepo(t, true)
	snap := func() string {
		return gitIn(t, r.dir, "status", "--porcelain") + gitIn(t, r.dir, "branch", "--list") + gitIn(t, r.dir, "log", "--oneline", "--all")
	}
	before := snap()
	w := r.create("ses_main")
	writeFile(t, filepath.Join(w.Dir, "new.txt"), "x")
	os.Remove(filepath.Join(w.Dir, "gone.txt"))
	var buf bytes.Buffer
	r.ws.Diff(context.Background(), w, r.state(w), &buf, 1<<20)
	r.ws.Release(w)
	if err := r.ws.Remove(context.Background(), owned(r, w)); err != nil {
		t.Fatal(err)
	}
	if after := snap(); after != before {
		t.Fatalf("the repository changed:\nbefore %s\nafter %s", before, after)
	}
	if b, _ := os.ReadFile(filepath.Join(r.dir, "gone.txt")); string(b) != "bye\n" {
		t.Fatal("a file of the main checkout changed")
	}
}

func TestIntegrationCreateRefusesAnInvalidSessionOrCommit(t *testing.T) {
	r := newRepo(t, true)
	commit, _ := r.ws.Resolve(context.Background(), "HEAD")
	for session, c := range map[string]string{"../x": commit, "ses_ok": "nothex", "ses_ok2": strings.ToUpper(commit)} {
		if _, err := r.ws.Create(context.Background(), session, c); err == nil {
			t.Errorf("Create(%q, %q) succeeded", session, c)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(r.data, "worktrees")); len(entries) != 0 {
		t.Fatalf("something was created: %v", entries)
	}
}

func TestIntegrationCreateInAMissingDataFolderFails(t *testing.T) {
	r := newRepo(t, true)
	ws := gitworktree.New(r.dir, filepath.Join(t.TempDir(), "no", "such", "data"))
	commit, _ := ws.Resolve(context.Background(), "HEAD")
	if _, err := ws.Create(context.Background(), "ses_x", commit); err == nil {
		t.Fatal("Create succeeded without a data folder")
	}
}

func TestIntegrationAFailedCreateLeavesNoNewLockFileButKeepsAnOldOne(t *testing.T) {
	r := newRepo(t, true)
	commit, _ := r.ws.Resolve(context.Background(), "HEAD")
	lock := func(s string) string { return filepath.Join(r.data, "worktrees", s+".lock") }
	writeFile(t, filepath.Join(r.data, "worktrees", "ses_new", "x"), "x") // folder in the way, no lock file yet
	if _, err := r.ws.Create(context.Background(), "ses_new", commit); !errors.Is(err, core.ErrExists) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(lock("ses_new")); err == nil {
		t.Fatal("a failed Create left a lock file behind")
	}
	writeFile(t, lock("ses_old"), "")
	writeFile(t, filepath.Join(r.data, "worktrees", "ses_old", "x"), "x")
	if _, err := r.ws.Create(context.Background(), "ses_old", commit); !errors.Is(err, core.ErrExists) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(lock("ses_old")); err != nil {
		t.Fatal("a lock file that existed before was removed")
	}
}

func TestIntegrationStateAndDiffRefuseAForgedWorkdir(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_forged")
	st := r.state(w)
	ctx := context.Background()
	forged := []core.Workdir{
		{Session: "../x", Dir: w.Dir, Branch: w.Branch, Base: w.Base},
		{Session: w.Session, Dir: r.dir, Branch: w.Branch, Base: w.Base},
		{Session: w.Session, Dir: w.Dir, Branch: "main", Base: w.Base},
	}
	for _, f := range forged {
		if _, err := r.ws.State(ctx, f); !errors.Is(err, core.ErrNotOurs) {
			t.Errorf("State(%+v) = %v", f, err)
		}
		if err := r.ws.Diff(ctx, f, st, io.Discard, 1<<20); !errors.Is(err, core.ErrNotOurs) {
			t.Errorf("Diff(%+v) = %v", f, err)
		}
	}
	bad := w
	bad.Base = "nothex"
	if err := r.ws.Diff(ctx, bad, st, io.Discard, 1<<20); err == nil {
		t.Error("a bad base id reached git")
	}
	if err := r.ws.Diff(ctx, w, core.State{Tree: "nothex"}, io.Discard, 1<<20); err == nil {
		t.Error("a bad tree id reached git")
	}
}

func TestIntegrationDiffCapIsInclusive(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_cap2")
	writeFile(t, filepath.Join(w.Dir, "new.txt"), "fresh\n")
	st := r.state(w)
	var full bytes.Buffer
	if err := r.ws.Diff(context.Background(), w, st, &full, 1<<20); err != nil {
		t.Fatal(err)
	}
	n := int64(full.Len())
	if err := r.ws.Diff(context.Background(), w, st, io.Discard, n); err != nil {
		t.Fatalf("a diff of exactly the cap: %v", err)
	}
	if err := r.ws.Diff(context.Background(), w, st, io.Discard, n-1); !errors.Is(err, core.ErrDiffTooLarge) {
		t.Fatalf("one byte over the cap: %v", err)
	}
}

func TestIntegrationRemoveReportsABranchItCannotDelete(t *testing.T) {
	r := newRepo(t, true)
	w := r.create("ses_locked")
	r.ws.Release(w)
	o := owned(r, w)
	writeFile(t, filepath.Join(r.dir, ".git", "refs", "heads", "delegate", "ses_locked.lock"), "")
	if err := r.ws.Remove(context.Background(), o); err == nil {
		t.Fatal("Remove hid the failure to delete the branch")
	}
}

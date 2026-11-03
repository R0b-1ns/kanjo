//go:build integration

package gitworktree_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/gitworktree"
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

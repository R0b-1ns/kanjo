package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// maxLinkTarget bounds the target of a symbolic link read from the repository.
const maxLinkTarget = 4096

// Workspace implements core.Workspace for a repository and a data folder. The
// worktrees live in <data>/worktrees, one folder and one lock file per session.
type Workspace struct {
	repo, data string
	mu         sync.Mutex
	locks      map[string]*os.File // locks held by Create until Release
}

// New returns the workspace of a repository (its working tree root) that keeps
// worktrees under dataDir.
func New(repo, dataDir string) *Workspace {
	return &Workspace{repo: repo, data: dataDir, locks: map[string]*os.File{}}
}

func (w *Workspace) worktrees() string { return filepath.Join(w.data, "worktrees") }

func (w *Workspace) dirOf(session string) string { return filepath.Join(w.worktrees(), session) }
func (w *Workspace) lockOf(session string) string {
	return filepath.Join(w.worktrees(), session+".lock")
}

func (w *Workspace) git(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	out, err := git(ctx, dir, env, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// Resolve implements core.Workspace.
func (w *Workspace) Resolve(ctx context.Context, ref string) (string, error) {
	if !core.ValidRef(ref) {
		return "", core.ErrBadRef
	}
	out, err := w.git(ctx, w.repo, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err == nil && core.ValidCommitID(out) {
		return out, nil
	}
	if heads, herr := w.git(ctx, w.repo, nil, "for-each-ref", "--count=1", "refs/heads"); herr == nil && heads == "" {
		return "", core.ErrNoCommit
	}
	return "", core.ErrBadRef
}

// Create implements core.Workspace.
func (w *Workspace) Create(ctx context.Context, session, commit string) (core.Workdir, error) {
	if !core.ValidSession(session) || !core.ValidCommitID(commit) {
		return core.Workdir{}, errors.New("invalid session or commit")
	}
	if err := w.ensureWorktrees(); err != nil {
		return core.Workdir{}, err
	}
	lock, fresh, err := w.takeLock(session)
	if err != nil {
		return core.Workdir{}, err
	}
	wd, err := w.add(ctx, session, commit)
	if err != nil {
		unlock(lock)
		if fresh {
			os.Remove(w.lockOf(session))
		}
		return core.Workdir{}, err
	}
	w.mu.Lock()
	w.locks[session] = lock
	w.mu.Unlock()
	return wd, nil
}

// ensureWorktrees makes sure <data>/worktrees is a real directory (0700).
func (w *Workspace) ensureWorktrees() error {
	d := w.worktrees()
	fi, err := os.Lstat(d)
	if errors.Is(err, fs.ErrNotExist) {
		if err = os.Mkdir(d, 0o700); err == nil {
			return nil
		}
		fi, err = os.Lstat(d)
	}
	switch {
	case err != nil:
		return err
	case !fi.IsDir():
		return errors.New("the worktrees folder is not a real directory")
	}
	return nil
}

// takeLock takes the run lock of a session without waiting. A lock held by
// another run means the session exists.
func (w *Workspace) takeLock(session string) (f *os.File, fresh bool, err error) {
	path := w.lockOf(session)
	_, statErr := os.Lstat(path)
	f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fresh, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			err = core.ErrExists
		}
		return nil, fresh, err
	}
	return f, statErr != nil, nil
}

func unlock(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

// add makes the worktree and its branch; it refuses a folder or branch that
// exists and never replaces them.
func (w *Workspace) add(ctx context.Context, session, commit string) (core.Workdir, error) {
	dir, branch := w.dirOf(session), core.BranchOf(session)
	if _, err := os.Lstat(dir); err == nil {
		return core.Workdir{}, core.ErrExists
	}
	switch _, err := w.git(ctx, w.repo, nil, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); {
	case err == nil:
		return core.Workdir{}, core.ErrExists
	case exitCode(err) != 1:
		return core.Workdir{}, err
	}
	if _, err := w.git(ctx, w.repo, nil, "worktree", "add", "-q", "-b", branch, "--", dir, commit); err != nil {
		return core.Workdir{}, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return core.Workdir{}, err
	}
	return core.Workdir{Session: session, Dir: dir, Branch: branch, Base: commit}, nil
}

// Release implements core.Workspace.
func (w *Workspace) Release(wd core.Workdir) error {
	w.mu.Lock()
	f := w.locks[wd.Session]
	delete(w.locks, wd.Session)
	w.mu.Unlock()
	if f != nil {
		unlock(f)
	}
	return nil
}

// State implements core.Workspace. It builds a tree from the worktree with a
// temporary index, so the real index is never read or written and a symbolic
// link is stored, not followed.
func (w *Workspace) State(ctx context.Context, wd core.Workdir) (core.State, error) {
	if err := w.owns(wd); err != nil {
		return core.State{}, err
	}
	tmp, err := os.MkdirTemp("", "kanjo-index-")
	if err != nil {
		return core.State{}, err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, err := w.git(ctx, wd.Dir, env, "add", "-A"); err != nil {
		return core.State{}, err
	}
	tree, err := w.git(ctx, wd.Dir, env, "write-tree")
	if err != nil {
		return core.State{}, err
	}
	head, err := w.git(ctx, w.repo, nil, "rev-parse", "--verify", "--end-of-options", "refs/heads/"+wd.Branch+"^{commit}")
	if err != nil {
		return core.State{}, err
	}
	entries, err := w.entries(ctx, tree)
	if err != nil {
		return core.State{}, err
	}
	return core.State{Tree: tree, Head: head, Entries: entries}, nil
}

// owns checks that wd is a workdir this adapter hands out.
func (w *Workspace) owns(wd core.Workdir) error {
	if !core.ValidSession(wd.Session) || wd.Dir != w.dirOf(wd.Session) || wd.Branch != core.BranchOf(wd.Session) {
		return core.ErrNotOurs
	}
	return nil
}

// entries lists the files of a tree with their link targets.
func (w *Workspace) entries(ctx context.Context, tree string) ([]core.Entry, error) {
	if !core.ValidCommitID(tree) { // a tree id looks like a commit id
		return nil, errors.New("git returned an unexpected tree id")
	}
	out, err := git(ctx, w.repo, nil, nil, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, err
	}
	var entries []core.Entry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 {
			return nil, errors.New("git returned an unexpected tree listing")
		}
		e := core.Entry{Path: string(path), Mode: fields[0], Blob: fields[2]}
		if e.Mode == "120000" {
			if e.Link, err = w.linkTarget(ctx, e.Blob); err != nil {
				return nil, err
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// linkTarget reads the text of a symbolic link blob, at most maxLinkTarget bytes.
func (w *Workspace) linkTarget(ctx context.Context, blob string) (string, error) {
	if !core.ValidCommitID(blob) {
		return "", errors.New("git returned an unexpected blob id")
	}
	var buf bytes.Buffer
	lim := &limited{w: &buf, left: maxLinkTarget}
	if _, err := git(ctx, w.repo, nil, lim, "cat-file", "blob", blob); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// capWriter fails once more than left bytes would be written.
type capWriter struct {
	w        io.Writer
	left     int64
	exceeded bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		c.exceeded = true
		return 0, core.ErrDiffTooLarge
	}
	c.left -= int64(len(p))
	return c.w.Write(p)
}

// Diff implements core.Workspace: the changes from the base commit to the
// tree, binary files included, without renames, external diff programs or text
// conversion.
func (w *Workspace) Diff(ctx context.Context, wd core.Workdir, to core.State, dst io.Writer, max int64) error {
	if err := w.owns(wd); err != nil {
		return err
	}
	if !core.ValidCommitID(wd.Base) || !core.ValidCommitID(to.Tree) {
		return errors.New("invalid base or tree id")
	}
	cw := &capWriter{w: dst, left: max}
	_, err := git(ctx, w.repo, nil, cw, "diff-tree", "-p", "-r", "--binary", "--no-renames", "--no-ext-diff", "--no-textconv", wd.Base, to.Tree)
	if cw.exceeded {
		return core.ErrDiffTooLarge
	}
	return err
}

var _ core.Workspace = (*Workspace)(nil)

// ownedPaths checks that o names a session's own branch and folder, derived
// from the session and nothing else.
func (w *Workspace) ownedPaths(o core.Owned) error {
	if !core.ValidSession(o.Session) || o.Branch != core.BranchOf(o.Session) || o.Dir != core.DirOf(o.Session) || (o.Tip != "" && !core.ValidCommitID(o.Tip)) {
		return core.ErrNotOurs
	}
	return nil
}

// probe says what exists of an owned workspace. A folder that is not a real
// directory, that git does not list as a worktree on the session's branch, or
// whose resolved path is not the derived one is not ours.
func (w *Workspace) probe(ctx context.Context, o core.Owned) (core.Presence, error) {
	if err := w.ownedPaths(o); err != nil {
		return core.Presence{}, err
	}
	var p core.Presence
	dir := w.dirOf(o.Session)
	fi, err := os.Lstat(dir)
	switch {
	case err == nil:
		if !fi.IsDir() {
			return p, core.ErrNotOurs // a link, a file: never followed
		}
		if err := w.listedOnBranch(ctx, dir, o.Branch); err != nil {
			return p, err
		}
		p.DirExists = true
	case !errors.Is(err, fs.ErrNotExist):
		return p, err
	}
	tip, err := w.git(ctx, w.repo, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+o.Branch+"^{commit}")
	switch {
	case err == nil:
		p.BranchExists, p.TipMatches = true, tip == o.Tip
	case exitCode(err) != 1:
		return p, err
	}
	p.Busy, err = w.busy(o.Session)
	return p, err
}

// listedOnBranch checks that git lists dir as a worktree checked out on branch.
func (w *Workspace) listedOnBranch(ctx context.Context, dir, branch string) error {
	real, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		return core.ErrNotOurs
	}
	out, err := w.git(ctx, w.repo, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	if worktreeBranches(out)[filepath.Join(real, filepath.Base(dir))] != "refs/heads/"+branch {
		return core.ErrNotOurs
	}
	return nil
}

// worktreeBranches reads "git worktree list --porcelain": the checked out
// branch of each worktree, by resolved path.
func worktreeBranches(porcelain string) map[string]string {
	out := map[string]string{}
	for _, block := range strings.Split(porcelain, "\n\n") {
		var path, branch string
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "worktree "); ok {
				path = v
			}
			if v, ok := strings.CutPrefix(line, "branch "); ok {
				branch = v
			}
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			out[resolved] = branch
		}
	}
	return out
}

// busy tries the run lock without waiting; it creates nothing.
func (w *Workspace) busy(session string) (busy bool, err error) {
	f, err := os.OpenFile(w.lockOf(session), os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return busy, nil
	}
	if err != nil {
		return busy, err
	}
	defer f.Close()
	switch err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); {
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, nil
	case err != nil:
		return busy, err
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return busy, nil
}

// Inspect implements core.Workspace.
func (w *Workspace) Inspect(ctx context.Context, o core.Owned) (core.Presence, error) {
	return w.probe(ctx, o)
}

// Remove implements core.Workspace. It deletes the worktree (git removes a
// link, it does not follow it), then the branch only if it still points to the
// recorded tip, then the lock file.
func (w *Workspace) Remove(ctx context.Context, o core.Owned) error {
	p, err := w.probe(ctx, o)
	switch {
	case err != nil:
		return err
	case p.Busy:
		return core.ErrBusy
	case p.BranchExists && !p.TipMatches:
		return core.ErrMoved
	}
	if err := w.deleteBranchAndFolder(ctx, o, p); err != nil {
		return err
	}
	if err := os.Remove(w.lockOf(o.Session)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (w *Workspace) deleteBranchAndFolder(ctx context.Context, o core.Owned, p core.Presence) error {
	if p.DirExists {
		if _, err := w.git(ctx, w.repo, nil, "worktree", "remove", "--force", "--", w.dirOf(o.Session)); err != nil {
			return err
		}
	}
	if p.BranchExists {
		if _, err := w.git(ctx, w.repo, nil, "update-ref", "-d", "refs/heads/"+o.Branch, o.Tip); err != nil {
			return err
		}
	}
	return nil
}

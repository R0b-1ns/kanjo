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
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, core.ErrExists
		}
		return nil, false, err
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

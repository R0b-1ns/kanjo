package kanjotest

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
)

const (
	fakeBase = "1111111111111111111111111111111111111111"
	fakeMove = "2222222222222222222222222222222222222222"
)

type fakeFile struct{ content, link string }

type fakeTree struct {
	files map[string]fakeFile
	tip   string
}

// FakeWorkspace is an in-memory core.Workspace. The repository holds
// README.md and gone.txt at HEAD, main and the base commit. Workdir.Dir is
// "/fake/worktrees/<session>".
type FakeWorkspace struct {
	// HasCommit is true by default; set it to false for a repository with no commit.
	HasCommit bool
	// OnCreate, when set, is called with the session at the start of Create,
	// before anything is checked or made.
	OnCreate func(session string)

	mu      sync.Mutex
	repo    map[string]string
	trees   map[string]*fakeTree
	held    map[string]bool // lock held by Create until Release
	ext     map[string]bool // lock held by another process
	foreign map[string]bool // a folder that is not ours sits at the session's dir
	removes int
}

// NewFakeWorkspace returns a repository with one commit.
func NewFakeWorkspace() *FakeWorkspace {
	return &FakeWorkspace{
		HasCommit: true,
		repo:      map[string]string{"README.md": "hello\n", "gone.txt": "bye\n"},
		trees:     map[string]*fakeTree{}, held: map[string]bool{}, ext: map[string]bool{}, foreign: map[string]bool{},
	}
}

func fakeHash(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s;", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Resolve implements core.Workspace.
func (f *FakeWorkspace) Resolve(_ context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case !f.HasCommit:
		return "", core.ErrNoCommit
	case !core.ValidRef(ref) || (ref != "HEAD" && ref != "main" && ref != fakeBase):
		return "", core.ErrBadRef
	}
	return fakeBase, nil
}

// Create implements core.Workspace.
func (f *FakeWorkspace) Create(_ context.Context, session, commit string) (core.Workdir, error) {
	if f.OnCreate != nil {
		f.OnCreate(session)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !core.ValidSession(session) || commit != fakeBase {
		return core.Workdir{}, errors.New("invalid session or commit")
	}
	if _, ok := f.trees[session]; ok || f.foreign[session] {
		return core.Workdir{}, core.ErrExists
	}
	files := map[string]fakeFile{}
	for p, c := range f.repo {
		files[p] = fakeFile{content: c}
	}
	f.trees[session] = &fakeTree{files: files, tip: fakeBase}
	f.held[session] = true
	return core.Workdir{Session: session, Dir: "/fake/" + core.DirOf(session), Branch: core.BranchOf(session), Base: commit}, nil
}

func (f *FakeWorkspace) tree(session string) *fakeTree {
	tr := f.trees[session]
	if tr == nil {
		panic("kanjotest: no workspace for session " + session)
	}
	return tr
}

// Put creates or changes a regular file of the session's worktree.
func (f *FakeWorkspace) Put(session, path, content string) {
	f.setFile(session, path, fakeFile{content: content})
}

// Link creates or changes a symbolic link of the session's worktree.
func (f *FakeWorkspace) Link(session, path, target string) {
	f.setFile(session, path, fakeFile{link: target})
}

func (f *FakeWorkspace) setFile(session, path string, file fakeFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tree(session).files[path] = file
}

// Delete removes a file of the session's worktree.
func (f *FakeWorkspace) Delete(session, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.tree(session).files, path)
}

// MoveTip makes the session's branch point to another commit.
func (f *FakeWorkspace) MoveTip(session string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tree(session).tip = fakeMove
}

// LockExternally holds the run lock of a session as another process would; the
// returned function drops it.
func (f *FakeWorkspace) LockExternally(session string) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ext[session] = true
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.ext, session)
	}
}

// AddForeignDir puts a folder that is not a worktree of this tool at the dir of
// a session.
func (f *FakeWorkspace) AddForeignDir(session string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.foreign[session] = true
}

// Holding reports whether Create's lock of the session is still held.
func (f *FakeWorkspace) Holding(session string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[session]
}

// Branches lists the branches of the worktrees that exist, sorted.
func (f *FakeWorkspace) Branches() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for s := range f.trees {
		out = append(out, core.BranchOf(s))
	}
	sort.Strings(out)
	return out
}

// Removals counts the worktrees Remove actually deleted.
func (f *FakeWorkspace) Removals() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.removes
}

// State implements core.Workspace.
func (f *FakeWorkspace) State(_ context.Context, w core.Workdir) (core.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tr, ok := f.trees[w.Session]
	if !ok {
		return core.State{}, core.ErrNotOurs
	}
	return stateOf(tr), nil
}

func stateOf(tr *fakeTree) core.State {
	st := core.State{Head: tr.tip}
	paths := make([]string, 0, len(tr.files))
	for p := range tr.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var tree []string
	for _, p := range paths {
		fl := tr.files[p]
		e := core.Entry{Path: p, Mode: "100644", Blob: fakeHash(fl.content)}
		if fl.link != "" {
			e.Mode, e.Blob, e.Link = "120000", fakeHash(fl.link), fl.link
		}
		st.Entries = append(st.Entries, e)
		tree = append(tree, p, e.Mode, e.Blob)
	}
	st.Tree = fakeHash(tree...)
	return st
}

type capWriter struct {
	w    io.Writer
	left int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, core.ErrDiffTooLarge
	}
	c.left -= int64(len(p))
	return c.w.Write(p)
}

// Diff implements core.Workspace. The text lists every changed path in a
// git-like header; contents are not reproduced.
func (f *FakeWorkspace) Diff(_ context.Context, w core.Workdir, to core.State, dst io.Writer, max int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.trees[w.Session]; !ok {
		return core.ErrNotOurs
	}
	out := &capWriter{w: dst, left: max}
	for _, h := range diffHeaders(stateOfRepo(f.repo).Entries, to.Entries) {
		if _, err := io.WriteString(out, h); err != nil {
			return err
		}
	}
	return nil
}

// diffHeaders describes how entries differ from base, added and changed files
// in the order of entries, then deleted ones.
func diffHeaders(base, entries []core.Entry) []string {
	old := map[string]core.Entry{}
	for _, b := range base {
		old[b.Path] = b
	}
	var out []string
	for _, e := range entries {
		b, ok := old[e.Path]
		delete(old, e.Path)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("diff --git a/%s b/%s\nnew file mode %s\n", e.Path, e.Path, e.Mode))
		case b != e:
			out = append(out, fmt.Sprintf("diff --git a/%s b/%s\nindex %s\n", e.Path, e.Path, e.Mode))
		}
	}
	for _, b := range base {
		if _, gone := old[b.Path]; gone {
			out = append(out, fmt.Sprintf("diff --git a/%s b/%s\ndeleted file mode %s\n", b.Path, b.Path, b.Mode))
		}
	}
	return out
}

func stateOfRepo(repo map[string]string) core.State {
	files := map[string]fakeFile{}
	for p, c := range repo {
		files[p] = fakeFile{content: c}
	}
	return stateOf(&fakeTree{files: files, tip: fakeBase})
}

// Release implements core.Workspace.
func (f *FakeWorkspace) Release(w core.Workdir) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, w.Session)
	return nil
}

func ownedOK(o core.Owned) bool {
	return core.ValidSession(o.Session) && o.Branch == core.BranchOf(o.Session) && o.Dir == core.DirOf(o.Session)
}

// Inspect implements core.Workspace.
func (f *FakeWorkspace) Inspect(_ context.Context, o core.Owned) (core.Presence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ownedOK(o) || f.foreign[o.Session] {
		return core.Presence{}, core.ErrNotOurs
	}
	tr, ok := f.trees[o.Session]
	if !ok {
		return core.Presence{}, nil
	}
	return core.Presence{DirExists: true, BranchExists: true, Busy: f.held[o.Session] || f.ext[o.Session], TipMatches: tr.tip == o.Tip}, nil
}

// Remove implements core.Workspace.
func (f *FakeWorkspace) Remove(_ context.Context, o core.Owned) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ownedOK(o) || f.foreign[o.Session] {
		return core.ErrNotOurs
	}
	tr, ok := f.trees[o.Session]
	switch {
	case !ok:
		return nil
	case f.held[o.Session] || f.ext[o.Session]:
		return core.ErrBusy
	case tr.tip != o.Tip:
		return core.ErrMoved
	}
	delete(f.trees, o.Session)
	f.removes++
	return nil
}

// WorkspaceFixture is what WorkspaceContract needs from an adapter: the
// workspace over a repository whose HEAD holds the files README.md and
// gone.txt, and the means to act on a worktree from outside.
type WorkspaceFixture struct {
	Workspace core.Workspace
	// Write creates or changes a regular file; Symlink a link; Delete a file.
	Write   func(w core.Workdir, path, content string)
	Symlink func(w core.Workdir, path, target string)
	Delete  func(w core.Workdir, path string)
	// Lock holds the run lock of a session like another process; the returned
	// function drops it.
	Lock func(session string) (unlock func())
	// MoveTip makes the session's branch point to another commit.
	MoveTip func(session string)
	// ForeignDir puts a folder that is not a worktree of this tool at the
	// session's dir.
	ForeignDir func(session string)
}

type wsEnv struct {
	t   *testing.T
	fx  WorkspaceFixture
	ctx context.Context
}

func (e wsEnv) create(session string) core.Workdir {
	e.t.Helper()
	commit, err := e.fx.Workspace.Resolve(e.ctx, "HEAD")
	if err != nil {
		e.t.Fatalf("Resolve HEAD: %v", err)
	}
	w, err := e.fx.Workspace.Create(e.ctx, session, commit)
	if err != nil {
		e.t.Fatalf("Create: %v", err)
	}
	e.t.Cleanup(func() { e.fx.Workspace.Release(w) })
	return w
}

func (e wsEnv) state(w core.Workdir) core.State {
	e.t.Helper()
	st, err := e.fx.Workspace.State(e.ctx, w)
	if err != nil {
		e.t.Fatalf("State: %v", err)
	}
	return st
}

// owned builds the journal's view of a workspace: the tip is the branch head
// at the end of the run.
func (e wsEnv) owned(w core.Workdir) core.Owned {
	return core.Owned{Session: w.Session, Branch: core.BranchOf(w.Session), Dir: core.DirOf(w.Session), Tip: e.state(w).Head}
}

func (e wsEnv) presence(o core.Owned) core.Presence {
	e.t.Helper()
	p, err := e.fx.Workspace.Inspect(e.ctx, o)
	if err != nil {
		e.t.Fatalf("Inspect: %v", err)
	}
	return p
}

type wsCase struct {
	name    string
	commits bool
	run     func(e wsEnv)
}

var workspaceCases = []wsCase{
	{"resolve a missing ref is ErrBadRef", true, func(e wsEnv) {
		if _, err := e.fx.Workspace.Resolve(e.ctx, "no-such-ref"); !errors.Is(err, core.ErrBadRef) {
			e.t.Fatalf("err = %v", err)
		}
	}},
	{"resolve gives a commit id", true, func(e wsEnv) {
		c, err := e.fx.Workspace.Resolve(e.ctx, "HEAD")
		if err != nil || !core.ValidCommitID(c) {
			e.t.Fatalf("commit %q, err %v", c, err)
		}
	}},
	{"resolve in a repository without commit is ErrNoCommit", false, func(e wsEnv) {
		if _, err := e.fx.Workspace.Resolve(e.ctx, "HEAD"); !errors.Is(err, core.ErrNoCommit) {
			e.t.Fatalf("err = %v", err)
		}
	}},
	{"distinct sessions give distinct workdirs and branches", true, func(e wsEnv) {
		a, b := e.create("ses_a"), e.create("ses_b")
		if a.Dir == b.Dir || a.Branch == b.Branch || a.Branch != core.BranchOf("ses_a") || a.Base != b.Base {
			e.t.Fatalf("a = %+v, b = %+v", a, b)
		}
	}},
	{"the same session twice is ErrExists and changes nothing", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Write(w, "kept.txt", "x")
		before := e.state(w)
		if _, err := e.fx.Workspace.Create(e.ctx, "ses_a", w.Base); !errors.Is(err, core.ErrExists) {
			e.t.Fatalf("err = %v", err)
		}
		if after := e.state(w); after.Tree != before.Tree {
			e.t.Fatal("the first worktree changed")
		}
		if err := e.fx.Workspace.Remove(e.ctx, e.owned(w)); !errors.Is(err, core.ErrBusy) {
			e.t.Fatalf("the first run lost its lock: %v", err)
		}
	}},
	{"create takes the lock until Release", true, func(e wsEnv) {
		w := e.create("ses_a")
		o := e.owned(w)
		if !e.presence(o).Busy {
			e.t.Fatal("not busy right after Create")
		}
		if err := e.fx.Workspace.Remove(e.ctx, o); !errors.Is(err, core.ErrBusy) {
			e.t.Fatalf("Remove while held: %v", err)
		}
		if err := e.fx.Workspace.Release(w); err != nil {
			e.t.Fatal(err)
		}
		if err := e.fx.Workspace.Release(w); err != nil {
			e.t.Fatalf("second Release: %v", err)
		}
		if e.presence(o).Busy {
			e.t.Fatal("still busy after Release")
		}
	}},
	{"state is equal after a no-op and different after a write", true, func(e wsEnv) {
		w := e.create("ses_a")
		first := e.state(w)
		if again := e.state(w); again.Tree != first.Tree || first.Tree == "" {
			e.t.Fatalf("trees %q %q", first.Tree, again.Tree)
		}
		e.fx.Write(w, "new.txt", "x")
		if changed := e.state(w); changed.Tree == first.Tree {
			e.t.Fatal("state did not change after a write")
		}
	}},
	{"state lists a link with its target and mode", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Symlink(w, "ln", "README.md")
		e.fx.Write(w, "file.txt", "x")
		var link, file *core.Entry
		st := e.state(w)
		for i := range st.Entries {
			switch st.Entries[i].Path {
			case "ln":
				link = &st.Entries[i]
			case "file.txt":
				file = &st.Entries[i]
			}
		}
		if link == nil || link.Mode != "120000" || link.Link != "README.md" {
			e.t.Fatalf("link entry = %+v", link)
		}
		if file == nil || file.Mode != "100644" || file.Link != "" || file.Blob == "" {
			e.t.Fatalf("file entry = %+v", file)
		}
	}},
	{"diff includes new, changed and deleted files", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Write(w, "new.txt", "x")
		e.fx.Write(w, "README.md", "changed\n")
		e.fx.Delete(w, "gone.txt")
		var buf strings.Builder
		if err := e.fx.Workspace.Diff(e.ctx, w, e.state(w), &buf, 1<<20); err != nil {
			e.t.Fatal(err)
		}
		for _, name := range []string{"new.txt", "README.md", "gone.txt"} {
			if !strings.Contains(buf.String(), name) {
				e.t.Errorf("diff lacks %s:\n%s", name, buf.String())
			}
		}
	}},
	{"diff over the byte cap fails with ErrDiffTooLarge", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Write(w, "big.txt", strings.Repeat("x", 4096))
		var buf strings.Builder
		if err := e.fx.Workspace.Diff(e.ctx, w, e.state(w), &buf, 10); !errors.Is(err, core.ErrDiffTooLarge) {
			e.t.Fatalf("err = %v", err)
		}
		if buf.Len() > 10 {
			e.t.Fatalf("wrote %d bytes past the cap", buf.Len())
		}
	}},
	{"remove of a session that never existed succeeds", true, func(e wsEnv) {
		o := core.Owned{Session: "ses_never", Branch: core.BranchOf("ses_never"), Dir: core.DirOf("ses_never"), Tip: "0123456789abcdef0123456789abcdef01234567"}
		if err := e.fx.Workspace.Remove(e.ctx, o); err != nil {
			e.t.Fatalf("err = %v", err)
		}
	}},
	{"remove of a foreign folder or a non-derived name is ErrNotOurs and deletes nothing", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Workspace.Release(w)
		good := e.owned(w)
		wrongBranch := good
		wrongBranch.Branch = "main"
		wrongDir := good
		wrongDir.Dir = "../elsewhere"
		for _, o := range []core.Owned{wrongBranch, wrongDir} {
			if err := e.fx.Workspace.Remove(e.ctx, o); !errors.Is(err, core.ErrNotOurs) {
				e.t.Fatalf("Remove(%+v) = %v", o, err)
			}
		}
		e.fx.ForeignDir("ses_foreign")
		foreign := core.Owned{Session: "ses_foreign", Branch: core.BranchOf("ses_foreign"), Dir: core.DirOf("ses_foreign"), Tip: good.Tip}
		if err := e.fx.Workspace.Remove(e.ctx, foreign); !errors.Is(err, core.ErrNotOurs) {
			e.t.Fatalf("Remove(foreign) = %v", err)
		}
		if p := e.presence(good); !p.DirExists || !p.BranchExists {
			e.t.Fatalf("our worktree was touched: %+v", p)
		}
	}},
	{"remove with the lock held by another process is ErrBusy", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Workspace.Release(w)
		o := e.owned(w)
		unlock := e.fx.Lock("ses_a")
		if err := e.fx.Workspace.Remove(e.ctx, o); !errors.Is(err, core.ErrBusy) {
			e.t.Fatalf("err = %v", err)
		}
		if !e.presence(o).Busy {
			e.t.Fatal("Inspect does not see the other lock")
		}
		unlock()
		if err := e.fx.Workspace.Remove(e.ctx, o); err != nil {
			e.t.Fatalf("after unlock: %v", err)
		}
	}},
	{"remove with a moved tip is ErrMoved and deletes nothing", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Workspace.Release(w)
		o := e.owned(w)
		e.fx.MoveTip("ses_a")
		if err := e.fx.Workspace.Remove(e.ctx, o); !errors.Is(err, core.ErrMoved) {
			e.t.Fatalf("err = %v", err)
		}
		if p := e.presence(o); !p.DirExists || !p.BranchExists || p.TipMatches {
			e.t.Fatalf("presence = %+v", p)
		}
	}},
	{"remove twice succeeds", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Workspace.Release(w)
		o := e.owned(w)
		for i := 0; i < 2; i++ {
			if err := e.fx.Workspace.Remove(e.ctx, o); err != nil {
				e.t.Fatalf("Remove #%d: %v", i+1, err)
			}
		}
		if p := e.presence(o); p.DirExists || p.BranchExists || p.Busy {
			e.t.Fatalf("presence after Remove = %+v", p)
		}
	}},
	{"a removed session can be created again", true, func(e wsEnv) {
		w := e.create("ses_a")
		e.fx.Workspace.Release(w)
		if err := e.fx.Workspace.Remove(e.ctx, e.owned(w)); err != nil {
			e.t.Fatal(err)
		}
		e.create("ses_a")
	}},
	{"inspect reports folder, branch, busy and tip", true, func(e wsEnv) {
		w := e.create("ses_a")
		o := e.owned(w)
		if p := e.presence(o); p != (core.Presence{DirExists: true, BranchExists: true, Busy: true, TipMatches: true}) {
			e.t.Fatalf("held: %+v", p)
		}
		e.fx.Workspace.Release(w)
		if p := e.presence(o); p != (core.Presence{DirExists: true, BranchExists: true, TipMatches: true}) {
			e.t.Fatalf("released: %+v", p)
		}
		if p := e.presence(core.Owned{Session: "ses_none", Branch: core.BranchOf("ses_none"), Dir: core.DirOf("ses_none")}); p != (core.Presence{}) {
			e.t.Fatalf("unknown: %+v", p)
		}
	}},
}

// WorkspaceContract checks the behavior every core.Workspace must have. open
// returns a fixture over a fresh repository; with commits false the
// repository has no commit yet.
func WorkspaceContract(t *testing.T, open func(t *testing.T, commits bool) WorkspaceFixture) {
	for _, c := range workspaceCases {
		t.Run(c.name, func(t *testing.T) {
			c.run(wsEnv{t: t, fx: open(t, c.commits), ctx: context.Background()})
		})
	}
}

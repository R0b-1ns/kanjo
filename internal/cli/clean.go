package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

type cleanOpts struct {
	yes           bool
	repo, dataDir string
}

func cleanFlags(fs *flag.FlagSet) *cleanOpts {
	o := &cleanOpts{}
	fs.BoolVar(&o.yes, "yes", false, "remove what is listed and record it as abandoned (default: only list)")
	fs.StringVar(&o.repo, "repo", "", "git working tree root (default: the current folder)")
	dataDirFlag(fs, &o.dataDir)
	return o
}

const nothingToClean = "nothing to clean\n"

func cleanCmd(args []string, stdout, stderr io.Writer, e env) int {
	flags := newFlagSet("clean", stderr)
	o := cleanFlags(flags)
	if code := parseFlags(flags, args); code != proceed {
		return code
	}
	cfg, _, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	ws := workspaceFor(e, repoPath(o.repo, e.cwd), cfg.DataDir)
	cs, err := listCleanable(e, cfg.DataDir, ws)
	if err != nil {
		return fail(stderr, err)
	}
	if len(cs) == 0 {
		fmt.Fprint(stdout, nothingToClean)
		return 0
	}
	if !o.yes {
		listCandidates(stdout, cfg.DataDir, cs)
		return 0
	}
	st, err := sqlitestore.Open(cfg.DataDir)
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	return printCleaned(stdout, core.Clean(e.context(), core.FinishDeps{Journal: st, Workspace: ws, Clock: e.clock}, cs))
}

// listCleanable reads the runs without a verdict. The database is opened
// read-only: only --yes with something to remove writes. A missing database
// has none.
func listCleanable(e env, dataDir string, ws core.Workspace) ([]core.Candidate, error) {
	ro, err := sqlitestore.OpenReadOnly(dataDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer ro.Close()
	return core.Candidates(e.context(), core.FinishDeps{Journal: ro, Workspace: ws, Clock: e.clock})
}

// listCandidates prints one line per run: session, task, state, folder, branch.
func listCandidates(w io.Writer, dataDir string, cs []core.Candidate) {
	for _, c := range cs {
		r := c.Run
		fmt.Fprintln(w, printable(fmt.Sprintf("%s %s %s %s %s", r.Session, r.Task, c.State, filepath.Join(dataDir, core.DirOf(r.Session)), core.BranchOf(r.Session))))
	}
	fmt.Fprintln(w, "run kanjo clean --yes to remove these worktrees and branches and record the runs as abandoned")
}

// printCleaned prints one line per result; any failure makes the exit code 1.
func printCleaned(w io.Writer, results []core.CleanResult) int {
	code := 0
	for _, r := range results {
		session := r.Candidate.Run.Session
		switch {
		case r.Skipped:
			fmt.Fprintf(w, "skipped (running) %s\n", session)
		case r.Err != nil:
			code = 1
			fmt.Fprintln(w, printable(fmt.Sprintf("failed %s: %v", session, r.Err)))
		default:
			fmt.Fprintf(w, "removed %s\n", session)
		}
	}
	return code
}

package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/R0b-1ns/kanjo/internal/config"
	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/gitworktree"
	"github.com/R0b-1ns/kanjo/internal/pricesfile"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

type finishOpts struct{ verdict, repo, dataDir string }

func finishFlags(fs *flag.FlagSet) *finishOpts {
	o := &finishOpts{}
	fs.StringVar(&o.verdict, "verdict", "", "your verdict: accepted, modified or rejected (required)")
	fs.StringVar(&o.repo, "repo", "", "git working tree root (default: the current folder)")
	dataDirFlag(fs, &o.dataDir)
	return o
}

const finishUsage = "usage: kanjo finish <session> --verdict accepted|modified|rejected [--repo DIR] [--data-dir DIR]\n"

func finishCmd(args []string, stdout, stderr io.Writer, e env) int {
	fs := newFlagSet("finish", stderr)
	o := finishFlags(fs)
	session, code := parseFinishArgs(fs, args)
	if code != proceed {
		return code
	}
	verdict, err := o.check(session)
	if err != nil {
		fmt.Fprintf(stderr, "kanjo finish: %v\n%s", err, finishUsage)
		return 2
	}
	cfg, book, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	repo := o.repo
	if repo == "" {
		repo = e.cwd
	}
	if !filepath.IsAbs(repo) {
		repo = filepath.Join(e.cwd, repo)
	}
	var ws core.Workspace = gitworktree.New(repo, cfg.DataDir)
	if e.ports != nil {
		ws = e.ports.Workspace
	}
	st, err := sqlitestore.Open(cfg.DataDir)
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	out, err := core.Finish(e.context(), core.FinishDeps{Journal: st, Workspace: ws, Clock: e.clock}, session, verdict)
	if err != nil {
		return fail(stderr, finishError(err))
	}
	printFinish(stdout, cfg.DataDir, repo, session, verdict, out)
	afterFinish(stdout, stderr, e, cfg, book, st, out.Run)
	return 0
}

// parseFinishArgs parses the options and the one positional session id, which
// may come before or after the options.
func parseFinishArgs(fs *flag.FlagSet, args []string) (string, int) {
	if code := parseFlagsLeaving(fs, args); code != proceed {
		return "", code
	}
	rest := fs.Args()
	session := ""
	if len(rest) > 0 {
		session = rest[0]
		if code := parseFlagsLeaving(fs, rest[1:]); code != proceed {
			return "", code
		}
		rest = fs.Args()
	}
	if len(rest) > 0 {
		fmt.Fprintf(fs.Output(), "kanjo finish: unexpected argument %q\n", rest[0])
		return "", 2
	}
	return session, proceed
}

// check validates the session and the verdict before any file is touched.
func (o *finishOpts) check(session string) (core.LeadVerdict, error) {
	v := core.LeadVerdict(o.verdict)
	switch {
	case !core.ValidSession(session):
		return "", errors.New("a valid session id is required")
	case v == core.Abandoned:
		return "", errors.New("abandoned is recorded by kanjo clean, not by finish")
	case v != core.Accepted && v != core.Modified && v != core.Rejected:
		return "", errors.New("--verdict must be accepted, modified or rejected")
	}
	return v, nil
}

// finishError words the refusals of finish.
func finishError(err error) error {
	switch {
	case errors.Is(err, core.ErrUnknownRun):
		return errors.New("no run with that session in this data folder")
	case errors.Is(err, core.ErrStillRunning):
		return errors.New("the run is still running; wait for it to end")
	case errors.Is(err, core.ErrMoved):
		return errors.New("the branch of the run moved since it ended (a commit was made on it?): nothing was removed and no verdict recorded; resolve it with git, then run finish again")
	case errors.Is(err, core.ErrNotOurs):
		return errors.New("the folder of the run is not a worktree kanjo created: nothing was removed")
	}
	return err
}

func printFinish(stdout io.Writer, dataDir, repo, session string, v core.LeadVerdict, out core.FinishOutcome) {
	if out.AlreadyRecorded {
		fmt.Fprintf(stdout, "finish: %s %s (already recorded)\n", session, v)
		return
	}
	fmt.Fprintf(stdout, "finish: %s %s\n", session, v)
	switch {
	case !out.Run.Created:
		fmt.Fprintln(stdout, "no workspace was created")
	case out.WorkspaceGone:
		fmt.Fprintln(stdout, "workspace already removed")
	default:
		fmt.Fprintln(stdout, "workspace removed")
	}
	if v == core.Rejected {
		return
	}
	patch := filepath.Join(dataDir, "runs", session, "changes.patch")
	if _, err := os.Stat(patch); err != nil {
		fmt.Fprintln(stdout, "no patch was kept for this run")
		return
	}
	fmt.Fprintln(stdout, printable("apply: git -C "+core.QuoteShell(repo)+" apply "+core.QuoteShell(patch)))
}

// afterFinish takes a Claude snapshot and prints the task totals. A snapshot
// that fails is a warning: the verdict is already recorded.
func afterFinish(stdout, stderr io.Writer, e env, cfg config.Config, book *pricesfile.Book, st *sqlitestore.Store, run core.RunRecord) {
	if err := finishSnapshot(stdout, e, cfg, book, st); err != nil {
		fmt.Fprintf(stderr, "warning: snapshot failed: %s\n", printable(err.Error()))
	}
	if run.Task == core.NoTask {
		return
	}
	rows, err := st.Rows(e.context())
	if err == nil {
		var sessions []core.SessionInfo
		if sessions, err = st.Sessions(e.context()); err == nil {
			rep := core.Aggregate(rows, sessions, book, core.Query{By: "task", Task: run.Task})
			writeText(stdout, "task", rep, book)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "warning: task totals: %s\n", printable(err.Error()))
	}
}

func finishSnapshot(stdout io.Writer, e env, cfg config.Config, book *pricesfile.Book, st *sqlitestore.Store) error {
	src, err := claudeSource(cfg, book)
	if err != nil {
		return err
	}
	log, err := snapshotInto(e.context(), src, st, e.clock)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, snapshotLine(log, cfg.DataDir))
	return nil
}

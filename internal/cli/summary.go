package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

type summaryOpts struct{ task, since, dataDir string }

func summaryFlags(fs *flag.FlagSet) *summaryOpts {
	o := &summaryOpts{}
	fs.StringVar(&o.task, "task", "", "only runs of this task label (default: all)")
	fs.StringVar(&o.since, "since", "", "only runs started on or after this date, YYYY-MM-DD, UTC (default: all)")
	dataDirFlag(fs, &o.dataDir)
	return o
}

const noRuns = "no runs\n"

var (
	scriptOrder = []core.RunVerdict{core.RunOK, core.RunStray, core.RunFailed, core.RunTimeout, core.RunQuota, core.RunInterrupted, core.RunRunning}
	leadOrder   = []core.LeadVerdict{core.Accepted, core.Modified, core.Rejected, core.Abandoned}
)

func summaryCmd(args []string, stdout, stderr io.Writer, e env) int {
	flags := newFlagSet("summary", stderr)
	o := summaryFlags(flags)
	if code := parseFlags(flags, args); code != proceed {
		return code
	}
	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	since, err := o.parse(given)
	if err != nil {
		fmt.Fprintf(stderr, "kanjo summary: %v\n", err)
		return 2
	}
	cfg, _, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	st, err := sqlitestore.OpenReadOnly(cfg.DataDir)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprint(stdout, noRuns)
		return 0
	}
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	recs, err := st.Runs(e.context())
	if err != nil {
		return fail(stderr, err)
	}
	s := core.Summarize(recs, o.task, since)
	if s.Runs == 0 {
		fmt.Fprint(stdout, noRuns)
		return 0
	}
	printSummary(stdout, s)
	return 0
}

// parse checks --task and --since before any file is touched.
func (o *summaryOpts) parse(given map[string]bool) (time.Time, error) {
	if given["task"] && (o.task == core.NoTask || !core.ValidTask(o.task)) {
		return time.Time{}, errors.New("--task must be a valid task label")
	}
	if !given["since"] {
		return time.Time{}, nil
	}
	if !core.ValidDay(o.since) {
		return time.Time{}, errors.New("--since must be a date, YYYY-MM-DD")
	}
	return time.Parse("2006-01-02", o.since)
}

func printSummary(w io.Writer, s core.Summary) {
	line := func(label string, v any) { fmt.Fprintf(w, "%s  %v\n", label, v) }
	line("runs", s.Runs)
	line("tasks", s.Tasks)
	line("runs without a task", s.Unlabelled)
	line("launches", s.Launches)
	line("retries", s.Retries)
	line("duration", s.Duration)
	fmt.Fprintln(w, "script verdicts")
	for _, v := range scriptOrder {
		if n := s.Script[v]; n > 0 {
			fmt.Fprintf(w, "  %s  %d\n", v, n)
		}
	}
	fmt.Fprintln(w, "lead verdicts")
	for _, v := range leadOrder {
		if n := s.Lead[v]; n > 0 {
			fmt.Fprintf(w, "  %s  %d\n", v, n)
		}
	}
	fmt.Fprintf(w, "  no lead verdict  %d\n", s.NoLead)
}

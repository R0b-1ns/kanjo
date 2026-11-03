package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/R0b-1ns/kanjo/internal/config"
	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/pricesfile"
	"github.com/R0b-1ns/kanjo/internal/projects"
)

const (
	noData     = "no data; run kanjo snapshot\n"
	noProjects = "no projects; set projects in the user config\n"
)

type usageOpts struct {
	by, task, since, dataDir string
	csv, all                 bool
}

func usageFlags(fs *flag.FlagSet) *usageOpts {
	o := &usageOpts{}
	fs.StringVar(&o.by, "by", "", "group by: task, model, day or session; with --all: project, task, model or day (default: task, or project with --all)")
	fs.BoolVar(&o.all, "all", false, "report every project listed in projects in the user config")
	fs.StringVar(&o.task, "task", "", "only this task label (default: all)")
	fs.StringVar(&o.since, "since", "", "only days on or after this date, YYYY-MM-DD (default: all)")
	fs.BoolVar(&o.csv, "csv", false, "print CSV instead of a table")
	dataDirFlag(fs, &o.dataDir)
	return o
}

func usageCmd(args []string, stdout, stderr io.Writer, e env) int {
	fs := newFlagSet("usage", stderr)
	o := usageFlags(fs)
	if code := parseFlags(fs, args); code != proceed {
		return code
	}
	q := o.query()
	if err := o.validate(q); err != nil {
		fmt.Fprintf(stderr, "kanjo usage: %v\n", err)
		return 2
	}
	cfg, book, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	if o.all {
		return usageAll(o, q, cfg, book, stdout, stderr)
	}
	rows, sessions, err := projects.ReadStore(cfg.DataDir)
	if err != nil {
		return fail(stderr, err)
	}
	if len(rows) == 0 {
		return emptyReport(o.csv, q.By, noData, stdout, stderr)
	}
	rep := core.Aggregate(rows, sessions, book, q)
	if o.csv {
		return writeCSV(stdout, stderr, q.By, rep)
	}
	writeText(stdout, q.By, rep, book)
	return 0
}

// query resolves the default grouping: task, or project with --all.
func (o *usageOpts) query() core.Query {
	by := o.by
	if by == "" {
		by = "task"
		if o.all {
			by = "project"
		}
	}
	return core.Query{By: by, Task: o.task, Since: o.since}
}

func (o *usageOpts) validate(q core.Query) error {
	if o.all {
		return q.ValidateAll()
	}
	return q.Validate()
}

// usageAll reports every configured project together. A project that cannot
// be read is named on stderr and the others are still reported.
func usageAll(o *usageOpts, q core.Query, cfg config.Config, book *pricesfile.Book, stdout, stderr io.Writer) int {
	res := projects.Read(context.Background(), cfg.Projects)
	for _, name := range res.Failed {
		fmt.Fprintf(stderr, "warning: project %s could not be read\n", name)
	}
	if len(cfg.Projects) == 0 || !hasRows(res.Projects) {
		hint := noData
		if len(cfg.Projects) == 0 {
			hint = noProjects
		}
		return emptyReport(o.csv, q.By, hint, stdout, stderr)
	}
	rep := core.AggregateProjects(res.Projects, book, q)
	if o.csv {
		return writeCSV(stdout, stderr, q.By, rep)
	}
	writeText(stdout, q.By, rep, book)
	return 0
}

func hasRows(ps []core.ProjectUsage) bool {
	for _, p := range ps {
		if len(p.Rows) > 0 {
			return true
		}
	}
	return false
}

// emptyReport prints the hint: on stdout, or on stderr with --csv so that
// stdout stays a valid CSV (header only).
func emptyReport(csv bool, by, hint string, stdout, stderr io.Writer) int {
	if csv {
		fmt.Fprint(stderr, hint)
		return writeRecords(stdout, stderr, table(by, core.Report{}))
	}
	fmt.Fprint(stdout, hint)
	return 0
}

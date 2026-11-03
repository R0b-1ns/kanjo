package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

const noData = "no data; run kanjo snapshot\n"

type usageOpts struct {
	by, task, since, dataDir string
	csv                      bool
}

func usageFlags(fs *flag.FlagSet) *usageOpts {
	o := &usageOpts{}
	fs.StringVar(&o.by, "by", "task", "group by: task, model, day or session")
	fs.StringVar(&o.task, "task", "", "only this task label (default: all)")
	fs.StringVar(&o.since, "since", "", "only days on or after this date, YYYY-MM-DD (default: all)")
	fs.BoolVar(&o.csv, "csv", false, "print CSV instead of a table")
	fs.StringVar(&o.dataDir, "data-dir", "", "data folder (default: data_dir from the config, ./.ai-usage)")
	return o
}

func usageCmd(args []string, stdout, stderr io.Writer, e env) int {
	fs := newFlagSet("usage", stderr)
	o := usageFlags(fs)
	if code := parseFlags(fs, args); code != proceed {
		return code
	}
	q := core.Query{By: o.by, Task: o.task, Since: o.since}
	if err := q.Validate(); err != nil {
		fmt.Fprintf(stderr, "kanjo usage: %v\n", err)
		return 2
	}
	cfg, book, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	rows, sessions, err := readStore(cfg.DataDir)
	if err != nil {
		return fail(stderr, err)
	}
	if len(rows) == 0 {
		if o.csv { // keep stdout a valid CSV: the hint goes to stderr
			fmt.Fprint(stderr, noData)
			return writeRecords(stdout, stderr, table(q.By, core.Report{}))
		}
		fmt.Fprint(stdout, noData)
		return 0
	}
	rep := core.Aggregate(rows, sessions, book, q)
	if o.csv {
		return writeCSV(stdout, stderr, q.By, rep)
	}
	writeText(stdout, q.By, rep, book)
	return 0
}

// readStore reads every saved row without writing anything: a data folder
// that does not exist yet reads as empty.
func readStore(dir string) ([]core.UsageRow, []core.SessionInfo, error) {
	st, err := sqlitestore.OpenReadOnly(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer st.Close()
	rows, err := st.Rows(context.Background())
	if err != nil {
		return nil, nil, err
	}
	sessions, err := st.Sessions(context.Background())
	return rows, sessions, err
}

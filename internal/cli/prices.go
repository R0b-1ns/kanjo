package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/projects"
)

// exitWarnings is the exit code of prices check when it printed a warning.
const exitWarnings = 3

type pricesCheckOpts struct {
	dataDir string
	all     bool
}

func pricesCheckFlags(fs *flag.FlagSet) *pricesCheckOpts {
	o := &pricesCheckOpts{}
	fs.BoolVar(&o.all, "all", false, "also look for unpriced models in every project listed in projects in the user config")
	dataDirFlag(fs, &o.dataDir)
	return o
}

func pricesCmd(args []string, stdout, stderr io.Writer, e env) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, "usage: kanjo prices check [--all] [--data-dir DIR]")
		return 2
	}
	fs := newFlagSet("prices check", stderr)
	o := pricesCheckFlags(fs)
	if code := parseFlags(fs, args[1:]); code != proceed {
		return code
	}
	cfg, book, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "prices: %s, retrieved %s\n", book.Source(), book.Fetched())
	age, warnings := priceAge(book.Fetched(), e.clock.Now(), cfg.Prices.MaxAgeDays)
	fmt.Fprint(stdout, age)
	data, err := checkedRows(o.all, cfg.DataDir, cfg.Projects)
	if err != nil {
		return fail(stderr, err)
	}
	if o.all && len(cfg.Projects) == 0 {
		warnings = append(warnings, "no projects listed in the user config; nothing was checked")
	}
	warnings = append(warnings, unpricedWarnings(data, book)...)
	for _, w := range warnings {
		fmt.Fprintln(stdout, "warning: "+w)
	}
	if len(warnings) > 0 {
		return exitWarnings
	}
	return 0
}

// priceAge describes how old the price file is: a report line and the
// warnings. It warns when the file is strictly older than maxDays whole UTC
// days, or dated in the future.
func priceAge(fetched string, now time.Time, maxDays int) (line string, warnings []string) {
	t, err := time.Parse("2006-01-02", fetched)
	if err != nil { // Parse already validated the date
		return "", nil
	}
	// A date up to one day ahead is today in a timezone ahead of UTC.
	if t.Sub(now) > 24*time.Hour {
		return "", []string{"price file date is in the future"}
	}
	days := int(now.Sub(t) / (24 * time.Hour)) // 0 for a date one day ahead
	line = fmt.Sprintf("age: %d days (limit %d)\n", days, maxDays)
	if days > maxDays {
		warnings = []string{fmt.Sprintf("prices are %d days old (limit %d); update the file", days, maxDays)}
	}
	return line, warnings
}

// checkedRows reads the rows to look for unpriced models in: this project's,
// or every configured project's. Unreadable projects are an error: a check
// that silently skips data would say "all priced" wrongly.
func checkedRows(all bool, dataDir string, folders []string) ([]core.UsageRow, error) {
	if !all {
		rows, _, err := projects.ReadStore(dataDir)
		return rows, err
	}
	res := projects.Read(context.Background(), folders)
	if len(res.Failed) > 0 {
		return nil, fmt.Errorf("project %s could not be read", res.Failed[0])
	}
	rows, _ := core.Flatten(res.Projects)
	return rows, nil
}

// unpricedWarnings names each model of rows that the price book does not know.
func unpricedWarnings(rows []core.UsageRow, pb core.PriceBook) []string {
	messages := map[string]int64{}
	for _, r := range rows {
		if _, priced := core.Cost(r, pb); !priced {
			messages[r.Model] += r.Messages
		}
	}
	var out []string
	for _, model := range slices.Sorted(maps.Keys(messages)) {
		out = append(out, fmt.Sprintf("model %s has no price (%d messages)", model, messages[model]))
	}
	return out
}

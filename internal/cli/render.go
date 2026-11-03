package cli

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/pricesfile"
)

const estimateNotice = "Claude cost is an estimate at API list prices, not a subscription bill."

// table is the report as records: a header, then one line per group. An
// unpriced group has an empty est_usd, never 0.
func table(by string, rep core.Report) [][]string {
	out := [][]string{{by, "messages", "input", "output", "reasoning", "cache_write_5m", "cache_write_1h", "cache_read", "est_usd", "unpriced_messages"}}
	for _, g := range rep.Groups {
		est := ""
		if g.Priced {
			est = usd(g.USD)
		}
		out = append(out, []string{g.Name, n(g.Messages), n(g.Input), n(g.Output), n(g.Reasoning),
			n(g.CacheWrite5m), n(g.CacheWrite1h), n(g.CacheRead), est, n(g.UnpricedMessages)})
	}
	return out
}

func n(v int64) string { return strconv.FormatInt(v, 10) }

// usd keeps four decimals: cheap models cost fractions of a cent.
func usd(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

func total(rep core.Report) string {
	return fmt.Sprintf("total est_usd %s (%d messages unpriced)", usd(rep.TotalUSD), rep.UnpricedMessages)
}

func warnings(rep core.Report) []string {
	var out []string
	for _, s := range rep.MultiLabelSessions {
		out = append(out, fmt.Sprintf("warning: session %s mentions several tasks; counted on one task", s))
	}
	return out
}

// writeCSV prints the table on stdout; totals, warnings and the notice go to
// stderr so the CSV stays machine-readable.
func writeCSV(stdout, stderr io.Writer, by string, rep core.Report) int {
	w := csv.NewWriter(stdout)
	if err := w.WriteAll(table(by, rep)); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stderr, total(rep))
	for _, line := range warnings(rep) {
		fmt.Fprintln(stderr, line)
	}
	fmt.Fprintln(stderr, estimateNotice)
	return 0
}

func writeText(w io.Writer, by string, rep core.Report, book *pricesfile.Book) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, rec := range table(by, rep) {
		fmt.Fprintln(tw, strings.Join(rec, "\t"))
	}
	tw.Flush()
	fmt.Fprintln(w, total(rep))
	for _, line := range warnings(rep) {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "%s\nprices: %s, retrieved %s\n", estimateNotice, book.Source(), book.Fetched())
}

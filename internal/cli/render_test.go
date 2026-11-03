package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteCSVReportsAWriteFailure(t *testing.T) {
	var errb bytes.Buffer
	rep := core.Report{Groups: []core.Group{{Name: "ABC-1", Priced: true}}}
	if code := writeCSV(brokenWriter{}, &errb, "task", rep); code != 1 || !strings.Contains(errb.String(), "disk full") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
	if strings.Contains(errb.String(), "total est_usd") {
		t.Fatal("no totals after a failed write")
	}
}

func TestWriteCSVKeepsTotalsWarningsAndNoticeOffStdout(t *testing.T) {
	var out, errb bytes.Buffer
	rep := core.Report{
		Groups:             []core.Group{{Name: "ABC-1", Priced: true, USD: 1.5, Messages: 2}},
		TotalUSD:           1.5,
		UnpricedMessages:   3,
		MultiLabelSessions: []string{"s-9"},
	}
	if code := writeCSV(&out, &errb, "task", rep); code != 0 {
		t.Fatalf("code %d", code)
	}
	if strings.Contains(out.String(), "total") || strings.Contains(out.String(), "warning") {
		t.Fatalf("stdout must hold only CSV: %q", out.String())
	}
	for _, want := range []string{"total est_usd 1.5000 (3 messages unpriced)", "warning: session s-9 mentions several tasks", estimateNotice} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr lacks %q: %q", want, errb.String())
		}
	}
}

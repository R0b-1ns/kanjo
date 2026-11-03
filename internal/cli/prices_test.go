package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixedNow is 2026-11-03 12:00 UTC: a file fetched on 2026-10-04 is 30 whole
// days old, one fetched on 2026-10-03 is 31.
func (w *world) pricesFetched(day string) {
	w.t.Helper()
	path := filepath.Join(filepath.Dir(w.data), "prices.json")
	write(w.t, path, `{"source":"https://example.test/prices","fetched":"`+day+`","models":{"claude-sonnet-5-5":{"input":2,"output":10,"cache_write_5m":2.5,"cache_write_1h":4,"cache_read":0.1}}}`)
	w.topLevel("prices_file = \"" + path + "\"\n")
}

func TestPricesCheckAtThirtyDaysDoesNotWarn(t *testing.T) {
	w := newWorld(t)
	w.pricesFetched("2026-10-04")
	code, out, _ := w.run("prices", "check")
	if code != 0 || !strings.Contains(out, "age: 30 days (limit 30)") || strings.Contains(out, "warning") {
		t.Fatalf("code %d\n%s", code, out)
	}
	if !strings.Contains(out, "prices: https://example.test/prices, retrieved 2026-10-04") {
		t.Fatalf("source line missing:\n%s", out)
	}
}

func TestPricesCheckAtThirtyOneDaysWarns(t *testing.T) {
	w := newWorld(t)
	w.pricesFetched("2026-10-03")
	code, out, _ := w.run("prices", "check")
	if code != 3 || !strings.Contains(out, "warning: prices are 31 days old (limit 30)") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestPricesCheckLimitIsConfigurable(t *testing.T) {
	w := newWorld(t)
	w.pricesFetched("2026-10-27") // 7 days old
	w.must("prices", "check")
	write(t, filepath.Join(w.home, ".config", "kanjo", "config.toml"), mustRead(t, filepath.Join(w.home, ".config", "kanjo", "config.toml"))+"[prices]\nmax_age_days = 6\n")
	if code, out, _ := w.run("prices", "check"); code != 3 || !strings.Contains(out, "7 days old (limit 6)") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPricesCheckFutureDateWarns(t *testing.T) {
	w := newWorld(t)
	w.pricesFetched("2026-11-04")
	if code, out, _ := w.run("prices", "check"); code != 3 || !strings.Contains(out, "warning: price file date is in the future") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestPricesCheckInvalidFileExits1(t *testing.T) {
	w := newWorld(t)
	path := filepath.Join(filepath.Dir(w.data), "bad.json")
	write(t, path, `{"source":"x","fetched":"2026-10-04","models":{"claude-sonnet-5-5":{"input":-1}}}`)
	w.topLevel("prices_file = \"" + path + "\"\n")
	if code, _, errb := w.run("prices", "check"); code != 1 || !strings.Contains(errb, "prices file") {
		t.Fatalf("code %d stderr %q", code, errb)
	}
}

func TestPricesCheckListsUnpricedModelsOfTheData(t *testing.T) {
	w := newWorld(t)
	w.pricesFetched("2026-11-01")
	w.must("snapshot")
	code, out, _ := w.run("prices", "check")
	if code != 3 || !strings.Contains(out, "warning: model claude-unknown-9 has no price (1 messages)") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestPricesCheckAllLooksAtEveryProject(t *testing.T) {
	w := newWorld(t)
	w.twoProjects()
	w.pricesFetched("2026-11-01")
	code, out, _ := w.run("prices", "check", "--all")
	if code != 3 || !strings.Contains(out, "model claude-opus-5-5 has no price (2 messages)") || !strings.Contains(out, "model claude-unknown-9 has no price (2 messages)") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestPricesCheckWithoutDatabaseCreatesNothing(t *testing.T) {
	w := newWorld(t)
	w.pricesFetched("2026-11-01")
	if code, out, _ := w.run("prices", "check"); code != 0 || strings.Contains(out, "warning") {
		t.Fatalf("code %d\n%s", code, out)
	}
	if _, err := os.Stat(w.data); !os.IsNotExist(err) {
		t.Fatalf("data folder was created: %v", err)
	}
}

func TestPricesNeedsTheCheckSubcommand(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{{"prices"}, {"prices", "fetch"}} {
		if code, _, errb := w.run(args...); code != 2 || !strings.Contains(errb, "prices check") {
			t.Errorf("%v: code %d stderr %q", args, code, errb)
		}
	}
}

func TestPricesCheckAllRefusesAnUnreadableProject(t *testing.T) {
	w := newWorld(t)
	_, b := w.twoProjects()
	w.pricesFetched("2026-11-01")
	write(t, filepath.Join(b, ".ai-usage", "kanjo.db"), "garbage that is not a sqlite database at all, padded to be long enough........")
	code, out, errb := w.run("prices", "check", "--all")
	if code != 1 || !strings.Contains(errb, "project app-b could not be read") || strings.Contains(out, "has no price") {
		t.Fatalf("a check that skips unreadable data would wrongly say all is priced: code %d out %q err %q", code, out, errb)
	}
}

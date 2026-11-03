package cli

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/web"
)

// syncBuffer is a bytes.Buffer safe to read while the server writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type running struct {
	base           string // http://127.0.0.1:port
	port           int
	stdout, stderr *syncBuffer
	stop           func() int
}

// serve starts "kanjo serve --port 0 ..." and waits for its address line.
func (w *world) serve(extra ...string) *running {
	w.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{stdout: &syncBuffer{}, stderr: &syncBuffer{}}
	code := make(chan int, 1)
	go func() {
		code <- run(append([]string{"serve", "--port", "0"}, extra...), r.stdout, r.stderr,
			env{home: w.home, cwd: w.cwd, clock: kanjotest.FixedClock{T: fixedNow}, ctx: ctx})
	}()
	r.stop = func() int { cancel(); return <-code }
	w.t.Cleanup(func() { cancel() })
	re := regexp.MustCompile(`serving on http://127\.0\.0\.1:(\d+)/`)
	for i := 0; i < 100; i++ {
		if m := re.FindStringSubmatch(r.stdout.String()); m != nil {
			fmt.Sscan(m[1], &r.port)
			r.base = "http://127.0.0.1:" + m[1]
			return r
		}
		select {
		case c := <-code:
			w.t.Fatalf("serve exited %d early: %s", c, r.stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	w.t.Fatalf("serve did not start: %q %q", r.stdout.String(), r.stderr.String())
	return nil
}

func fetch(t *testing.T, url, host string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func apiData(t *testing.T, r *running, query string) web.Data {
	t.Helper()
	code, body := fetch(t, r.base+"/api/data"+query, "")
	var d web.Data
	if code != 200 || json.Unmarshal([]byte(body), &d) != nil {
		t.Fatalf("code %d body %s", code, body)
	}
	return d
}

func TestServeAnswersOnlyOnLoopbackWithItsOwnHost(t *testing.T) {
	w := newWorld(t)
	r := w.serve()
	if !strings.HasPrefix(r.stdout.String(), "serving on http://127.0.0.1:") {
		t.Fatalf("stdout = %q", r.stdout.String())
	}
	if code, _ := fetch(t, r.base+"/api/data", ""); code != 200 {
		t.Fatalf("code %d", code)
	}
	if code, _ := fetch(t, r.base+"/api/data", "evil.example"); code != 403 {
		t.Fatalf("foreign Host: code %d, want 403", code)
	}
	if code := r.stop(); code != 0 {
		t.Fatalf("exit %d after a clean stop; stderr %q", code, r.stderr.String())
	}
}

func TestServeLogsNothingAndCreatesNothing(t *testing.T) {
	w := newWorld(t)
	r := w.serve()
	fetch(t, r.base+"/", "")
	fetch(t, r.base+"/nope", "")
	fetch(t, r.base+"/", "evil.example")
	r.stop()
	if r.stderr.String() != "" {
		t.Fatalf("serve wrote to stderr: %q", r.stderr.String())
	}
	if _, err := os.Stat(w.data); !os.IsNotExist(err) {
		t.Fatalf("serve created the data folder: %v", err)
	}
}

func TestServeBusyPortFailsWithoutFallback(t *testing.T) {
	w := newWorld(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	code, out, errb := w.run("serve", "--port", fmt.Sprint(port))
	if code != 1 || out != "" || errb == "" {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errb)
	}
}

func TestServeBadPort(t *testing.T) {
	w := newWorld(t)
	for _, p := range []string{"65536", "-1", "abc"} {
		if code, _, _ := w.run("serve", "--port", p); code != 2 {
			t.Errorf("--port %s: code %d, want 2", p, code)
		}
	}
}

func TestServeUsesTheConfiguredPortWhenNoFlagIsGiven(t *testing.T) {
	w := newWorld(t)
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfgPath := w.home + "/.config/kanjo/config.toml"
	b, _ := os.ReadFile(cfgPath)
	write(t, cfgPath, string(b)+fmt.Sprintf("[serve]\nport = %d\n", port))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	go run([]string{"serve"}, out, &syncBuffer{}, env{home: w.home, cwd: w.cwd, clock: kanjotest.FixedClock{T: fixedNow}, ctx: ctx})
	want := fmt.Sprintf("serving on http://127.0.0.1:%d/", port)
	for i := 0; i < 100 && !strings.Contains(out.String(), want); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out.String(), want) {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
}

// The page and `usage` read the same rows through the same aggregation: the
// numbers below are computed by hand from the fixtures (see usage tests).
func TestPageAndCLIGiveTheSameTotalsForTheProject(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	r := w.serve()
	d := apiData(t, r, "")
	if len(d.Tasks) != 2 || d.Tasks[0].Name != "ABC-12" || d.Tasks[1].Name != "XYZ-3" {
		t.Fatalf("tasks = %+v", d.Tasks)
	}
	sameAsCSV(t, d.Tasks, w.must("usage", "--csv"))
	sameAsCSV(t, d.Models, w.must("usage", "--by", "model", "--csv"))
	sameAsCSV(t, d.Days, w.must("usage", "--by", "day", "--csv"))
	if fmt.Sprintf("%.4f", d.TotalUSD) != "0.0527" || d.UnpricedMessages != 1 {
		t.Fatalf("total %v unpriced %d, want 0.0527 and 1", d.TotalUSD, d.UnpricedMessages)
	}
}

func TestPageAndCLIGiveTheSameTotalsForAllProjects(t *testing.T) {
	w := newWorld(t)
	w.twoProjects()
	r := w.serve()
	d := apiData(t, r, "?scope=all")
	sameAsCSV(t, d.Projects, w.must("usage", "--all", "--csv"))
	sameAsCSV(t, d.Tasks, w.must("usage", "--all", "--by", "task", "--csv"))
	if fmt.Sprintf("%.4f", d.TotalUSD) != "12.0000" || d.UnpricedMessages != 2 || fmt.Sprintf("%.4f", *d.Projects[0].USD) != "2.0000" || fmt.Sprintf("%.4f", *d.Projects[1].USD) != "10.0000" {
		t.Fatalf("data = %+v", d)
	}
}

func TestScopeAllNamesAnUnreadableProject(t *testing.T) {
	w := newWorld(t)
	_, b := w.twoProjects()
	write(t, b+"/.ai-usage/kanjo.db", "garbage that is not a sqlite database at all, padded to be long enough........")
	d := apiData(t, w.serve(), "?scope=all")
	if len(d.FailedProjects) != 1 || d.FailedProjects[0] != "app-b" || len(d.Projects) != 1 {
		t.Fatalf("data = %+v", d)
	}
}

func TestCanaryNeverInPageOrJSON(t *testing.T) {
	w := newWorld(t)
	w.must("snapshot")
	r := w.serve()
	for _, p := range []string{"/", "/app.js", "/app.css", "/api/data", "/api/data?scope=all"} {
		if _, body := fetch(t, r.base+p, ""); strings.Contains(body, "CANARY") || strings.Contains(body, w.home) || strings.Contains(body, w.data) {
			t.Errorf("%s leaks transcript text or a path", p)
		}
	}
}

// sameAsCSV checks every group against the CSV `usage` printed: name,
// messages, tokens and the four-decimal cost (empty when unpriced).
func sameAsCSV(t *testing.T, groups []web.GroupData, csvText string) {
	t.Helper()
	recs, err := csv.NewReader(strings.NewReader(csvText)).ReadAll()
	if err != nil || len(recs) != len(groups)+1 {
		t.Fatalf("csv has %d records for %d groups (%v):\n%s", len(recs), len(groups), err, csvText)
	}
	for i, g := range groups {
		est := ""
		if g.USD != nil {
			est = fmt.Sprintf("%.4f", *g.USD)
		}
		want := []string{g.Name, fmt.Sprint(g.Messages), fmt.Sprint(g.Input), fmt.Sprint(g.Output), fmt.Sprint(g.Reasoning),
			fmt.Sprint(g.CacheWrite5m), fmt.Sprint(g.CacheWrite1h), fmt.Sprint(g.CacheRead), est, fmt.Sprint(g.UnpricedMessages)}
		if strings.Join(recs[i+1], ",") != strings.Join(want, ",") {
			t.Errorf("group %d: csv %v, json %v", i, recs[i+1], want)
		}
	}
}

func TestServeAcceptsTheHighestPort(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop at once: only the port check matters
	var out, errb bytes.Buffer
	code := run([]string{"serve", "--port", "65535"}, &out, &errb, env{home: w.home, cwd: w.cwd, clock: kanjotest.FixedClock{T: fixedNow}, ctx: ctx})
	if code == 2 {
		t.Fatalf("65535 is a valid port: %s", errb.String())
	}
}

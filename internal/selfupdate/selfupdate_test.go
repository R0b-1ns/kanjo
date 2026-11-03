package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	oldBinary = "old binary"
	newBinary = "new binary contents"
	tag       = "v0.0.0-20261103120000-aaaaaaaaaaaa"
)

func sum(data string) string { h := sha256.Sum256([]byte(data)); return hex.EncodeToString(h[:]) }

// fakeRelease is a GitHub-like server. Each field can be tweaked by a test
// before the first request.
type fakeRelease struct {
	srv      *httptest.Server
	binary   string
	sums     string // empty = correct sums for binary
	assetURL func(base, name string) string
	redirect string // when set, /dl/ answers with a redirect to this URL
	status   map[string]int
	hits     atomic.Int32
	latest   string // raw body for /latest, empty = generated
}

func newFake(t *testing.T) *fakeRelease {
	t.Helper()
	f := &fakeRelease{binary: newBinary, status: map[string]int{}}
	f.assetURL = func(base, name string) string { return base + "/dl/" + name }
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if code := f.status["/latest"]; code != 0 {
			http.Error(w, "secret server detail", code)
			return
		}
		body := f.latest
		if body == "" {
			body = fmt.Sprintf(`{"tag_name":%q,"assets":[{"name":"kanjo-linux-amd64","browser_download_url":%q},{"name":"kanjo-darwin-arm64","browser_download_url":%q},{"name":"SHA256SUMS","browser_download_url":%q}]}`,
				tag, f.assetURL(f.srv.URL, "kanjo-linux-amd64"), f.assetURL(f.srv.URL, "kanjo-darwin-arm64"), f.assetURL(f.srv.URL, "SHA256SUMS"))
		}
		fmt.Fprint(w, body)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		name := strings.TrimPrefix(r.URL.Path, "/dl/")
		if code := f.status[r.URL.Path]; code != 0 {
			http.Error(w, "secret server detail", code)
			return
		}
		if f.redirect != "" {
			http.Redirect(w, r, f.redirect, http.StatusFound)
			return
		}
		switch name {
		case "SHA256SUMS":
			sums := f.sums
			if sums == "" {
				sums = sum(f.binary) + "  kanjo-linux-amd64\n" + sum(f.binary) + "  kanjo-darwin-arm64\n"
			}
			fmt.Fprint(w, sums)
		default:
			fmt.Fprint(w, f.binary)
		}
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelease) updater(t *testing.T) (Updater, string) {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "kanjo")
	if err := os.WriteFile(exe, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	return Updater{
		Client: f.srv.Client(), LatestURL: f.srv.URL + "/latest", AllowedHosts: []string{u.Host},
		Current: "v0.0.0-20260101000000-bbbbbbbbbbbb", GOOS: "linux", GOARCH: "amd64", Exe: exe,
	}, exe
}

func content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noTemp(t *testing.T, exe string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries after the call, want only the binary", len(entries))
	}
}

func TestUpdateReplacesBinary(t *testing.T) {
	f := newFake(t)
	u, exe := f.updater(t)
	got, updated, err := u.Update(context.Background())
	if err != nil || !updated || got != tag {
		t.Fatalf("Update = %q, %v, %v", got, updated, err)
	}
	if content(t, exe) != newBinary {
		t.Fatal("binary not replaced")
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
	}
	noTemp(t, exe)
}

func TestUpToDateDoesNothing(t *testing.T) {
	f := newFake(t)
	u, exe := f.updater(t)
	u.Current = tag
	got, updated, err := u.Update(context.Background())
	if err != nil || updated || got != tag {
		t.Fatalf("Update = %q, %v, %v", got, updated, err)
	}
	if content(t, exe) != oldBinary || f.hits.Load() != 1 {
		t.Fatalf("binary %q after %d requests; want it untouched after one", content(t, exe), f.hits.Load())
	}
}

func TestCheckOnlyDownloadsNothing(t *testing.T) {
	f := newFake(t)
	u, exe := f.updater(t)
	latest, available, err := u.Check(context.Background())
	if err != nil || !available || latest != tag {
		t.Fatalf("Check = %q, %v, %v", latest, available, err)
	}
	u.Current = tag
	if _, available, _ := u.Check(context.Background()); available {
		t.Fatal("same tag must not be an update")
	}
	if f.hits.Load() != 2 || content(t, exe) != oldBinary {
		t.Fatalf("%d requests; want 2 (API only)", f.hits.Load())
	}
}

func TestHashMismatchKeepsOldBinary(t *testing.T) {
	f := newFake(t)
	f.sums = sum("something else") + "  kanjo-linux-amd64\n"
	u, exe := f.updater(t)
	if _, _, err := u.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want a checksum error", err)
	}
	if content(t, exe) != oldBinary {
		t.Fatal("a download that fails its checksum must not replace the binary")
	}
	noTemp(t, exe)
}

func TestSumsFormats(t *testing.T) {
	for name, sums := range map[string]string{
		"two spaces":  sum(newBinary) + "  kanjo-linux-amd64\n",
		"binary mark": sum(newBinary) + " *kanjo-linux-amd64\n",
		"other lines": sum("x") + "  other\n\n" + sum(newBinary) + "  kanjo-linux-amd64\n",
	} {
		f := newFake(t)
		f.sums = sums
		u, exe := f.updater(t)
		if _, updated, err := u.Update(context.Background()); err != nil || !updated || content(t, exe) != newBinary {
			t.Errorf("%s: updated %v err %v", name, updated, err)
		}
	}
}

func TestBadChecksumFilesAreRefused(t *testing.T) {
	for name, sums := range map[string]string{
		"no line for the platform": sum(newBinary) + "  kanjo-darwin-arm64\n",
		"short hash":               "abcd  kanjo-linux-amd64\n",
		"non-hex hash":             strings.Repeat("z", 64) + "  kanjo-linux-amd64\n",
		"empty":                    "\n",
	} {
		f := newFake(t)
		f.sums = sums
		u, exe := f.updater(t)
		if _, _, err := u.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Errorf("%s: err = %v, want a checksum error", name, err)
		}
		if content(t, exe) != oldBinary {
			t.Errorf("%s: binary replaced", name)
		}
	}
	f := newFake(t)
	f.sums = "abcd  kanjo-linux-amd64\n"
	u, _ := f.updater(t)
	if _, _, err := u.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid checksum") {
		t.Errorf("a short hash must be called invalid: %v", err)
	}
}

func TestForeignHostsAreRefused(t *testing.T) {
	f := newFake(t)
	f.assetURL = func(_, name string) string { return "https://evil.example/" + name }
	u, exe := f.updater(t)
	if _, _, err := u.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("err = %v, want a refusal naming the host", err)
	}
	if content(t, exe) != oldBinary {
		t.Fatal("binary replaced")
	}
	u.LatestURL = "https://evil.example/latest"
	if _, _, err := u.Check(context.Background()); err == nil {
		t.Fatal("the release API URL is checked too")
	}
}

func TestRedirectToForeignHostIsRefused(t *testing.T) {
	f := newFake(t)
	f.redirect = "https://evil.example/payload"
	u, exe := f.updater(t)
	if _, _, err := u.Update(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if content(t, exe) != oldBinary {
		t.Fatal("binary replaced")
	}
}

func TestPlainHTTPAndUserinfoAreRefused(t *testing.T) {
	f := newFake(t)
	u, _ := f.updater(t)
	host := strings.TrimPrefix(f.srv.URL, "https://")
	for name, bad := range map[string]string{
		"http":     "http://" + host + "/dl/kanjo-linux-amd64",
		"userinfo": "https://user:pw@" + host + "/dl/kanjo-linux-amd64",
		"file":     "file:///etc/passwd",
		"no host":  "https:///x",
	} {
		if err := u.allowed(bad); err == nil {
			t.Errorf("%s: %s accepted", name, bad)
		}
	}
	if err := u.allowed(f.srv.URL + "/dl/x"); err != nil {
		t.Errorf("an https URL on an allowed host must pass: %v", err)
	}
}

func TestOversizedDownloadsAreRefused(t *testing.T) {
	f := newFake(t)
	u, exe := f.updater(t)
	u.MaxBinaryBytes = int64(len(newBinary)) - 1
	if _, _, err := u.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("binary: err = %v", err)
	}
	if content(t, exe) != oldBinary {
		t.Fatal("binary replaced")
	}
	noTemp(t, exe)
	u.MaxBinaryBytes = 0
	u.MaxSumsBytes = 10
	if _, _, err := u.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("checksum file: err = %v", err)
	}
	u.MaxSumsBytes = 0
	u.MaxAPIBytes = 10
	if _, _, err := u.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("API answer: err = %v", err)
	}
}

func TestExactLimitIsAccepted(t *testing.T) {
	f := newFake(t)
	u, exe := f.updater(t)
	u.MaxBinaryBytes = int64(len(newBinary))
	if _, updated, err := u.Update(context.Background()); err != nil || !updated || content(t, exe) != newBinary {
		t.Fatalf("a binary of exactly the limit must be accepted: %v", err)
	}
}

func TestMissingAssetForPlatform(t *testing.T) {
	f := newFake(t)
	u, _ := f.updater(t)
	u.GOOS, u.GOARCH = "plan9", "mips"
	if _, _, err := u.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "plan9/mips") {
		t.Fatalf("err = %v, want it to name the platform", err)
	}
}

func TestBadServerAnswers(t *testing.T) {
	cases := map[string]func(f *fakeRelease){
		"api 500":        func(f *fakeRelease) { f.status["/latest"] = 500 },
		"download 404":   func(f *fakeRelease) { f.status["/dl/kanjo-linux-amd64"] = 404 },
		"sums 404":       func(f *fakeRelease) { f.status["/dl/SHA256SUMS"] = 404 },
		"not json":       func(f *fakeRelease) { f.latest = "<html>" },
		"empty tag":      func(f *fakeRelease) { f.latest = `{"tag_name":"","assets":[]}` },
		"tag with space": func(f *fakeRelease) { f.latest = `{"tag_name":"v1 evil","assets":[]}` },
	}
	for name, tweak := range cases {
		f := newFake(t)
		tweak(f)
		u, exe := f.updater(t)
		_, _, err := u.Update(context.Background())
		if err == nil {
			t.Errorf("%s: expected an error", name)
		} else if strings.Contains(err.Error(), "secret server detail") {
			t.Errorf("%s: the server's text must not be echoed: %v", name, err)
		}
		if content(t, exe) != oldBinary {
			t.Errorf("%s: binary replaced", name)
		}
	}
}

func TestUnwritableDirectoryFailsCleanly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newFake(t)
	u, exe := f.updater(t)
	dir := filepath.Dir(exe)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if _, _, err := u.Update(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	os.Chmod(dir, 0o700)
	if content(t, exe) != oldBinary {
		t.Fatal("binary replaced")
	}
	noTemp(t, exe)
}

func TestUnknownExecutablePath(t *testing.T) {
	f := newFake(t)
	u, _ := f.updater(t)
	u.Exe = ""
	if _, _, err := u.Update(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCancelledContext(t *testing.T) {
	f := newFake(t)
	u, _ := f.updater(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := u.Check(ctx); err == nil {
		t.Fatal("expected an error")
	}
}

func TestEveryFailureReportsNotUpdated(t *testing.T) {
	cases := map[string]struct {
		tweak func(f *fakeRelease, u *Updater)
		want  string
	}{
		"api error":      {func(f *fakeRelease, _ *Updater) { f.status["/latest"] = 500 }, "HTTP 500"},
		"unknown exe":    {func(_ *fakeRelease, u *Updater) { u.Exe = "" }, "cannot tell"},
		"no platform":    {func(_ *fakeRelease, u *Updater) { u.GOOS = "plan9" }, "plan9/amd64"},
		"sums missing":   {func(f *fakeRelease, _ *Updater) { f.status["/dl/SHA256SUMS"] = 404 }, "HTTP 404"},
		"sums unusable":  {func(f *fakeRelease, _ *Updater) { f.sums = "\n" }, "no checksum for"},
		"binary missing": {func(f *fakeRelease, _ *Updater) { f.status["/dl/kanjo-linux-amd64"] = 404 }, "HTTP 404"},
	}
	for name, c := range cases {
		f := newFake(t)
		u, exe := f.updater(t)
		c.tweak(f, &u)
		_, updated, err := u.Update(context.Background())
		if err == nil || updated || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: updated %v err %v, want not updated and %q", name, updated, err, c.want)
		}
		if content(t, exe) != oldBinary {
			t.Errorf("%s: binary replaced", name)
		}
	}
}

func TestCheckValidatesTheTag(t *testing.T) {
	for tagValue, ok := range map[string]bool{"": false, "v1 evil": false, "-v1": false, "v0.0.0-20261103120000-abcdef012345": true, "1.2.3+build_4": true} {
		f := newFake(t)
		f.latest = fmt.Sprintf(`{"tag_name":%q,"assets":[]}`, tagValue)
		u, _ := f.updater(t)
		latest, _, err := u.Check(context.Background())
		if (err == nil) != ok || (ok && latest != tagValue) {
			t.Errorf("tag %q: latest %q err %v, want accepted=%v", tagValue, latest, err, ok)
		}
	}
	f := newFake(t)
	f.latest = `{"tag_name":"v1","assets":[]}`
	u, _ := f.updater(t)
	if _, _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	for limit, ok := range map[int64]bool{int64(len(f.latest)): true, int64(len(f.latest)) - 1: false} {
		u.MaxAPIBytes = limit
		if _, _, err := u.Check(context.Background()); (err == nil) != ok {
			t.Errorf("limit %d: err = %v, want accepted=%v", limit, err, ok)
		}
	}
}

func TestClientDefaults(t *testing.T) {
	if got := (Updater{}).client().Timeout; got != requestTimeout {
		t.Errorf("default timeout = %v, want %v", got, requestTimeout)
	}
	if got := (Updater{Client: &http.Client{Timeout: 5 * time.Second}}).client().Timeout; got != 5*time.Second {
		t.Errorf("a timeout set by the caller must be kept, got %v", got)
	}
}

func TestRedirectLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/hop/", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
		if n == 0 {
			fmt.Fprint(w, `{"tag_name":"v1","assets":[]}`)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", n-1), http.StatusFound)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	for hops, ok := range map[int]bool{4: true, 5: false} {
		u := Updater{Client: srv.Client(), LatestURL: fmt.Sprintf("%s/hop/%d", srv.URL, hops), AllowedHosts: []string{host}}
		if _, _, err := u.Check(context.Background()); (err == nil) != ok {
			t.Errorf("%d redirects: err = %v, want accepted=%v", hops, err, ok)
		}
	}
}

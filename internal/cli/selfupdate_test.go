package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/selfupdate"
)

const (
	releaseTag  = "v0.0.0-20261103120000-aaaaaaaaaaaa"
	runningTag  = "v0.0.0-20260101000000-bbbbbbbbbbbb"
	freshBinary = "fresh binary"
)

// updaterFor serves one release and returns an Updater and the path of the
// "running" binary it would replace.
func updaterFor(t *testing.T, status int) (selfupdate.Updater, string) {
	t.Helper()
	name := "kanjo-" + runtime.GOOS + "-" + runtime.GOARCH
	h := sha256.Sum256([]byte(freshBinary))
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			http.Error(w, "boom", status)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":%q},{"name":"SHA256SUMS","browser_download_url":%q}]}`,
			releaseTag, name, srv.URL+"/dl/"+releaseTag+"/bin", srv.URL+"/dl/"+releaseTag+"/sums")
	})
	mux.HandleFunc("/dl/"+releaseTag+"/bin", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, freshBinary) })
	mux.HandleFunc("/dl/"+releaseTag+"/sums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(h[:]), name)
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	exe := filepath.Join(t.TempDir(), "kanjo")
	if err := os.WriteFile(exe, []byte("running binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return selfupdate.Updater{
		Client: srv.Client(), LatestURL: srv.URL + "/latest", AllowedHosts: []string{strings.TrimPrefix(srv.URL, "https://")},
		Current: runningTag, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Exe: exe,
		AssetBase: srv.URL + "/dl/",
	}, exe
}

func runUpdate(t *testing.T, u selfupdate.Updater, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	e := env{home: t.TempDir(), cwd: t.TempDir(), clock: kanjotest.FixedClock{T: fixedNow}, update: u}
	code = run(append([]string{"self-update"}, args...), &out, &errb, e)
	return code, out.String(), errb.String()
}

func TestSelfUpdateInstalls(t *testing.T) {
	u, exe := updaterFor(t, 0)
	code, out, errb := runUpdate(t, u)
	if code != 0 || out != "updated "+runningTag+" -> "+releaseTag+"\n" {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errb)
	}
	if got, _ := os.ReadFile(exe); string(got) != freshBinary {
		t.Fatalf("binary = %q", got)
	}
}

func TestSelfUpdateCheckOnlyReports(t *testing.T) {
	u, exe := updaterFor(t, 0)
	code, out, _ := runUpdate(t, u, "--check")
	if code != 0 || out != "update available: "+runningTag+" -> "+releaseTag+"\n" {
		t.Fatalf("code %d stdout %q", code, out)
	}
	if got, _ := os.ReadFile(exe); string(got) != "running binary" {
		t.Fatal("--check must not install anything")
	}
}

func TestSelfUpdateWhenUpToDate(t *testing.T) {
	for _, args := range [][]string{nil, {"--check"}} {
		u, _ := updaterFor(t, 0)
		u.Current = releaseTag
		code, out, _ := runUpdate(t, u, args...)
		if code != 0 || out != "kanjo is up to date ("+releaseTag+")\n" {
			t.Errorf("%v: code %d stdout %q", args, code, out)
		}
	}
}

func TestSelfUpdateFailureExits1(t *testing.T) {
	for _, args := range [][]string{nil, {"--check"}} {
		u, _ := updaterFor(t, 500)
		code, out, errb := runUpdate(t, u, args...)
		if code != 1 || out != "" || !strings.Contains(errb, "HTTP 500") || strings.Contains(errb, "boom") {
			t.Errorf("%v: code %d stdout %q stderr %q", args, code, out, errb)
		}
	}
}

func TestSelfUpdateUsageErrorsExit2(t *testing.T) {
	u, _ := updaterFor(t, 0)
	for _, args := range [][]string{{"extra"}, {"--nope"}} {
		if code, _, errb := runUpdate(t, u, args...); code != 2 || errb == "" {
			t.Errorf("%v: code %d stderr %q", args, code, errb)
		}
	}
}

func TestProductionUpdaterOnlyTalksToGitHub(t *testing.T) {
	u := productionUpdater("/bin/kanjo")
	if u.AssetBase != "https://github.com/R0b-1ns/kanjo/releases/download/" {
		t.Errorf("AssetBase = %q: assets must be pinned to this repository's releases", u.AssetBase)
	}
	if u.Current != Version || u.Exe != "/bin/kanjo" || !strings.HasPrefix(u.LatestURL, "https://api.github.com/repos/R0b-1ns/kanjo/") {
		t.Fatalf("updater = %+v", u)
	}
	for _, h := range u.AllowedHosts {
		if h != "api.github.com" && h != "github.com" && !strings.HasSuffix(h, ".githubusercontent.com") {
			t.Errorf("unexpected allowed host %q", h)
		}
	}
}

func TestRunningBinaryIsTheResolvedExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := runningBinary(); got != want {
		t.Fatalf("runningBinary() = %q, want %q", got, want)
	}
}

func TestSelfUpdateNeverDowngrades(t *testing.T) {
	for _, args := range [][]string{nil, {"--check"}} {
		u, exe := updaterFor(t, 0)
		u.Current = "v0.0.0-20271231235959-ffffffffffff"
		code, out, _ := runUpdate(t, u, args...)
		if code != 0 || out != "kanjo is up to date ("+u.Current+")\n" {
			t.Errorf("%v: code %d stdout %q", args, code, out)
		}
		if got, _ := os.ReadFile(exe); string(got) != "running binary" {
			t.Errorf("%v: binary replaced", args)
		}
	}
}

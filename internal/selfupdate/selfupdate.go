// Package selfupdate replaces the running binary with the latest GitHub
// release after checking its SHA-256 against the release's SHA256SUMS.
//
// Every URL must be https, carry no credentials and name an allowed host,
// including after redirects; every body has a size cap; the binary is written
// to a temporary file next to the target and renamed over it only once the
// checksum matches. The checksum proves integrity, not origin: see
// docs/release.md for verifying the build attestation.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	defaultMaxAPI    = 1 << 20
	defaultMaxSums   = 64 << 10
	defaultMaxBinary = 200 << 20
	maxRedirects     = 5
	requestTimeout   = 2 * time.Minute
)

var (
	tagRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	// stampRe matches release tags: v0.0.0-<UTC yyyymmddhhmmss>-<12-char sha>.
	stampRe = regexp.MustCompile(`^v0\.0\.0-([0-9]{14})-[0-9a-f]{12}$`)
)

// isNewer reports whether release tag latest should replace the running
// current. Two release tags are ordered by their timestamp, so a build that is
// as recent as or newer than the latest release (or a deleted latest release)
// never causes a downgrade. Any other tag, such as "dev", counts as different.
func isNewer(latest, current string) bool {
	l, c := stampRe.FindStringSubmatch(latest), stampRe.FindStringSubmatch(current)
	if l == nil || c == nil {
		return latest != current
	}
	return l[1] > c[1]
}

// Updater looks for and installs the latest release.
type Updater struct {
	Client       *http.Client // nil = a default client; a copy is used
	LatestURL    string       // the GitHub "latest release" API URL
	AllowedHosts []string     // hosts (with port, if any) that may be contacted
	Current      string       // tag of the running binary
	GOOS, GOARCH string       // platform of the binary to fetch
	Exe          string       // path of the binary to replace
	// Size caps in bytes; 0 means the default.
	MaxAPIBytes, MaxSumsBytes, MaxBinaryBytes int64
}

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check reports the latest release tag and whether it is newer than Current.
func (u Updater) Check(ctx context.Context) (latest string, available bool, err error) {
	r, err := u.latest(ctx)
	return r.Tag, err == nil && isNewer(r.Tag, u.Current), err
}

// Update installs the latest release unless it is not newer than the running one.
func (u Updater) Update(ctx context.Context) (tag string, updated bool, err error) {
	r, err := u.latest(ctx)
	if err != nil || !isNewer(r.Tag, u.Current) {
		return r.Tag, false, err
	}
	if u.Exe == "" {
		return r.Tag, false, errors.New("cannot tell which binary to replace")
	}
	name := "kanjo-" + u.GOOS + "-" + u.GOARCH
	binURL, sumsURL := r.assetURL(name), r.assetURL("SHA256SUMS")
	if binURL == "" || sumsURL == "" {
		return r.Tag, false, fmt.Errorf("release %s has no %s binary or no SHA256SUMS", r.Tag, u.GOOS+"/"+u.GOARCH)
	}
	sums, err := u.readAll(ctx, sumsURL, orDefault(u.MaxSumsBytes, defaultMaxSums))
	if err != nil {
		return r.Tag, false, err
	}
	want, err := checksumFor(sums, name)
	if err != nil {
		return r.Tag, false, err
	}
	if err := u.install(ctx, binURL, want); err != nil {
		return r.Tag, false, err
	}
	return r.Tag, true, nil
}

func (r release) assetURL(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func (u Updater) latest(ctx context.Context) (release, error) {
	data, err := u.readAll(ctx, u.LatestURL, orDefault(u.MaxAPIBytes, defaultMaxAPI))
	if err != nil {
		return release{}, err
	}
	var r release
	if err := json.Unmarshal(data, &r); err != nil || !tagRe.MatchString(r.Tag) {
		return release{}, errors.New("the release answer is not valid")
	}
	return r, nil
}

func orDefault(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}

// allowed refuses anything but an https URL without credentials on an
// allowed host.
func (u Updater) allowed(raw string) error {
	p, err := url.Parse(raw)
	if err != nil || p.Scheme != "https" || p.User != nil || p.Host == "" {
		return errors.New("refusing to fetch: only https URLs without credentials are allowed")
	}
	if !slices.Contains(u.AllowedHosts, p.Host) {
		return fmt.Errorf("refusing to fetch from host %q: not an allowed release host", p.Host)
	}
	return nil
}

func (u Updater) client() *http.Client {
	c := http.Client{}
	if u.Client != nil {
		c = *u.Client
	}
	if c.Timeout == 0 {
		c.Timeout = requestTimeout
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		return u.allowed(req.URL.String())
	}
	return &c
}

// get starts a request; the caller closes the body. The server's text is
// never put in an error.
func (u Updater) get(ctx context.Context, raw string) (*http.Response, error) {
	if err := u.allowed(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kanjo")
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release data: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch release data: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func (u Updater) readAll(ctx context.Context, raw string, max int64) ([]byte, error) {
	resp, err := u.get(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err == nil && int64(len(data)) > max {
		err = fmt.Errorf("answer is larger than %d bytes", max)
	}
	return data, err
}

// checksumFor finds the SHA-256 of name in sha256sum output.
func checksumFor(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.TrimPrefix(f[1], "*") != name {
			continue
		}
		if b, err := hex.DecodeString(f[0]); err != nil || len(b) != sha256.Size {
			return "", fmt.Errorf("SHA256SUMS has an invalid checksum for %s", name)
		}
		return strings.ToLower(f[0]), nil
	}
	return "", fmt.Errorf("SHA256SUMS has no checksum for %s", name)
}

// install downloads the binary next to Exe, verifies it and renames it over
// Exe. On any failure the temporary file is removed and Exe is untouched.
func (u Updater) install(ctx context.Context, raw, wantSum string) (err error) {
	resp, err := u.get(ctx, raw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(u.Exe), ".kanjo-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", u.Exe, err)
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err = fill(tmp, resp.Body, wantSum, orDefault(u.MaxBinaryBytes, defaultMaxBinary)); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), u.Exe)
}

// fill copies body into f up to max bytes, checks the checksum, makes the file
// executable and flushes it to disk.
func fill(f *os.File, body io.Reader, wantSum string, max int64) error {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, max+1))
	switch {
	case err != nil:
		return fmt.Errorf("download: %w", err)
	case n > max:
		return fmt.Errorf("download is larger than %d bytes", max)
	case hex.EncodeToString(h.Sum(nil)) != wantSum:
		return errors.New("download does not match its checksum in SHA256SUMS")
	}
	return errors.Join(f.Chmod(0o755), f.Sync(), f.Close())
}

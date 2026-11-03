// Package web is the read-only report served on the loopback interface.
// Everything it shows comes from saved usage; it never writes, never reaches
// a file the binary does not embed, and answers only requests whose Host is
// the loopback address and port it listens on.
package web

import (
	"context"
	"embed"
	"fmt"
	"net/http"

	"github.com/R0b-1ns/kanjo/internal/core"
)

//go:embed assets
var assets embed.FS

// Loaded is the saved usage a request reports on.
type Loaded struct {
	Projects []core.ProjectUsage
	Failed   []string // bare names of projects that could not be read
}

// Loader reads the saved usage for a scope ("project" or "all") on every request.
type Loader func(ctx context.Context, scope string) (Loaded, error)

// PriceInfo is the price book plus what the page says about its origin.
type PriceInfo interface {
	core.PriceBook
	Source() string
	Fetched() string
}

const (
	maxQueryBytes = 1024
	csp           = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

type server struct {
	hosts map[string]bool
	load  Loader
	book  PriceInfo
}

// NewHandler serves the page and /api/data for a server listening on the
// given loopback port. Only the routes below exist; everything else is 404.
func NewHandler(port int, load Loader, book PriceInfo) http.Handler {
	return &server{
		hosts: map[string]bool{fmt.Sprintf("127.0.0.1:%d", port): true, fmt.Sprintf("localhost:%d", port): true},
		load:  load, book: book,
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	switch {
	case !s.hosts[r.Host]: // DNS rebinding: a foreign name pointing at 127.0.0.1
		http.Error(w, "forbidden", http.StatusForbidden)
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		h.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	case len(r.URL.RawQuery) > maxQueryBytes:
		http.Error(w, "query too long", http.StatusRequestURITooLong)
	default:
		s.route(w, r)
	}
}

var staticFiles = map[string]struct{ file, contentType string }{
	"/":        {"assets/index.html", "text/html; charset=utf-8"},
	"/app.js":  {"assets/app.js", "text/javascript; charset=utf-8"},
	"/app.css": {"assets/app.css", "text/css; charset=utf-8"},
}

func (s *server) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/data" {
		s.data(w, r)
		return
	}
	f, ok := staticFiles[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := assets.ReadFile(f.file)
	if err != nil { // the files are embedded: only a build mistake gets here
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	reply(w, r, http.StatusOK, f.contentType, body)
}

// reply writes body; a HEAD request gets the headers only.
func reply(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

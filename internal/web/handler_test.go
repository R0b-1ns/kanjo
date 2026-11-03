package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/web"
)

const port = 8787

type book struct{ kanjotest.TablePrices }

func (book) Source() string  { return "https://example.test/prices" }
func (book) Fetched() string { return "2026-11-03" }

func emptyLoader(context.Context, string) (web.Loaded, error) {
	return web.Loaded{Projects: []core.ProjectUsage{}, Failed: []string{}}, nil
}

func handler() http.Handler { return web.NewHandler(port, emptyLoader, book{}) }

func do(h http.Handler, method, target, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const goodHost = "127.0.0.1:8787"

func TestOnlyGetAndHead(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS", "TRACE"} {
		rec := do(handler(), m, "/api/data", goodHost)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: code %d allow %q", m, rec.Code, rec.Header().Get("Allow"))
		}
	}
}

func TestHostMustBeLoopbackWithThePort(t *testing.T) {
	for host, want := range map[string]int{
		"127.0.0.1:8787": 200, "localhost:8787": 200,
		"evil.example": 403, "evil.example:8787": 403, "127.0.0.1": 403, "LOCALHOST:8787": 403,
		"localhost.:8787": 403, "[::1]:8787": 403, "127.0.0.1:9999": 403, "": 403,
		"127.0.0.1:8787.evil.example": 403, "user@127.0.0.1:8787": 403,
	} {
		if rec := do(handler(), "GET", "/", host); rec.Code != want {
			t.Errorf("Host %q: code %d, want %d", host, rec.Code, want)
		}
	}
}

func TestSecurityHeadersOnEveryAnswer(t *testing.T) {
	const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	for _, c := range []struct{ method, target, host string }{
		{"GET", "/", goodHost}, {"GET", "/app.js", goodHost}, {"GET", "/app.css", goodHost}, {"GET", "/api/data", goodHost},
		{"GET", "/nope", goodHost}, {"GET", "/", "evil.example"}, {"POST", "/", goodHost},
	} {
		rec := do(handler(), c.method, c.target, c.host)
		h := rec.Header()
		if h.Get("Content-Security-Policy") != csp || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cross-Origin-Resource-Policy") != "same-origin" ||
			h.Get("Cache-Control") != "no-store" {
			t.Errorf("%v: headers = %v", c, h)
		}
		for k := range h {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%v: CORS header %s", c, k)
			}
		}
	}
}

func TestAssetsAreServedWithTheirTypes(t *testing.T) {
	for path, typ := range map[string]string{"/": "text/html; charset=utf-8", "/app.js": "text/javascript; charset=utf-8", "/app.css": "text/css; charset=utf-8"} {
		rec := do(handler(), "GET", path, goodHost)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != typ || rec.Body.Len() == 0 {
			t.Errorf("%s: code %d type %q body %d bytes", path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
}

func TestUnknownPathsAre404(t *testing.T) {
	for _, p := range []string{"/nope", "/etc/passwd", "/api/", "/api", "/app.js/", "/%2e%2e/", "/../", "//", "/assets/app.js", "/index.html", "/.git/config"} {
		if rec := do(handler(), "GET", p, goodHost); rec.Code != 404 {
			t.Errorf("%s: code %d, want 404", p, rec.Code)
		}
	}
}

func TestHeadHasNoBody(t *testing.T) {
	get := do(handler(), "GET", "/", goodHost)
	head := do(handler(), "HEAD", "/", goodHost)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Type") != get.Header().Get("Content-Type") {
		t.Fatalf("HEAD: code %d, %d body bytes", head.Code, head.Body.Len())
	}
}

func TestLongQueryIs414(t *testing.T) {
	long := "/api/data?task=" + strings.Repeat("a", 2000)
	if rec := do(handler(), "GET", long, goodHost); rec.Code != http.StatusRequestURITooLong {
		t.Fatalf("code %d", rec.Code)
	}
	ok := "/?x=" + strings.Repeat("a", 1000)
	if rec := do(handler(), "GET", ok, goodHost); rec.Code != 200 {
		t.Fatalf("a 1004 byte query must pass, code %d", rec.Code)
	}
}

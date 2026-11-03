package web_test

import (
	"regexp"
	"strings"
	"testing"
)

func asset(t *testing.T, path string) string {
	t.Helper()
	rec := do(handler(), "GET", path, goodHost)
	if rec.Code != 200 {
		t.Fatalf("%s: code %d", path, rec.Code)
	}
	return rec.Body.String()
}

func TestPageHasNoExternalReferences(t *testing.T) {
	for _, p := range []string{"/", "/app.js", "/app.css"} {
		// the SVG namespace is an identifier, not a network reference
		body := strings.ReplaceAll(asset(t, p), "http://www.w3.org/2000/svg", "")
		for _, bad := range []string{"http://", "https://", "//cdn", "@import", "url(", "<link rel=\"preconnect"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q", p, bad)
			}
		}
	}
}

func TestPageNeverBuildsMarkupFromData(t *testing.T) {
	js := asset(t, "/app.js")
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "setTimeout(\"", "javascript:"} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js contains %q", bad)
		}
	}
}

func TestPageHasNoInlineScriptOrStyle(t *testing.T) {
	html := asset(t, "/")
	if strings.Count(html, "<script") != 1 || !strings.Contains(html, `<script src="/app.js"`) {
		t.Error("index.html must load exactly one script, /app.js")
	}
	if strings.Contains(html, "<style") || regexp.MustCompile(`\sstyle=`).MatchString(html) || regexp.MustCompile(`\son[a-z]+=`).MatchString(html) {
		t.Error("index.html has inline style or an event handler attribute (the CSP forbids them)")
	}
	if js := asset(t, "/app.js"); strings.Contains(js, ".style.") || strings.Contains(js, "setAttribute(\"style\"") {
		t.Error("app.js sets inline styles (the CSP forbids them): use classes and SVG attributes")
	}
}

func TestPageShowsTheEstimateNoticeFromTheServer(t *testing.T) {
	if js := asset(t, "/app.js"); !strings.Contains(js, "data.notice") {
		t.Error("app.js must display the notice /api/data sends")
	}
	if !strings.Contains(asset(t, "/"), `id="notice"`) {
		t.Error("index.html needs the #notice element")
	}
}

func TestPageSupportsDarkAndMobile(t *testing.T) {
	if !strings.Contains(asset(t, "/app.css"), "prefers-color-scheme: dark") {
		t.Error("app.css has no dark theme")
	}
	if !strings.Contains(asset(t, "/"), `name="viewport"`) {
		t.Error("index.html has no viewport meta")
	}
}

func TestEveryElementTheScriptLooksUpExists(t *testing.T) {
	html, js := asset(t, "/"), asset(t, "/app.js")
	ids := regexp.MustCompile(`byId\("([a-z-]+)"\)`).FindAllStringSubmatch(js, -1)
	if len(ids) < 8 {
		t.Fatalf("expected the script to use byId for its elements, found %d", len(ids))
	}
	for _, m := range ids {
		if !strings.Contains(html, `id="`+m[1]+`"`) {
			t.Errorf("app.js looks up #%s but index.html has no such id", m[1])
		}
	}
}

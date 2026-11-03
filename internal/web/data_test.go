package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/web"
)

func prices() book {
	return book{kanjotest.TablePrices{Prices: map[string]core.Price{"m": {Input: 4, Output: 20}}}}
}

func urow(session, day, task, model string, msgs int64, tok core.Tokens) core.UsageRow {
	return core.UsageRow{Key: core.Key{Source: "claude", Session: session, Day: day, Task: task, Model: model}, Messages: msgs, Tokens: tok}
}

// fixture: app-a costs 4 (1M input at 4), app-b costs 10 (0.5M output at 20)
// plus 2 unpriced messages.
func fixture() []core.ProjectUsage {
	return []core.ProjectUsage{
		{Name: "app-a", Rows: []core.UsageRow{urow("a1", "2026-11-02", "ABC-1", "m", 3, core.Tokens{Input: 1_000_000})}},
		{Name: "app-b", Rows: []core.UsageRow{
			urow("b1", "2026-11-03", "XYZ-3", "m", 2, core.Tokens{Output: 500_000}),
			urow("b2", "2026-11-03", "XYZ-3", "unknown", 2, core.Tokens{Output: 7}),
		}},
	}
}

// loaderOf serves all of ps for scope=all and the first project for scope=project.
func loaderOf(ps []core.ProjectUsage, failed ...string) web.Loader {
	return func(_ context.Context, scope string) (web.Loaded, error) {
		if scope == "all" {
			return web.Loaded{Projects: ps, Failed: failed}, nil
		}
		return web.Loaded{Projects: ps[:1], Failed: []string{}}, nil
	}
}

func get(t *testing.T, l web.Loader, target string) (int, []byte) {
	t.Helper()
	rec := do(web.NewHandler(port, l, prices()), "GET", target, goodHost)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("%s: content type %q", target, ct)
	}
	return rec.Code, rec.Body.Bytes()
}

func decode(t *testing.T, body []byte) web.Data {
	t.Helper()
	var d web.Data
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, body)
	}
	return d
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func TestDataTotalsMatchTheAggregate(t *testing.T) {
	code, body := get(t, loaderOf(fixture()), "/api/data?scope=all")
	d := decode(t, body)
	if code != 200 || d.Scope != "all" || !near(d.TotalUSD, 14) || !near(d.USDBySource["claude"], 14) || d.UnpricedMessages != 2 {
		t.Fatalf("code %d data %+v", code, d)
	}
	if d.Notice != core.EstimateNotice || d.Prices.Source != "https://example.test/prices" || d.Prices.Fetched != "2026-11-03" {
		t.Fatalf("notice/prices = %q %+v", d.Notice, d.Prices)
	}
	if len(d.Days) != 2 || d.Days[0].Name != "2026-11-02" || !near(*d.Days[0].USD, 4) || !near(*d.Days[1].USD, 10) {
		t.Fatalf("days = %+v", d.Days)
	}
	if len(d.Tasks) != 2 || d.Tasks[0].Name != "ABC-1" || d.Tasks[0].Messages != 3 || d.Tasks[0].Input != 1_000_000 {
		t.Fatalf("tasks = %+v", d.Tasks)
	}
}

func TestUnpricedModelIsNullAndDoesNotChangeTheTotal(t *testing.T) {
	_, body := get(t, loaderOf(fixture()), "/api/data?scope=all")
	d := decode(t, body)
	if len(d.Models) != 2 || d.Models[0].Name != "m" || d.Models[1].Name != "unknown" {
		t.Fatalf("models = %+v", d.Models)
	}
	if d.Models[1].USD != nil || d.Models[1].UnpricedMessages != 2 || d.Models[1].Output != 7 || !near(d.TotalUSD, 14) {
		t.Fatalf("unknown model = %+v total %v", d.Models[1], d.TotalUSD)
	}
	if !strings.Contains(string(body), `"usd":null`) {
		t.Fatalf("an unpriced group must serialise usd as null, never 0:\n%s", body)
	}
}

func TestTaskAndSinceFilters(t *testing.T) {
	_, body := get(t, loaderOf(fixture()), "/api/data?scope=all&task=XYZ-3&since=2026-11-03")
	d := decode(t, body)
	if !near(d.TotalUSD, 10) || len(d.Tasks) != 1 || d.Tasks[0].Name != "XYZ-3" || len(d.Days) != 1 {
		t.Fatalf("data = %+v", d)
	}
	_, body = get(t, loaderOf(fixture()), "/api/data?scope=all&since=2030-01-01")
	if d := decode(t, body); d.TotalUSD != 0 || len(d.Models) != 0 || len(d.FilterTasks) != 2 {
		t.Fatalf("a since after every day keeps the filter list: %+v", d)
	}
}

func TestFilterTasksIgnoreTheTaskFilter(t *testing.T) {
	_, body := get(t, loaderOf(fixture()), "/api/data?scope=all&task=ABC-1")
	d := decode(t, body)
	if len(d.FilterTasks) != 2 || d.FilterTasks[0] != "ABC-1" || d.FilterTasks[1] != "XYZ-3" || len(d.Tasks) != 1 {
		t.Fatalf("filter tasks = %v, tasks = %+v", d.FilterTasks, d.Tasks)
	}
}

func TestScopeAllListsProjectsAndFailures(t *testing.T) {
	_, body := get(t, loaderOf(fixture(), "app-c"), "/api/data?scope=all")
	d := decode(t, body)
	if len(d.Projects) != 2 || d.Projects[0].Name != "app-a" || !near(*d.Projects[0].USD, 4) || d.Projects[1].Messages != 4 || len(d.FailedProjects) != 1 || d.FailedProjects[0] != "app-c" {
		t.Fatalf("projects = %+v failed %v", d.Projects, d.FailedProjects)
	}
	_, body = get(t, loaderOf(fixture()), "/api/data") // default scope is project
	if d := decode(t, body); d.Scope != "project" || len(d.Projects) != 0 || !near(d.TotalUSD, 4) {
		t.Fatalf("project scope = %+v", d)
	}
}

func TestEmptyDataIsValidJSONWithEmptyLists(t *testing.T) {
	l := func(context.Context, string) (web.Loaded, error) { return web.Loaded{}, nil }
	for _, scope := range []string{"project", "all"} {
		code, body := get(t, l, "/api/data?scope="+scope)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil || code != 200 {
			t.Fatalf("code %d err %v\n%s", code, err, body)
		}
		for _, k := range []string{"models", "tasks", "days", "filter_tasks", "multi_task_sessions", "projects", "failed_projects"} {
			if list, ok := m[k].([]any); !ok || len(list) != 0 {
				t.Errorf("%s: %v (%T), want an empty list", k, m[k], m[k])
			}
		}
		if by, ok := m["usd_by_source"].(map[string]any); !ok || len(by) != 0 {
			t.Errorf("usd_by_source = %v", m["usd_by_source"])
		}
	}
}

func TestBadParametersAre400(t *testing.T) {
	for _, q := range []string{"scope=x", "task=../x", "task=%00", "since=2026-13-40", "since=a&since=b", "scope=all&scope=project", "foo=1", "task=%zz", "scope="} {
		code, body := get(t, loaderOf(fixture()), "/api/data?"+q)
		if code != http.StatusBadRequest || string(body) != "{\"error\":\"bad request\"}\n" {
			t.Errorf("%s: code %d body %q", q, code, body)
		}
	}
}

func TestLoaderErrorHidesTheDetail(t *testing.T) {
	l := func(context.Context, string) (web.Loaded, error) {
		return web.Loaded{}, errors.New("open /Users/someone/secret/dir/kanjo.db: permission denied")
	}
	code, body := get(t, l, "/api/data")
	if code != 500 || string(body) != "{\"error\":\"cannot read data\"}\n" {
		t.Fatalf("code %d body %q", code, body)
	}
}

func TestHostileValuesStayData(t *testing.T) {
	hostile := "<script>alert(1)</script>"
	noSlash := "<img src=x onerror=alert(1)>" // a task label is cut at its first "/"
	ps := []core.ProjectUsage{{Name: hostile, Rows: []core.UsageRow{urow(hostile, "2026-11-03", noSlash, hostile, 1, core.Tokens{Output: 1})}}}
	_, body := get(t, loaderOf(ps), "/api/data?scope=all")
	if strings.Contains(string(body), "<script>") || strings.Contains(string(body), "<img") {
		t.Fatalf("raw markup in the JSON:\n%s", body)
	}
	d := decode(t, body)
	if d.Models[0].Name != hostile || d.Tasks[0].Name != noSlash || d.Projects[0].Name != hostile {
		t.Fatalf("values must round-trip unchanged: %+v", d)
	}
}

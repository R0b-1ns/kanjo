package web_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/web"
)

type rendered struct {
	Requests                                                                     []string
	Total, Unpriced, Notice, Status, Prices                                      string
	ModelNames, ModelValues, TaskNames, ProjectNames, Failed, Multi, TaskOptions []string
	ProjectsHidden                                                               bool
	TokenRows                                                                    int
}

// runScript executes the real /app.js in node against a fake DOM, with the
// JSON /api/data would send for the given loader and scope.
func runScript(t *testing.T, l web.Loader, scope string) rendered {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed: the page script is not executed")
	}
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	_, data := get(t, l, "/api/data?scope="+scope)
	cmd := exec.Command(node, "testdata/dom.js", write("app.js", asset(t, "/app.js")), write("data.json", string(data)))
	cmd.Env = append(os.Environ(), "SCOPE="+scope)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var r rendered
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return r
}

func TestScriptRendersTheReport(t *testing.T) {
	r := runScript(t, loaderOf(fixture()), "all")
	if r.Total != "$14.0000" || r.Notice != core.EstimateNotice || r.Prices != "Prices: https://example.test/prices · retrieved 2026-11-03" || r.Status != "" {
		t.Fatalf("rendered = %+v", r)
	}
	if r.Unpriced != "2 messages have no price and are not in the total." {
		t.Fatalf("unpriced = %q", r.Unpriced)
	}
	if len(r.ModelNames) != 2 || r.ModelNames[1] != "unknown" || r.ModelValues[0] != "$14.0000" || r.ModelValues[1] != "no price" {
		t.Fatalf("an unpriced model must read \"no price\", never $0: %v %v", r.ModelNames, r.ModelValues)
	}
	if r.ProjectsHidden || len(r.ProjectNames) != 2 || r.TokenRows != 3 || len(r.TaskOptions) != 3 {
		t.Fatalf("rendered = %+v", r)
	}
	if len(r.Requests) != 1 || r.Requests[0] != "/api/data?scope=all" {
		t.Fatalf("requests = %v", r.Requests)
	}
}

func TestScriptShowsTheProjectScopeWithoutTheProjectsCard(t *testing.T) {
	if r := runScript(t, loaderOf(fixture()), "project"); !r.ProjectsHidden || len(r.ProjectNames) != 0 {
		t.Fatalf("rendered = %+v", r)
	}
}

func TestScriptKeepsHostileValuesAsText(t *testing.T) {
	hostile := "<img src=x onerror=alert(1)>"
	ps := []core.ProjectUsage{{Name: hostile, Rows: []core.UsageRow{urow("s", "2026-11-03", hostile, "m", 1, core.Tokens{Output: 1})}}}
	r := runScript(t, loaderOf(ps, hostile), "all")
	if r.ModelNames[0] != "m" || r.TaskNames[0] != hostile || r.ProjectNames[0] != hostile || r.Failed[0] != "Project "+hostile+" could not be read." {
		t.Fatalf("hostile values must come out as plain text: %+v", r)
	}
}

func TestScriptExplainsAnEmptySelection(t *testing.T) {
	r := runScript(t, func(_ context.Context, _ string) (web.Loaded, error) { return web.Loaded{}, nil }, "project")
	if r.Total != "$0.0000" || r.Status != "No usage for this selection. Run kanjo snapshot to save usage." || len(r.ModelNames) != 0 {
		t.Fatalf("rendered = %+v", r)
	}
}

func TestScriptWarnsAboutMultiTaskSessions(t *testing.T) {
	ps := fixture()
	ps[0].Sessions = []core.SessionInfo{{Source: "claude", Session: "a1", TaskSource: "heuristic", LabelsSeen: 2}}
	r := runScript(t, loaderOf(ps), "project")
	if len(r.Multi) != 1 || r.Multi[0] != "Session a1 mentions several tasks; it is counted on one task." {
		t.Fatalf("multi = %v", r.Multi)
	}
}

package core

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// ReportSchemaVersion changes only when a field of report.json changes meaning.
const ReportSchemaVersion = 1

// RunReport is report.json (docs/delegation.md). It holds counts, codes, ids and
// repository-relative paths; never a command, a prompt, an answer or code.
type RunReport struct {
	SchemaVersion int          `json:"schema_version"`
	Session       string       `json:"session"`
	Task          string       `json:"task"`
	Adapter       string       `json:"adapter"`
	Model         string       `json:"model"`
	Verdict       RunVerdict   `json:"verdict"`
	Reasons       []Reason     `json:"reasons"`
	ExitCode      int          `json:"exit_code"`
	StartedAt     string       `json:"started_at"`
	DurationMS    int64        `json:"duration_ms"`
	Launches      int          `json:"launches"`
	Retries       int          `json:"retries"`
	TimedOut      bool         `json:"timed_out"`
	Tokens        ReportTokens `json:"tokens"`
	Files         ReportFiles  `json:"files"`
	Steps         []ReportStep `json:"steps"`
	Base          string       `json:"base"`
	Branch        string       `json:"branch"`
	Patch         string       `json:"patch"` // file name in the run folder, empty when none was written
}

// ReportTokens is what the agent consumed. Status is "available" or "unavailable".
type ReportTokens struct {
	Status  string        `json:"status"`
	ByModel []ReportModel `json:"by_model"`
}

// ReportModel is the consumption of one model. CostUSD is null when unknown.
type ReportModel struct {
	Model        string   `json:"model"`
	Input        int64    `json:"input"`
	Output       int64    `json:"output"`
	Reasoning    int64    `json:"reasoning"`
	CacheWrite5m int64    `json:"cache_write_5m"`
	CacheWrite1h int64    `json:"cache_write_1h"`
	CacheRead    int64    `json:"cache_read"`
	CostUSD      *float64 `json:"cost_usd"`
}

// ReportFiles lists repository-relative paths.
type ReportFiles struct {
	Expected         []string `json:"expected"`
	Missing          []string `json:"missing"`
	ForbiddenTouched []string `json:"forbidden_touched"`
	Unexpected       []string `json:"unexpected"`
	UnsafeSymlinks   []string `json:"unsafe_symlinks"`
}

// ReportStep is one command, named (setup, agent, test, check-1), never quoted.
type ReportStep struct {
	Name       string `json:"name"`
	Exit       int    `json:"exit"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

// usageSum is the consumption of one model over the launches of a run.
type usageSum struct {
	tokens  Tokens
	cost    float64
	unknown bool // some launch gave no cost
}

// addUsage folds one launch's usage into the run's, per model.
func (g *delegation) addUsage(us []ModelUsage) {
	for _, u := range us {
		if g.usage == nil {
			g.usage = map[string]*usageSum{}
		}
		s := g.usage[u.Model]
		if s == nil {
			s = &usageSum{}
			g.usage[u.Model] = s
		}
		s.tokens = s.tokens.Add(u.Tokens)
		if u.CostUSD == nil {
			s.unknown = true
		} else {
			s.cost += *u.CostUSD
		}
	}
}

// models lists the consumed models, sorted.
func (g *delegation) models() []string {
	names := make([]string, 0, len(g.usage))
	for m := range g.usage {
		names = append(names, m)
	}
	slices.Sort(names)
	return names
}

func (s *usageSum) costPtr() *float64 {
	if s.unknown {
		return nil
	}
	c := s.cost
	return &c
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// report builds report.json from the run as it ended.
func (g *delegation) report(res RunResult) RunReport {
	r := RunReport{
		SchemaVersion: ReportSchemaVersion, Session: g.run.Session, Task: g.run.Task, Adapter: g.run.Adapter, Model: g.run.Model,
		Verdict: res.Verdict, Reasons: orEmpty(g.reasonList()), ExitCode: res.Exit,
		StartedAt: g.run.StartedAt.UTC().Format(MarkerTimeLayout), DurationMS: res.Duration.Milliseconds(),
		Launches: res.Launches, Retries: res.Retries, TimedOut: slices.Contains(g.reasons, ReasonAgentTimeout),
		Tokens: ReportTokens{Status: "unavailable", ByModel: []ReportModel{}},
		Files: ReportFiles{
			Expected: orEmpty(g.req.Expect), Missing: orEmpty(g.findings.Missing), ForbiddenTouched: orEmpty(g.findings.ForbiddenTouched),
			Unexpected: orEmpty(g.findings.Unexpected), UnsafeSymlinks: orEmpty(g.findings.UnsafeSymlinks),
		},
		Steps: []ReportStep{}, Base: g.run.Base, Branch: BranchOf(g.run.Session),
	}
	if g.patchKept {
		r.Patch = string(ArtPatch)
	}
	for _, s := range g.steps {
		r.Steps = append(r.Steps, ReportStep{Name: s.Name, Exit: s.Exit, DurationMS: s.Duration.Milliseconds(), TimedOut: s.TimedOut})
	}
	for _, m := range g.models() {
		u := g.usage[m]
		r.Tokens.Status = "available"
		r.Tokens.ByModel = append(r.Tokens.ByModel, ReportModel{
			Model: m, Input: u.tokens.Input, Output: u.tokens.Output, Reasoning: u.tokens.Reasoning,
			CacheWrite5m: u.tokens.CacheWrite5m, CacheWrite1h: u.tokens.CacheWrite1h, CacheRead: u.tokens.CacheRead, CostUSD: u.costPtr(),
		})
	}
	return r
}

// reasonList is the reasons without repeats, in the order they arose.
func (g *delegation) reasonList() []Reason {
	var out []Reason
	for _, r := range g.reasons {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// QuoteShell quotes s as one word for a POSIX shell.
func QuoteShell(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Text is report.txt: the same facts as the JSON, one per line, ending with
// the commands to apply the patch and to record the verdict. dataDir is the
// absolute data folder to put before the run folder paths; empty gives the
// paths relative to the data folder, as report.txt itself has them.
func (r RunReport) Text(dataDir string) string {
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line("task: %s", r.Task)
	line("session: %s", r.Session)
	line("verdict: %s", r.Verdict)
	line("reasons: %s", joinOrNone(r.Reasons))
	line("exit: %d", r.ExitCode)
	line("model: %s %s", r.Adapter, r.Model)
	line("duration: %s", time.Duration(r.DurationMS)*time.Millisecond)
	line("launches: %d (retries %d)", r.Launches, r.Retries)
	line("tokens: %s", r.Tokens.Status)
	for _, m := range r.Tokens.ByModel {
		line("  %s: input %d, output %d, reasoning %d, cache write 5m %d, cache write 1h %d, cache read %d, cost %s",
			m.Model, m.Input, m.Output, m.Reasoning, m.CacheWrite5m, m.CacheWrite1h, m.CacheRead, costText(m.CostUSD))
	}
	line("files: expected %d, missing %d, forbidden touched %d, unexpected %d, unsafe links %d",
		len(r.Files.Expected), len(r.Files.Missing), len(r.Files.ForbiddenTouched), len(r.Files.Unexpected), len(r.Files.UnsafeSymlinks))
	for _, l := range []struct {
		name  string
		paths []string
	}{{"missing", r.Files.Missing}, {"forbidden", r.Files.ForbiddenTouched}, {"unexpected", r.Files.Unexpected}, {"unsafe link", r.Files.UnsafeSymlinks}} {
		for _, p := range l.paths {
			line("  %s: %s", l.name, p)
		}
	}
	for _, s := range r.Steps {
		line("step %s: exit %d, %s%s", s.Name, s.Exit, time.Duration(s.DurationMS)*time.Millisecond, map[bool]string{true: ", timed out"}[s.TimedOut])
	}
	line("base: %s", r.Base)
	line("branch: %s", r.Branch)
	if r.Patch != "" {
		line("patch: %s", r.runPath(dataDir, r.Patch))
		line("apply: git apply %s", r.runPath(dataDir, r.Patch))
	}
	line("finish: kanjo finish %s --verdict accepted|modified|rejected", r.Session)
	line("note: %s %s %s %s", r.Task, r.Session, r.Verdict, time.Duration(r.DurationMS)*time.Millisecond)
	return b.String()
}

// runPath is the path of a file of the run folder, quoted when absolute.
func (r RunReport) runPath(dataDir, name string) string {
	rel := "runs/" + r.Session + "/" + name
	if dataDir == "" {
		return rel
	}
	return QuoteShell(strings.TrimRight(dataDir, "/") + "/" + rel)
}

func joinOrNone(rs []Reason) string {
	if len(rs) == 0 {
		return "none"
	}
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = string(r)
	}
	return strings.Join(parts, ", ")
}

func costText(c *float64) string {
	if c == nil {
		return "unknown"
	}
	return fmt.Sprintf("$%.4f", *c)
}

# Foundations and Claude usage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go CLI `kanjo` that snapshots Claude Code transcript usage into a local SQLite store, reports tokens and estimated cost by task/model/day/session, updates itself from GitHub releases, and ships a release on every merge.

**Architecture:** `internal/core` holds the data model, ports (`UsageSource`, `Store`, `PriceBook`, `Clock`), cost, snapshot and aggregation, with no adapter import and no system call. Adapters live in their own packages and are wired in `internal/cli`. `internal/kanjotest` holds fakes and contract suites reused by every adapter test.

**Tech Stack:** Go 1.27.3, `modernc.org/sqlite`, `github.com/BurntSushi/toml`, stdlib for everything else.

**Spec:** `docs/superpowers/specs/2026-11-03-foundations-claude-usage-design.md` (slice design) and `docs/spec.md` (product spec).

## Global Constraints

- Module path `github.com/R0b-1ns/kanjo`; binary `kanjo`; `CGO_ENABLED=0` builds for darwin/linux × amd64/arm64.
- Everything committed is public: English only; no mention of other projects, personal paths or private details. Fixtures use invented ids and paths (`/home/dev/demo`).
- No prompt, response, code or other transcript text in the store, logs, errors or output. Only counts, model names, ids, labels and dates.
- `internal/core` imports no adapter package, no `os`, no `net`, and never calls `time.Now`.
- Untrusted inputs (transcripts, prices file, config, HTTP responses) are validated at the boundary; errors are returned, never swallowed.
- Tests never call a real model, the real `~/.claude`, or the network (httptest only).
- Exit codes: 0 ok, 1 error, 2 usage error.
- Day = UTC date of the message `timestamp`.
- Cost formula: `(input×p_input + output×p_output + cache_write_5m×p_w5 + cache_write_1h×p_w1 + cache_read×p_read) / 1_000_000`. Unpriced → empty cost, counted in `unpriced_messages`, never 0.
- Every report states: "Claude cost is an estimate at API list prices, not a subscription bill."
- Conventional commits, one logical change each, build never broken. No push, merge, PR or publish without the user.
- After each code task: `/ub-mode` pass (`crapper --changed`, `dryer <path>`, `mutator --changed`): CRAP < 10 per function, similarity < 0.8, no surviving mutant, no uncovered or dead code.

## Review Focus

1. No `~/.claude/projects` or no dir matching the prefixes → `snapshot` reports 0 sessions and exits 0; `usage` on an empty store says "no data; run kanjo snapshot" and exits 0. (Task 8)
2. A transcript being written while read ends with a truncated line → that line is skipped and counted, the rest of the session is kept. (Task 5)
3. An assistant line with a missing or unparseable `timestamp` → skipped and counted, not filed under an empty day. (Task 5)
4. The same session id under two matching project dirs (workspace root and a sub-repo prefix) → one session, message ids deduped across both. (Task 5)
5. Data dir cannot be created or written → clear error naming the path, exit 1, no partial DB. (Task 4)

---

### Task 0: Repository setup

**Files:**
- Create: `.gitignore`, `LICENSE`, `README.md`, `CLAUDE.md`, `docs/spec.md`, `go.mod`, `cmd/kanjo/main.go`, `internal/cli/cli.go`, `scripts/build.sh`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`

**Interfaces:**
- Produces: `cli.Version string` (ldflags `-X github.com/R0b-1ns/kanjo/internal/cli.Version=<tag>`, default `dev`); `cli.Run(args []string, stdout, stderr io.Writer) int`.

- [ ] **Step 1:** `git init -b main`; `cairn init --name kanjo --repos kanjo --path "$PWD"`; `cairn context`; `cairn guide`; `cairn recent`; `cairn list --type open`; `cairn list --type helper`.
- [ ] **Step 2:** `.gitignore`: `/dist/`, `/kanjo`, `*.test`, `.DS_Store`, `.metrics/`, `target/`, `.ai-usage/`, `.cairn/`.
- [ ] **Step 3:** `LICENSE` MIT, copyright `2026 Rob1s`.
- [ ] **Step 4:** `docs/spec.md`: full English translation of the product spec; drop references to other projects and the prototype (say "a throwaway prototype" without paths; "memory note" instead of a tool name).
- [ ] **Step 5:** `CLAUDE.md`: purpose; commands (`go test -race ./...`, `go vet ./...`, `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`, `scripts/build.sh`, crapper/dryer/mutator); publication rules; condensed method (brainstorm → plan → TDD → verify → review order; security first; core knows no tool; fakes + contract tests; config not hard-coded); one cairn note per slice via `cairn add`, `cairn amend` only.
- [ ] **Step 6:** `README.md`: one paragraph, install via release download or `kanjo self-update`, `snapshot`/`usage` examples, link to `docs/`.
- [ ] **Step 7:** `go mod init github.com/R0b-1ns/kanjo` (go 1.27.3); `main.go` calls `os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))`; `cli.Run` supports `version` only (prints `kanjo <Version>`), unknown command → usage on stderr, exit 2. Test `TestRunVersion`, `TestRunUnknownCommandExits2`.
- [ ] **Step 8:** `scripts/build.sh [version]`: POSIX sh, `set -eu`, `rm -rf dist`, `-trimpath -ldflags "-s -w -buildid= -X .../cli.Version=$version"`, `dist/kanjo-<os>-<arch>`, `SHA256SUMS` via `sha256sum` or `shasum -a 256`.
- [ ] **Step 9:** `ci.yml`: on `pull_request` and push to `main`, `permissions: contents: read`, checkout (`persist-credentials: false`) + setup-go pinned by sha (checkout `3d3c42e5aac5ba805825da76410c181273ba90b1` # v7.0.1, setup-go `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` # v7.0.0; upload/download-artifact pinned the same way), then `go mod verify`, `go vet ./...`, `go test -race -count=1 ./...`, `go run golang.org/x/vuln/cmd/govulncheck@<pinned version> ./...`, `scripts/build.sh ci`.
- [ ] **Step 10:** `release.yml`: on push to `main`, `permissions: {}`, `concurrency: {group: release, cancel-in-progress: false}`. Job `build` (contents: read): same checks as CI, compute `TAG=v0.0.0-$(TZ=UTC git show -s --format=%cd --date=format-local:%Y%m%d%H%M%S HEAD)-$(git rev-parse --short=12 HEAD)`, `scripts/build.sh "$TAG"`, upload `dist/`. Job `publish` (contents: write, id-token: write, attestations: write): download, `sha256sum -c SHA256SUMS`, `actions/attest-build-provenance` on `dist/kanjo-*` (pin by sha, look it up with `gh api repos/actions/attest-build-provenance/git/ref/tags/<latest>`), `gh release create "$TAG" dist/kanjo-* dist/SHA256SUMS --target "$GITHUB_SHA" --title "$TAG" --notes "Commit $GITHUB_SHA"`.
- [ ] **Step 11:** Verify: `go test ./... && go vet ./... && scripts/build.sh test && ls dist` shows 4 binaries + SHA256SUMS. Validate workflow YAML parses (`ruby -ryaml -e 'YAML.load_file(ARGV[0])' <file>`).
- [ ] **Step 12:** Commits: `chore: initialize repository`, `docs: add product spec`, `ci: add checks and release on merge`.

### Task 1: Core model and validation

**Files:**
- Create: `internal/core/model.go`, `internal/core/model_test.go`

**Interfaces:**
- Produces:
```go
type Tokens struct{ Input, Output, Reasoning, CacheWrite5m, CacheWrite1h, CacheRead int64 }
func (t Tokens) Add(o Tokens) Tokens
type Key struct{ Source, Session, Day, Task, Model string; LongContext bool }
type UsageRow struct{ Key; Messages int64; Tokens; CostReportedUSD *float64 }
func (r UsageRow) Validate() error
type SessionInfo struct{ Source, Session, TaskSource string; LabelsSeen int } // TaskSource: "map"|"heuristic"|"none"
func (s SessionInfo) Validate() error
func ValidTask(s string) bool
func ValidModel(s string) bool
func ValidSession(s string) bool
const NoTask = "-"
```
Charsets (anchored): source `[a-z][a-z0-9-]{0,31}`; session `[A-Za-z0-9_-]{1,128}`; model `[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}`; task `-` or `[A-Za-z0-9][A-Za-z0-9._/-]{0,63}` with no `..` segment; day `YYYY-MM-DD` that `time.Parse("2006-01-02")` accepts; counts ≥ 0; `CostReportedUSD` nil or finite ≥ 0.

- [ ] **Step 1:** Table test `TestUsageRowValidate`: valid row passes; each of `Task: "../x"`, `"=cmd"`, `"+1"`, `"@x"`, `"a b"`, 65-char task, `Model: "<synthetic>"`, `Day: "2026-13-01"`, `Session: "a/b"`, `Input: -1`, cost `NaN` and `-0.1` fails. `TestSessionInfoValidate` rejects TaskSource `"x"`.
- [ ] **Step 2:** Run `go test ./internal/core/` → FAIL (undefined).
- [ ] **Step 3:** Implement with package-level `regexp.MustCompile`.
- [ ] **Step 4:** Run → PASS. ub-mode tools on `internal/core`.
- [ ] **Step 5:** `git commit -m "feat(core): add usage row model and validation"`

### Task 2: Core ports, cost, snapshot, aggregation

**Files:**
- Create: `internal/core/ports.go`, `internal/core/cost.go`, `internal/core/snapshot.go`, `internal/core/report.go`, tests next to each.

**Interfaces:**
- Consumes: Task 1 types.
- Produces:
```go
type Batch struct{ Rows []UsageRow; Sessions []SessionInfo; Skipped int }
type UsageSource interface{ Read(ctx context.Context) (Batch, error) }
type SnapshotLog struct{ At time.Time; Sessions, Rows, Skipped int }
type Store interface {
    // ReplaceSessions deletes every stored row of each session in b.Sessions, inserts b.Rows, upserts b.Sessions and appends log, in one transaction.
    ReplaceSessions(ctx context.Context, b Batch, log SnapshotLog) error
    Rows(ctx context.Context) ([]UsageRow, error)
    Sessions(ctx context.Context) ([]SessionInfo, error)
}
type Price struct{ Input, Output, CacheWrite5m, CacheWrite1h, CacheRead float64 } // USD per million tokens
type PriceBook interface {
    Lookup(model string, longContext bool) (Price, bool)
    LongContext(model string, promptTokens int64) bool
}
type Clock interface{ Now() time.Time }
func Cost(r UsageRow, pb PriceBook) (usd float64, priced bool) // CostReportedUSD wins when set
func Snapshot(ctx context.Context, src UsageSource, st Store, clk Clock) (SnapshotLog, error) // validates every row/session first; any invalid → error, nothing written
type Query struct{ By, Task, Since string } // By: task|model|day|session; Since: YYYY-MM-DD inclusive, "" = all
func (q Query) Validate() error
type Group struct{ Name string; Messages int64; Tokens; USD float64; Priced bool; UnpricedMessages int64 }
type Report struct{ Groups []Group; TotalUSD float64; UnpricedMessages int64; MultiLabelSessions []string }
func Aggregate(rows []UsageRow, sessions []SessionInfo, pb PriceBook, q Query) Report
```
`Aggregate`: filter by `Task` (compared after cutting the row task at the first `/`) and `Since`; group name for `task` is the task cut at the first `/`; groups sorted by name; `Group.Priced` false only when no row of the group is priced; `MultiLabelSessions` = session ids (sorted) of filtered rows whose `SessionInfo.LabelsSeen > 1`.

Execution order: write `ports.go` (signatures only), then do Task 3, then the rest of this task; its tests use the `kanjotest` fakes.

- [ ] **Step 1:** Tests: `TestCostFormula` (row 1,000,000 of each field with price {4,20,5,8,0.2} → 37.2), `TestCostUnpriced` (unknown model → priced=false), `TestCostReportedWins`, `TestCostLongContextTier`; `TestSnapshotRejectsInvalidBatch` (store untouched), `TestSnapshotLogsClockTime`; `TestAggregateByTaskCutsAtSlash`, `TestAggregateUnpricedNotInTotal`, `TestAggregateSinceInclusive`, `TestAggregateFlagsMultiLabelSessions`, `TestQueryValidateRejectsBadBy`.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run → PASS; ub-mode tools.
- [ ] **Step 5:** `git commit -m "feat(core): add ports, cost, snapshot and aggregation"`

### Task 3: Test kit — fakes and contract suites (helper)

**Files:**
- Create: `internal/kanjotest/fakes.go`, `internal/kanjotest/contracts.go`, `internal/kanjotest/kanjotest_test.go`

**Interfaces:**
- Produces:
```go
type MemStore struct{ /* mutex-guarded */ }            // core.Store
func NewMemStore() *MemStore
func (m *MemStore) Logs() []core.SnapshotLog
type StaticSource struct{ Batch core.Batch; Err error } // core.UsageSource
type FixedClock struct{ T time.Time }                   // core.Clock
type TablePrices struct{ Prices map[string]core.Price; Long map[string]core.Price; Threshold map[string]int64 } // core.PriceBook
func StoreContract(t *testing.T, open func(t *testing.T) core.Store)
func PriceBookContract(t *testing.T, pb core.PriceBook, known string) // known: a model it prices
func UsageSourceContract(t *testing.T, src core.UsageSource)          // every row and session validates; rows' sessions all listed in Sessions
```
`StoreContract` cases: empty store returns no rows; replace then read returns same rows; replacing a session drops its old rows (task changed → no double count); untouched sessions kept; replace twice is idempotent; nil vs set `CostReportedUSD` round-trips; label `a.b-c/d` round-trips.

- [ ] **Step 1:** `kanjotest_test.go` runs `StoreContract(t, MemStore)`, `PriceBookContract(t, TablePrices{...}, "m")`, `UsageSourceContract(t, StaticSource{...})`.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run → PASS; ub-mode tools.
- [ ] **Step 5:** `git commit -m "test: add fakes and contract suites for core ports"`

### Task 4: SQLite store adapter

**Files:**
- Create: `internal/sqlitestore/store.go`, `internal/sqlitestore/store_test.go`

**Interfaces:**
- Consumes: `core.Store`, `kanjotest.StoreContract`.
- Produces: `func Open(dir string) (*Store, error)`, `func (s *Store) Close() error`; DB at `<dir>/kanjo.db`.

Schema (`PRAGMA user_version = 1`; refuse a higher version with an error):
```sql
CREATE TABLE usage(source TEXT, session TEXT, day TEXT, task TEXT, model TEXT, long_context INTEGER,
  messages INTEGER, input INTEGER, output INTEGER, reasoning INTEGER, cache_write_5m INTEGER,
  cache_write_1h INTEGER, cache_read INTEGER, cost_reported_usd REAL,
  PRIMARY KEY(source, session, day, task, model, long_context));
CREATE TABLE sessions(source TEXT, session TEXT, task_source TEXT, labels_seen INTEGER, PRIMARY KEY(source, session));
CREATE TABLE snapshots(at TEXT, sessions INTEGER, rows INTEGER, skipped INTEGER);
```
`Open`: `MkdirAll(dir, 0700)`; write `<dir>/.gitignore` = `*\n` if absent; create DB then `chmod 0600`. Rows read back are re-validated (`Validate`), a bad stored row is an error.

- [ ] **Step 1:** Tests: `TestStoreContract` (via `kanjotest.StoreContract` with `t.TempDir()`), `TestOpenCreatesPrivateDirAndGitignore` (dir 0700, db 0600, `.gitignore` == `"*\n"`), `TestOpenUnwritableDirFails` (parent 0500 → error mentions path, no db file), `TestOpenRejectsNewerSchema`.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement (parameterized SQL only). **Step 4:** PASS; ub-mode tools.
- [ ] **Step 5:** `git commit -m "feat(store): add SQLite store"`

### Task 5: Claude Code transcript source

**Files:**
- Create: `internal/claudecode/source.go`, `internal/claudecode/lines.go` (bounded line reader, helper), tests, `testdata/transcripts/...`

**Interfaces:**
- Consumes: `core.UsageSource`, `core.Batch`, `kanjotest.UsageSourceContract`.
- Produces:
```go
type Source struct {
    ProjectsDir  string
    Prefixes     []string           // project dir name prefixes
    TaskPattern  *regexp.Regexp     // nil = no heuristic
    TaskMap      map[string]string  // session prefix → task; longest prefix wins
    MaxLineBytes int
    LongContext  func(model string, promptTokens int64) bool
}
func (s Source) Read(ctx context.Context) (core.Batch, error) // Source == "claude"
func ReadTaskMap(r io.Reader) (map[string]string, error)      // lines "<prefix>\t<task>", '#' comments, invalid task → error with line number
func ForEachLine(r io.Reader, max int, fn func([]byte)) (oversized int, err error) // helper: lines > max are discarded and counted, reading continues
```
Rules: files `<dir>/<session>.jsonl` and `<dir>/<session>/subagents/*.jsonl` for every dir in `ProjectsDir` whose name starts with a prefix; `os.Lstat`, only regular files (symlinks skipped). Decode into a struct with only `type`, `timestamp`, `isSidechain`, `message.{id, model, usage, content}`; `content` is decoded only for `type=="user" && !isSidechain` and only scanned with `TaskPattern`, never retained. Dedupe by `message.id` across all files of a session, last wins. Skip `<synthetic>`. 5m = `cache_creation.ephemeral_5m_input_tokens`, 1h = `ephemeral_1h_input_tokens`; without `cache_creation`, 5m = `cache_creation_input_tokens`, 1h = 0. Prompt tokens = input + cache_read + 5m + 1h. Skipped (counted in `Batch.Skipped`): undecodable JSON, oversized, missing/invalid timestamp, model or session failing core validation. Task: map > most frequent heuristic label (ties: lexically smallest) > `-`. `ProjectsDir` missing → empty batch, no error.

Fixtures (invented ids `s-aaaa…`, cwd `/home/dev/demo`), each assistant message with round numbers so totals are hand-checkable:
- `-home-dev-demo/s-one.jsonl`: user msg naming `ABC-12` twice and `XYZ-3` once; msg `m1` opus (10 in/100 out/1000 read, cache_creation 5m 200 1h 300) appearing on 2 lines; msg `m2` sonnet without `cache_creation` (`cache_creation_input_tokens` 50); a `<synthetic>` msg; a corrupt line; a canary string `CANARY-7f3a` in user and assistant content.
- `-home-dev-demo/s-one/subagents/agent-x.jsonl`: msg `m3` haiku, plus a second copy of `m2` (dedupe across files).
- `-home-dev-demo-sub/s-two.jsonl`: msg `m4` `claude-unknown-9` (unpriced), day 2026-11-02; msg `m5` haiku with prompt > 100000 (long context).
- `-home-dev-other/s-three.jsonl`: must not be read (prefix mismatch).
- truncated last line in `s-two.jsonl`; one line with no timestamp.
- A symlink fixture is created in the test (`os.Symlink` in `t.TempDir()`), not committed.

- [ ] **Step 1:** Tests: `TestSourceContract`; `TestReadFixturesExactRows` (full expected `[]UsageRow` and `Skipped` written by hand); `TestDedupeLastCopyWins`; `TestSubagentsBelongToParent`; `TestSameSessionUnderTwoPrefixes`; `TestSkipsSynthetic`; `TestCacheCreationFallback`; `TestLongContextPerMessage`; `TestTaskMapBeatsHeuristic`; `TestHeuristicMostFrequentAndLabelsSeen` (LabelsSeen = 2); `TestSkipsSymlink`; `TestMissingProjectsDirIsEmpty`; `TestCanaryNeverInBatch` (`fmt.Sprintf("%+v", batch)` lacks `CANARY-7f3a`); `TestForEachLineOversized` (line of max+1 discarded, next line delivered); `TestReadTaskMapRejectsBadTask`.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement `ForEachLine` with `bufio.Reader.ReadSlice` loop discarding over-long fragments, then `Source`. **Step 4:** PASS; ub-mode tools.
- [ ] **Step 5:** Commits: `feat(claudecode): add bounded line reader`, `feat(claudecode): read usage from Claude Code transcripts`.

### Task 6: Prices file and system clock

**Files:**
- Create: `internal/pricesfile/pricesfile.go`, `internal/pricesfile/prices.json` (the one versioned copy, embedded with `//go:embed`; no root copy), `internal/pricesfile/pricesfile_test.go`, `internal/sysclock/clock.go`.

**Interfaces:**
- Produces: `func Parse(data []byte) (*Book, error)`, `func Default() *Book` (embedded), `func Load(path string) (*Book, error)` (`""` → Default), `(*Book) Source() string`, `(*Book) Fetched() string`; `*Book` implements `core.PriceBook`. `sysclock.Clock{}` implements `core.Clock`.

Format (strict: `DisallowUnknownFields`, all prices finite ≥ 0, `fetched` a date, `models` non-empty, `above_prompt_tokens` > 0):
```json
{"source":"https://platform.claude.com/docs/en/about-claude/pricing","fetched":"2026-11-03","unit":"USD per million tokens",
 "models":{"claude-haiku-5-5":{"input":0.10,"cache_write_5m":0.125,"cache_write_1h":0.20,"cache_read":0.01,"output":0.50,
   "long_context":{"above_prompt_tokens":100000,"input":0.50,"cache_write_5m":0.625,"cache_write_1h":1,"cache_read":0.05,"output":2.50}}}}
```
Values for fable/opus/sonnet/haiku exactly as the spec's table (section 7).

- [ ] **Step 1:** Tests: `TestPriceBookContract` (Default, known `claude-opus-5-5`); `TestDefaultMatchesSpecTable` (all 4 models, all 5 prices, haiku tier); `TestParseRejects` (unknown field, negative price, empty `models`, bad `fetched` date, `above_prompt_tokens` 0); `TestLongContextThreshold` (100000 → false, 100001 → true, opus → false).
- [ ] **Step 2–4:** FAIL → implement → PASS; ub-mode tools.
- [ ] **Step 5:** `git commit -m "feat(prices): add versioned prices file and system clock"`

### Task 7: Layered config

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces:
```go
type Config struct {
    DataDir    string `toml:"data_dir"`    // default ".ai-usage"
    PricesFile string `toml:"prices_file"` // default "" (embedded)
    Claude struct {
        ProjectsDir     string   `toml:"projects_dir"`     // default "<home>/.claude/projects"
        ProjectPrefixes []string `toml:"project_prefixes"` // default [cwd with "/" → "-"]
        MaxLineBytes    int      `toml:"max_line_bytes"`   // default 67108864, must be > 0
    } `toml:"claude"`
    Task struct {
        Pattern string `toml:"pattern"`  // default `\b[A-Z][A-Z0-9]+-[0-9]+(\.[0-9]+)?\b`, "" disables
        MapFile string `toml:"map_file"` // default "<data_dir>/claude-tasks.tsv"
    } `toml:"task"`
}
func Load(home, cwd, xdgConfigHome string) (Config, error)
```
Layers: defaults < `<xdg or home/.config>/kanjo/config.toml` < `<cwd>/kanjo.toml`; missing files are fine; unknown keys (`MetaData.Undecoded`) are an error naming file and key; invalid regex is an error. Relative `data_dir`/`map_file` resolve against `cwd`. Flags (Task 8) are applied after `Load`.

- [ ] **Step 1:** Tests: `TestDefaults`, `TestRepoOverridesUser`, `TestUnknownKeyFails`, `TestBadPatternFails`, `TestZeroMaxLineBytesFails`, `TestMapFileDefaultsUnderDataDir`.
- [ ] **Step 2–4:** FAIL → implement → PASS; ub-mode tools.
- [ ] **Step 5:** `git commit -m "feat(config): add layered TOML config"`

### Task 8: `snapshot` and `usage` commands

**Files:**
- Modify: `internal/cli/cli.go`
- Create: `internal/cli/snapshot.go`, `internal/cli/usage.go`, `internal/cli/cli_test.go`, `internal/cli/e2e_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `type env struct{ home, cwd, xdg string; clock core.Clock }` used by `run(args, stdout, stderr, env) int`; `Run` builds the real env.

Commands:
- `kanjo snapshot [--data-dir DIR]` → prints `snapshot: <n> sessions, <m> rows, <k> lines skipped -> <db path>`; if k > 0 also `warning: <k> transcript lines skipped (unreadable or invalid)` on stderr.
- `kanjo usage [--by task|model|day|session] [--task T] [--since YYYY-MM-DD] [--csv] [--data-dir DIR]`, default `--by task`. Columns: `<by>, messages, input, output, reasoning, cache_write_5m, cache_write_1h, cache_read, est_usd, unpriced_messages`; `est_usd` `%.2f` or empty when the group is unpriced. Text: `text/tabwriter`, then `total est_usd <x> (<n> messages unpriced)`, warnings `session <id> mentions several tasks; counted on one` per multi-label session, and the estimate notice line. CSV: `encoding/csv`, header + groups only, notice on stderr.
- Bad flag/value → stderr usage, exit 2.

- [ ] **Step 1:** `e2e_test.go` copies `testdata/transcripts` into `t.TempDir()` as `home/.claude/projects`, cwd `/home/dev/demo`, `FixedClock`: `TestSnapshotThenUsageByTaskExactTotals` (hand-computed table), `TestSnapshotTwiceUnchanged` (rows equal; 2 log lines), `TestUnpricedShownAndExcludedFromTotal`, `TestUsageCSV` (parse output, exact header), `TestNoTranscriptsDir` and `TestUsageEmptyStore` (Review Focus 1), `TestCanaryNeverInOutputOrDB` (stdout, stderr and raw `kanjo.db` bytes), `TestBadByExits2`, `TestMultiLabelWarning`.
- [ ] **Step 2–4:** FAIL → implement → PASS; ub-mode tools.
- [ ] **Step 5:** `git commit -m "feat(cli): add snapshot and usage commands"`

### Task 9: `self-update`

**Files:**
- Create: `internal/selfupdate/selfupdate.go`, `internal/selfupdate/selfupdate_test.go`, `internal/cli/selfupdate.go`

**Interfaces:**
- Produces:
```go
type Updater struct {
    Client       *http.Client
    LatestURL    string   // "https://api.github.com/repos/R0b-1ns/kanjo/releases/latest"
    AllowedHosts []string // "api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"
    Current, GOOS, GOARCH, Exe string
}
func (u Updater) Check(ctx context.Context) (latest string, available bool, err error) // available = tag != Current
func (u Updater) Update(ctx context.Context) (string, error)
```
Fetch only `https` URLs whose host is in `AllowedHosts`, including after redirects (`CheckRedirect`). Caps: API body 1 MiB, `SHA256SUMS` 64 KiB, binary 200 MiB; client timeout 2 min. Asset `kanjo-<GOOS>-<GOARCH>`; hash must equal its line in `SHA256SUMS`; write `<dir of Exe>/.kanjo-update-*` (0700 → 0755 after fsync), rename over `Exe` (`os.Executable` + `filepath.EvalSymlinks` in the CLI); remove temp on any error. CLI: `kanjo self-update [--check]`; prints `kanjo is up to date (<tag>)`, `update available: <cur> -> <tag>`, or `updated <cur> -> <tag>`.

- [ ] **Step 1:** Tests with `httptest.NewTLSServer` (AllowedHosts = its host): `TestUpdateReplacesBinary`, `TestUpToDateDoesNothing`, `TestCheckOnly`, `TestHashMismatchKeepsOldBinary`, `TestForeignHostRefused` (asset URL `https://evil.example/x`), `TestRedirectToForeignHostRefused`, `TestOversizedBinaryRefused`, `TestHTTPURLRefused`, `TestMissingAssetForPlatform`, `TestReadOnlyDirFails` (old binary intact, no temp left).
- [ ] **Step 2–4:** FAIL → implement → PASS; ub-mode tools.
- [ ] **Step 5:** `git commit -m "feat: add self-update from GitHub releases"`

### Task 10: Documentation

**Files:**
- Create: `docs/README.md` (index), `docs/configuration.md`, `docs/commands/snapshot.md`, `docs/commands/usage.md`, `docs/commands/self-update.md`, `docs/commands/version.md`, `docs/data-model.md` (rows, key, schema, merge rule, data dir), `docs/prices.md` (format, tiers, update by hand), `docs/claude-transcripts.md` (reading rules, task attribution, limits), `docs/adding-an-adapter.md`, `docs/release.md` (tag format, assets, `gh attestation verify dist/kanjo-darwin-arm64 --repo R0b-1ns/kanjo`), `docs/security.md` (threats and their tests), `docs/exit-codes.md`, `internal/cli/docs_test.go`, `docs/embed.go` if the test needs it.

Each file: one topic, exact names, table key/default/allowed values, copyable examples, self-contained, written for AI readers. Note the limits: the estimate is not a subscription bill; multi-task sessions without markers count on one task; the snapshot keeps purged sessions forever.

- [ ] **Step 1:** `TestEveryFlagDocumented` (each flag registered by each command's `flag.FlagSet` appears as `--name` in `docs/commands/<cmd>.md`) and `TestEveryConfigKeyDocumented` (each `toml` tag path appears in `docs/configuration.md`).
- [ ] **Step 2–4:** FAIL → write docs → PASS.
- [ ] **Step 5:** `git commit -m "docs: document commands, config, data and release"`

### Task 11: Slice review and memory note

- [ ] **Step 1:** Free checks: `go vet ./...`, `go test -race -count=1 ./...`, `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`, `gofmt -l .` (empty), `crapper`, `dryer internal`, `mutator` on the whole slice. Fix findings test-first.
- [ ] **Step 2:** `/code-review`, then `/simplify`, then `security-review` on the full slice diff (re-run the threat table of the design). Each finding: red test first (superpowers:receiving-code-review), then fix, separate commit.
- [ ] **Step 3:** superpowers:verification-before-completion: rerun Step 1, `scripts/build.sh test`, a manual `kanjo snapshot` + `kanjo usage` run against a temp copy of the fixtures.
- [ ] **Step 4:** `cairn add` one note: changes, decisions (from the design's calls table), open items (slices 2–9, global view, env config layer, `prices check`), helpers (`internal/kanjotest` fakes + contracts, `claudecode.ForEachLine` bounded reader, `selfupdate` verified download + atomic replace).

# Slice 2 Implementation Plan: web page, price check, multi-project view

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `kanjo serve` (read-only loopback page + `/api/data`), `kanjo prices check`, and a multi-project view (`usage --all`, page scope "all projects").

**Architecture:** `internal/core` gains pure aggregation helpers (per-source totals, dashboard, multi-project). A new `internal/projects` adapter reads other projects' databases read-only. A new `internal/web` package holds the HTTP handler and the embedded page. `internal/cli` wires them. `usage` and `/api/data` both call `core.Aggregate`, so totals cannot diverge.

**Tech Stack:** Go standard library (`net/http`, `embed`, `httptest`), existing `sqlitestore`, `BurntSushi/toml`. No new dependency. Page is plain HTML, CSS, JS and SVG embedded in the binary.

**Spec:** `docs/superpowers/specs/2026-11-03-serve-prices-check-global-view-design.md` (threat table and decisions live there); founding `docs/spec.md` sections 7, 9, 10, 11.

## Global Constraints

- Everything committed is public: English, no other projects, no personal paths. Conventional commits, one logical change each, never a broken build; commit trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Never push, merge, open a PR or publish.
- TDD: write the test, run it, **see it fail for the right reason**, then implement. `/ub-mode` on each change. Tests are offline (`httptest` on 127.0.0.1 only), fixtures fictional.
- `internal/core` imports no adapter and calls no `os`, `net`, `time.Now`.
- Strict inputs at the boundary; errors handled; no secret or content (prompt, answer, code) in logs, pages or data; the server logs nothing.
- Any new config key must be in `docs/configuration.md`; any new flag in its `docs/commands/*.md` (existing tests enforce both).
- Default listen address is `127.0.0.1` and cannot be changed in this slice.
- Gates before review: `go vet ./... && gofmt -l .`, `go test -race -count=1 ./...`, `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`, `crapper --changed` (CRAP < 10), `dryer <path>` (< 0.8), `mutator --changed --no-coverage --mutate-all` (kill survivors).

## Review Focus

- Empty data folder or empty project list: `/api/data` returns valid JSON with `[]` (never `null`) and the page shows an empty state.
- `Host` variants: uppercase, trailing dot, `[::1]:port`, `127.0.0.1` without port, empty: all 403 except exact `127.0.0.1:<port>` and `localhost:<port>`.
- One project database corrupt, symlinked or missing next to a good one: the good one is still reported; the bad one is named (folder base name only), no path.
- `since` after every day, unknown `task`, `scope=bogus`, repeated query parameters: 200 with empty groups or 400, never a panic.
- A task filter must keep offering the other tasks (the list of tasks is computed before the task filter).
- A busy port: error, no silent fallback to another port.

---

### Task 1: Per-source totals and dashboard in core

**Files:**
- Modify: `internal/core/report.go` (add `USDBySource`, fill in `Group.add`)
- Create: `internal/core/overview.go`, `internal/core/overview_test.go`
- Modify: `internal/cli/render.go` (use `core.EstimateNotice`)

**Interfaces:**
- Produces: `Report.USDBySource map[string]float64` (priced cost per `UsageRow.Source`; unpriced rows add nothing); `const EstimateNotice = "Claude cost is an estimate at API list prices, not a subscription bill."`; `type Dashboard struct{ Models, Tasks, Days Report }`; `func NewDashboard(rows []UsageRow, sessions []SessionInfo, pb PriceBook, q Query) Dashboard` (runs `Aggregate` three times with `By` = model, task, day; `q.By` ignored); `type ProjectUsage struct{ Name string; Rows []UsageRow; Sessions []SessionInfo }`; `func Flatten(ps []ProjectUsage) ([]UsageRow, []SessionInfo)`.

- [ ] **Step 1: Failing tests** in `overview_test.go`, using `kanjotest.TablePrices`-style prices from `core` tests (see `report_test.go` for the helper in use):
  `TestUSDBySourceSplitsClaudeAndJuniors` (two rows, sources `claude` and `junior` with `CostReportedUSD` 0.5; expect `{"claude": x, "junior": 0.5}` and `TotalUSD` equal to the sum); `TestUnpricedRowsAddNothingToSources` (unknown model: map has no entry, `UnpricedMessages` counted); `TestNewDashboardGroupsThreeWays` (same rows give `Models.TotalUSD == Tasks.TotalUSD == Days.TotalUSD`, names sorted); `TestFlattenKeepsAllRows`.
- [ ] **Step 2: Run** `go test ./internal/core/ -run 'USDBySource|Dashboard|Flatten' -v`; expected FAIL (undefined).
- [ ] **Step 3: Implement** the fields and functions above. `Group.add` already receives `*Report`; allocate the map lazily. Replace the `estimateNotice` const in `cli/render.go` by `core.EstimateNotice`.
- [ ] **Step 4: Run** `go test -race ./internal/core/ ./internal/cli/`; expected PASS (existing `usage` tests unchanged).
- [ ] **Step 5: Commit** `feat(core): split totals by source and add the dashboard aggregate`.

### Task 2: Multi-project aggregation in core

**Files:** Modify `internal/core/report.go` (add `ValidateAll`), `internal/core/overview.go`; tests in `overview_test.go`.

**Interfaces:**
- Consumes: `ProjectUsage`, `Flatten`, `Aggregate` (Task 1).
- Produces: `func (q Query) ValidateAll() error` (like `Validate` but `By` in `project|task|model|day`; session refused with the message `--by session is not available with --all`); `func AggregateProjects(ps []ProjectUsage, pb PriceBook, q Query) Report` (for `By == "project"`, one `Group` per project named `Name`, summing that project's task groups, so `TotalUSD` and `UnpricedMessages` are the sums; for other `By` values, `Aggregate` over `Flatten(ps)`).

- [ ] **Step 1: Failing tests** with two hand-built projects on `claude-sonnet-5-5` and `claude-opus-5-5` using the real default prices (sonnet input 2 and opus output 20 USD per million): project `app-a`: 3 messages, input 1,000,000, day 2026-11-02, task ABC-1 gives **2.0**; project `app-b`: 2 messages, opus output 500,000, day 2026-11-03, task XYZ-3 gives **10.0**; plus `app-b` row of model `claude-unknown-9`, 2 messages, unpriced. Tests: `TestAggregateProjectsByProject` (groups `app-a` 2.0 and `app-b` 10.0, total 12.0, unpriced 2), `TestAggregateProjectsByModelSumsAcrossProjects`, `TestAggregateProjectsSinceFilter` (since 2026-11-03 leaves only `app-b`), `TestValidateAll` (project ok, session refused, bogus refused).
- [ ] **Step 2: Run**; expected FAIL (undefined).
- [ ] **Step 3: Implement.** Shortcut to note in a comment: `MultiLabelSessions` ids are not project-qualified; upgrade if two projects reuse a session id.
- [ ] **Step 4: Run** `go test -race ./internal/core/`; PASS.
- [ ] **Step 5: Commit** `feat(core): aggregate usage across projects`.

### Task 3: Config keys `projects`, `serve.port`, `prices.max_age_days`

**Files:** Modify `internal/config/config.go`, `internal/config/config_test.go`.

**Interfaces:**
- Produces on `Config`: `Projects []string \`toml:"projects"\``, `Serve ServeConfig \`toml:"serve"\`` with `Port int \`toml:"port"\``, `Prices PricesConfig \`toml:"prices"\`` with `MaxAgeDays int \`toml:"max_age_days"\``. Defaults: no projects, port `8787`, max age `30`. Add `func ProjectName(folder string) string` (the folder base name).

- [ ] **Step 1: Failing tests:** `TestNewKeysHaveDefaults`; `TestProjectsMustBeAbsoluteWithUniqueSafeNames` (relative path, duplicate base name, base name failing `core.ValidTask` without `/`, empty entry: each an error naming the file); `TestPortRange` (0 and 65536 refused in config, 1 and 65535 accepted); `TestMaxAgeDaysMustBePositive`; `TestRepoConfigMayNotSetProjectsServeOrPrices` (a `./kanjo.toml` with each of the three keys is refused, message names the key, nothing is read from the listed folders); `TestUserConfigMaySetThem`.
- [ ] **Step 2: Run** `go test ./internal/config/ -run 'NewKeys|Projects|Port|MaxAge|RepoConfigMayNotSet' -v`; FAIL.
- [ ] **Step 3: Implement** in `Config`, `Load` defaults and `check()`. The repository allow-list already refuses unknown keys, so the new keys need no `repoKeys` entry (the test proves it). Project paths are cleaned with `filepath.Clean`.
- [ ] **Step 4: Run** `go test -race ./internal/config/ ./internal/cli/` (`TestEveryConfigKeyIsDocumented` fails until Task 12 writes the docs: add the three keys to `docs/configuration.md` key table now, minimal row each, completed in Task 12).
- [ ] **Step 5: Commit** `feat(config): add projects, serve.port and prices.max_age_days (user config only)`.

### Task 4: Read other projects' databases safely

**Files:** Create `internal/projects/projects.go`, `internal/projects/projects_test.go`.

**Interfaces:**
- Consumes: `sqlitestore.OpenReadOnly(dir)`, `config.ProjectName`, `core.ProjectUsage`.
- Produces: `type Result struct{ Projects []core.ProjectUsage; Failed []string }`; `func Read(ctx context.Context, folders []string) Result`. For each folder it opens `<folder>/.ai-usage` read-only. A missing database yields a project with no rows (it is simply empty). Any other error adds the project's base name to `Failed` and continues. `Failed` and `Projects` are never nil. Order follows `folders`. Errors are never returned with paths.

- [ ] **Step 1: Failing tests** (build databases with `sqlitestore.Open` + `Merge`): `TestReadsEachProjectIndependently`; `TestMissingDatabaseIsAnEmptyProject`; `TestCorruptDatabaseIsReportedAndOthersStillRead` (garbage bytes in `kanjo.db`); `TestSymlinkedDatabaseIsRefusedNotFollowed` (link to a valid database elsewhere: project in `Failed`, its rows absent); `TestReadNeverCreatesAnything` (folder tree identical before and after, including for a missing project); `TestHostileRowsInAnotherDatabaseAreFailures` (a row with a task starting with `=` inserted by raw SQL: project in `Failed`).
- [ ] **Step 2: Run** `go test ./internal/projects/ -v`; FAIL (package missing).
- [ ] **Step 3: Implement** `Read`; reuse the `readStore`-style logic by moving `readStore` from `cli/usage.go` into this package as `ReadStore(dir)` and have `usage` call it (no duplicate).
- [ ] **Step 4: Run** `go test -race ./internal/projects/ ./internal/cli/`; PASS.
- [ ] **Step 5: Commit** `feat(projects): read other projects' databases read-only`.

### Task 5: `usage --all`

**Files:** Modify `internal/cli/usage.go`, `internal/cli/render.go`; tests in `internal/cli/e2e_test.go` (new helper `seedProject(t, folder string, rows ...core.UsageRow)` using `sqlitestore.Open`).

**Interfaces:**
- Consumes: `projects.Read`, `core.AggregateProjects`, `Query.ValidateAll`, `config.Config.Projects`.
- Produces: flag `--all` (bool). With `--all`, `--by` defaults to `project` (detect "not set" with `fs.Visit`); the first CSV/table column is `project`. Unreadable projects print `warning: project NAME could not be read` on stderr (exit stays 0); with zero projects configured, print `no projects; set projects in the user config` and exit 0.

- [ ] **Step 1: Failing tests** with the Task 2 numbers across two seeded projects and a user config listing both folders: `TestUsageAllByProject` (CSV rows `app-a,3,1000000,0,0,0,0,0,2.0000,0` and `app-b,...,10.0000` plus the unpriced model counted, stderr total `total est_usd 12.0000 (2 messages unpriced)`), `TestUsageAllByModelAndDay`, `TestUsageAllRefusesSession` (exit 2), `TestUsageAllWithABrokenProjectStillPrintsTheRest` (stderr names `app-b`, no path), `TestUsageAllWithoutProjects`, `TestRepoConfigProjectsAreIgnoredByUsageAll` (a hostile `./kanjo.toml` makes `usage --all` exit 1, none of its folders read).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/cli/` (docs test fails for `--all` until Task 12; add the option row to `docs/commands/usage.md` now).
- [ ] **Step 5: Commit** `feat(cli): usage --all aggregates configured projects`.

### Task 6: `prices check`

**Files:** Create `internal/cli/prices.go`, `internal/cli/prices_test.go`; modify `internal/cli/cli.go` (register `prices`), `internal/cli/docs_test.go` (map command `prices` to `commands/prices-check.md`, register its flags).

**Interfaces:**
- Consumes: `setup(e, dataDir)`, `pricesfile.Book.Fetched()`, `core.Clock`, `projects.ReadStore`, `projects.Read`.
- Produces: `kanjo prices check [--all] [--data-dir DIR]`; `prices` with another or no subcommand exits 2. Behavior: prints `prices: <source>, retrieved <fetched>` and `age: N days (limit M)`; age in whole UTC days from `fetched` to `Clock.Now()`; warning `warning: prices are N days old (limit M); update the file` when **age > max_age_days** (30 days: no warning, 31: warning); warning `warning: price file date is in the future` when negative; warning `warning: model X has no price (N messages)` per unpriced model found in the data (`--all`: all configured projects). Exit 0 clean, 1 invalid prices file or unreadable data, 3 any warning. Never touches the network or writes.

- [ ] **Step 1: Failing tests** with `FixedClock` and a `prices_file` whose `fetched` is 2026-10-04 (age exactly 30 at 2026-11-03): `TestPricesCheckAtThirtyDaysDoesNotWarn` (exit 0), `TestPricesCheckAtThirtyOneDaysWarns` (exit 3), `TestPricesCheckThresholdIsConfigurable` (`prices.max_age_days = 7` in the user config), `TestPricesCheckFutureDate`, `TestPricesCheckInvalidFileExits1`, `TestPricesCheckListsUnpricedModels` (fixture `claude-unknown-9`), `TestPricesCheckWithoutDatabaseIsFine` (no folder created), `TestPricesCheckNeverWrites`, `TestPricesNeedsASubcommand` (exit 2).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/cli/`. Docs pages for the new command are written in Task 12; create a stub `docs/commands/prices-check.md` with the flag rows now so the docs test passes.
- [ ] **Step 5: Commit** `feat(cli): prices check warns on old prices and unpriced models`.

### Task 7: Web handler skeleton and request guards

**Files:** Create `internal/web/handler.go`, `internal/web/handler_test.go`, `internal/web/assets/index.html`, `app.js`, `app.css` (placeholders, real content in Task 9).

**Interfaces:**
- Produces: `type Loader func(ctx context.Context, scope string) (Loaded, error)`; `type Loaded struct{ Projects []core.ProjectUsage; Failed []string }`; `type PriceInfo interface{ core.PriceBook; Source() string; Fetched() string }`; `func NewHandler(port int, load Loader, book PriceInfo) http.Handler`. Routes: `/` (index.html), `/app.js`, `/app.css`, `/api/data`; anything else 404. Guards, applied to every route: method must be GET or HEAD else 405 with `Allow: GET, HEAD`; `Host` must equal exactly `127.0.0.1:<port>` or `localhost:<port>` else 403; query string over 1024 bytes gets 414. Headers on every answer: `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Resource-Policy: same-origin`, `Cache-Control: no-store`. No `Access-Control-*` header ever.

- [ ] **Step 1: Failing tests** (`httptest.NewRecorder`, port 8787): `TestOnlyGetAndHead` (POST, PUT, DELETE, PATCH, OPTIONS: 405); `TestHostMustBeLoopbackWithPort` (table: `127.0.0.1:8787` and `localhost:8787` 200; `evil.example`, `evil.example:8787`, `127.0.0.1`, `LOCALHOST:8787`, `localhost.:8787`, `[::1]:8787`, `127.0.0.1:9999`, empty: 403); `TestSecurityHeadersOnEveryRoute` (including 404 and 403; the exact CSP string above; no `Access-Control-Allow-Origin`); `TestUnknownPathsAre404` (`/`+`../`, `/etc/passwd`, `/api/`, `/app.js/`, `/%2e%2e/`); `TestHeadHasNoBody`; `TestLongQueryIs414`.
- [ ] **Step 2: Run** `go test ./internal/web/ -v`; FAIL.
- [ ] **Step 3: Implement** with `http.ServeMux` plus one wrapping function; assets via `//go:embed assets`, served from `embed.FS` by explicit route (no `http.FileServer`). `/api/data` returns 501 until Task 8.
- [ ] **Step 4: Run** `go test -race ./internal/web/`; PASS.
- [ ] **Step 5: Commit** `feat(web): read-only handler with host, method and header guards`.

### Task 8: `/api/data`

**Files:** Create `internal/web/data.go`, `internal/web/data_test.go`.

**Interfaces:**
- Consumes: Task 7 types, `core.NewDashboard`, `core.AggregateProjects`, `core.Flatten`, `core.EstimateNotice`.
- Produces: `GET /api/data?scope=project|all&task=LABEL&since=YYYY-MM-DD` (defaults `scope=project`; invalid value, unknown parameter or repeated parameter gives 400 with a fixed JSON `{"error":"bad request"}`; loader error gives 500 with `{"error":"cannot read data"}`, no detail). JSON body (field names are the contract, document them in Task 12):
  `scope`, `notice`, `prices{source,fetched}`, `total_usd`, `usd_by_source{}`, `unpriced_messages`, `models[]`, `tasks[]`, `days[]` (each Group: `name`, `messages`, `input`, `output`, `reasoning`, `cache_write_5m`, `cache_write_1h`, `cache_read`, `usd` (null when the group has no priced row), `unpriced_messages`), `filter_tasks[]` (task roots over all days and tasks, before filtering), `multi_task_sessions[]`, `projects[]` (`name`, `usd`, `messages`; only for `scope=all`, else `[]`), `failed_projects[]`. Every list is `[]` when empty. Content type `application/json; charset=utf-8`.

- [ ] **Step 1: Failing tests** with a fake `Loader` and `kanjotest.TablePrices`: `TestDataTotalsMatchTheAggregate` (hand-computed numbers from Task 2 fixture: total 12 with `usd_by_source` `{"claude":12}`, unpriced 2); `TestUnpricedModelAppearsAsNullAndDoesNotChangeTotal` (`usd` null for the unknown model, total unchanged); `TestFiltersTaskAndSince`; `TestFilterTasksIgnoreTheTaskFilter`; `TestScopeAllListsProjectsAndFailures`; `TestEmptyDataIsValidJSONWithEmptyLists` (decode into `map[string]any`, every list is a non-nil `[]any`); `TestBadParametersAre400` (`scope=x`, `task=../x`, `since=2026-13-40`, `since=a&since=b`, `foo=1`); `TestLoaderErrorHidesDetail` (error text containing a temp path is not in the body); `TestHostileValuesStayData` (session/task/model such as `<script>alert(1)</script>` built as rows bypassing `Validate`: the body decodes, the value comes back as a string and the raw bytes contain no unescaped `<script>`); `TestNoAbsolutePathsInBodies`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** with `encoding/json` into typed structs (`json.Encoder` with `SetEscapeHTML(true)`, the default).
- [ ] **Step 4: Run** `go test -race ./internal/web/`; PASS.
- [ ] **Step 5: Commit** `feat(web): /api/data from the shared aggregation`.

### Task 9: The page

**Files:** Replace the placeholders `internal/web/assets/index.html`, `app.js`, `app.css`; tests in `internal/web/assets_test.go`.

**Interfaces:** Consumes the `/api/data` contract. Layout (validated): one column of cards, filters at top (scope select, task select fed by `filter_tasks`, since date input), total card with the Claude / juniors split bar and the API-price notice and the unpriced count, cost per model, per task and per day as SVG bars, output tokens per model (bars), all tokens per model (table, cache reads included), the multi-task-session warning, projects card (scope all), failed projects, footer with price source and date. Colors are CSS custom properties: `--claude`, `--junior`; light and dark through `prefers-color-scheme`; cards stack on narrow screens (single column below 40rem, two columns above). All text goes through `textContent`; SVG built with `createElementNS`; filters re-fetch `/api/data` with the chosen query. A groups-with-null-`usd` row shows `no price`, never `0`.

- [ ] **Step 1: Failing tests (static):** `TestPageHasNoExternalReferences` (no `http://`, `https://`, `//cdn`, `@import`, `url(http` in any asset); `TestPageUsesNoInnerHTMLOrEval` (`innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write`, `eval(`, `new Function` absent from `app.js`); `TestPageHasNoInlineScriptOrStyle` (no `<style`, no `style=`, no `on[a-z]+=` handlers, one `<script src="/app.js">` in `index.html`); `TestPageStatesTheEstimateIsNotABill` (HTML or JS contains the sentence "not a subscription bill"); `TestPageSupportsDarkAndMobile` (CSS has `prefers-color-scheme: dark` and a `viewport` meta tag in HTML).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** the three files. Verify by eye later in Task 11 (real browser check with `serve` and a screenshot is part of the final verification, not a unit test).
- [ ] **Step 4: Run** `go test -race ./internal/web/`; PASS.
- [ ] **Step 5: Commit** `feat(web): single-page report with SVG charts and no external dependency`.

### Task 10: `kanjo serve`

**Files:** Create `internal/web/serve.go`, `internal/cli/serve.go`, `internal/cli/serve_test.go`; modify `internal/cli/cli.go`, `internal/cli/docs_test.go`; stub `docs/commands/serve.md` with the flag rows.

**Interfaces:**
- Produces: `func ListenLoopback(port int) (net.Listener, error)` (always `127.0.0.1`; port 0 picks a free port); `func Serve(ctx context.Context, ln net.Listener, h http.Handler) error` (refuses a listener whose address is not loopback; `ReadHeaderTimeout` 5 s, `ReadTimeout` 10 s, `WriteTimeout` 30 s, `IdleTimeout` 30 s, `MaxHeaderBytes` 8 KiB, `ErrorLog` discarded; shuts down gracefully when `ctx` ends). Command `kanjo serve [--port N] [--data-dir DIR]`: `--port` default is `serve.port`, valid 0..65535; prints `serving on http://127.0.0.1:<port>/` (one line, nothing else); stops on SIGINT or SIGTERM with exit 0; busy port exits 1 with a message. The Loader opens the current project (`cfg.DataDir`) for `scope=project` and `projects.Read(cfg.Projects)` for `scope=all`, read-only, on every request. `env` gains `ctx context.Context` (nil means background) so tests can stop the server.

- [ ] **Step 1: Failing tests:** `TestListenLoopbackIsLoopbackOnly` (address IP is `127.0.0.1`; a TCP dial to the machine's non-loopback address, found from `net.InterfaceAddrs`, on that port is refused; skip with a message when the machine has none); `TestServeRefusesNonLoopbackListener` (listener on `0.0.0.0:0` gives an error before serving); `TestServerLimits` (the `http.Server` fields above); `TestServeAnswersOnlyOnLoopback`/criterion §11 end to end: run the command with `--port 0` through a captured stdout, read the printed port, GET `/api/data` with the correct Host (200), with a foreign Host (403); `TestServeBusyPortFailsWithoutFallback`; `TestServeBadPort` (65536, -1, `abc`: exit 2); `TestServeCreatesNothingInTheDataFolder`; `TestServeLogsNothingToStderr`; **`TestPageAndCLIGiveTheSameTotals`**: seed the Task 5 fixture, call `usage --by task --csv` and `/api/data`, compare per-task `usd` formatted to four decimals, messages, token counts and the total (the numbers `2.0000` / `10.0000` / `12.0000` are asserted literally in both); `TestServeScopeAllReadsConfiguredProjects`; `TestCanaryNeverInPageOrJSON` (marker string absent from `/`, `/app.js`, `/api/data` bodies).
- [ ] **Step 2: Run** `go test ./internal/cli/ ./internal/web/ -run 'Serve|Listen|Limits|SameTotals|Canary' -v`; FAIL.
- [ ] **Step 3: Implement.** Signal handling via `signal.NotifyContext` in `Run` only (the `run` path uses `e.ctx`).
- [ ] **Step 4: Run** `go test -race -count=1 ./...`; PASS.
- [ ] **Step 5: Commit** `feat(cli): serve the read-only report on 127.0.0.1`.

### Task 11: Real-run verification (no commit unless a bug is found)

- [ ] **Step 1:** Build `go build -o $SCRATCH/kanjo ./cmd/kanjo`, create two temp projects with fixture transcripts in a temp HOME, `snapshot` each, set `projects` in a temp user config.
- [ ] **Step 2:** Run `kanjo serve --port 0` in the background; with `curl`: `/api/data` (200, JSON), `-H 'Host: evil.example'` (403), `-X POST` (405), `/etc/passwd` (404); show headers with `curl -i`.
- [ ] **Step 3:** Open the page with the Playwright tool at 1280 px and 390 px wide, light and dark (`browser_emulate_media`), take screenshots, check the console has no CSP violation and the empty state works on an empty data folder.
- [ ] **Step 4:** Any defect found: use superpowers:systematic-debugging, write the red test first, fix, commit `fix(...)`.

### Task 12: Documentation

**Files:** Create `docs/commands/serve.md`, `docs/commands/prices-check.md` (replace stubs), `docs/projects.md` (the global view: config key, `usage --all`, page scope, safety rules, example); modify `docs/configuration.md` (three keys with defaults, repo-config table rows saying "not allowed"), `docs/commands/usage.md` (`--all`, `--by project`), `docs/prices.md` (replace "planned, not built" by `prices check`), `docs/security.md` (new threat rows with the test names from Tasks 4 to 10; remove "Web server" from "Not in this slice"; add residual risks: any local process can read the page on loopback, the machine's other users on a shared host can too), `docs/exit-codes.md` (3 for `prices check`, per-command), `docs/README.md` (page table, quick start, "Not built yet"), `docs/claude-transcripts.md` untouched. `docs/commands/serve.md` documents the `/api/data` JSON contract from Task 8 in a table.

- [ ] **Step 1:** Write the pages: options and keys as tables with defaults, copyable examples (`projects = [...]`, `kanjo serve --port 9000`, `curl -s http://127.0.0.1:8787/api/data`, `kanjo prices check`).
- [ ] **Step 2:** Run `go test -race -count=1 ./internal/cli/` (docs consistency tests) and `gofmt -l .`; PASS, no output.
- [ ] **Step 3: Commit** `docs: document serve, prices check and the multi-project view`.

### Task 13: Gates, review and note

- [ ] **Step 1:** Run every gate from Global Constraints; paste the real results into the final report. `crapper --changed` and `dryer` findings above thresholds are fixed with a test first; `mutator --changed --no-coverage --mutate-all` survivors are killed with tests (an equivalent mutant is documented in the note).
- [ ] **Step 2:** `/code-review`, then `/simplify`, then a security review of the full diff against the threat table. Each finding gets a red test before its fix (superpowers:receiving-code-review); one `fix:` commit per logical finding.
- [ ] **Step 3:** `cairn add` one note for the slice: changes, decisions (config-key registry, fixed loopback, exit 3, `--all`), open points (all items out of scope in the spec, plus the automatic price update with diff review, session ids not project-qualified in warnings, `task.pattern` default heuristic still open), helpers with path and role (`internal/projects.Read`, `core.AggregateProjects`, `core.NewDashboard`, `web.NewHandler`, `web.ListenLoopback`, `cli.seedProject`). Do not edit any existing note.
- [ ] **Step 4:** Final report as requested (done and verified with real command output, skipped or unverified and why, threats with tests, decisions taken for the user, open points, helpers, cairn confirmation), then propose the order of the next slices for validation.

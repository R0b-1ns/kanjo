# Slice 1 design: foundations and Claude usage

Date: 2026-11-03. Source of truth for the product: [docs/spec.md](../../spec.md).
This document covers only slice 1. Everything else in the spec is out of scope here.

## Intent

kanjo must answer "what did this task cost, per model?" Slice 1 builds the core
(data model, ports, cost, aggregation) and the first real input: Claude Code
transcripts. With it, `kanjo snapshot` saves Claude usage locally and
`kanjo usage` reports tokens and estimated cost by task, model, day or session.

## Goals

- Core package with the normalized usage row (spec section 4), cost formula
  (section 7) and aggregation, importing no adapter and making no system call.
- Ports: `UsageSource`, `Store`, `PriceBook`, `Clock`. Each has a fake in a
  reusable test package and a contract test that every adapter runs.
- Adapters: Claude Code transcripts (`UsageSource`), SQLite (`Store`),
  `prices.json` (`PriceBook`), system clock (`Clock`).
- Commands: `snapshot`, `usage` (`--by task|model|day|session`, `--task`,
  `--since`, `--csv`), `version`, `self-update [--check]`.
- CI, and a release on every merge to main (section below).
- Fixtures of fictional transcripts covering every trap of section 3.1.
- Docs in `docs/`, one topic per file, written for AI readers.

## Non-goals (open, later slices)

Web page, delegation engine, opencode adapter, sandbox, caps, routing, agent
JSON returns, markers, cost per accepted task, `prices check`.

## Anti-goals

- Any prompt, response, code or other transcript text in the store, logs or output.
- A missing price shown as `$0`.
- A broken or changed transcript line aborting the run.
- Double counting after a snapshot is repeated or a task mapping changes.

## Decided by the user

Go (same toolchain shape as a single static binary), SQLite storage, MIT
license, `.cairn/` kept out of git.

Logging is per project (user, 2026-11-03): each project has its own data dir
(default `./.ai-usage/`) and reads only its own transcript prefixes. A global
view across projects is wanted later (slice 2); the project name is added at
aggregation time from the data dir list, so slice 1 rows need no project column.

## Builder's calls (mine, open to correction)

| Topic | Call |
|-------|------|
| SQLite driver | `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0` static binaries) |
| Config format | TOML via `github.com/BurntSushi/toml`; the routing slice will reuse it |
| Config layers | built-in defaults < `$XDG_CONFIG_HOME/kanjo/config.toml` (or `~/.config/...`) < `./kanjo.toml` < flags. The `KANJO_*` env layer is added with routing (slice 7), when it has a user |
| Day | UTC date of the message `timestamp` |
| `reasoning` for Claude | 0; thinking is already inside `output_tokens` (documented) |
| Haiku long-context tier | Chosen per message. `prices.json` may give a model a `long_context` tier with `above_prompt_tokens`; prompt = input + cache_read + cache_creation. Rows carry a `long_context` flag in their key |
| Snapshot merge | For each session read live, all its stored rows are replaced by the live rows; sessions absent from transcripts are kept untouched. Repeated snapshots are idempotent and a changed task mapping cannot double count |
| Task attribution (this slice) | (2) manual TSV map `<session-prefix>\t<task>` then (3) most frequent label matching `task.pattern` in non-sidechain user messages, else `-`. Markers (1) come later |
| Default `task.pattern` | `\b[A-Z][A-Z0-9]+-[0-9]+(\.[0-9]+)?\b` (issue-style ids) |
| Default project prefix | current directory with `/` replaced by `-` |
| Prices default | repo `prices.json` embedded in the binary; `prices_file` overrides |
| `--since` | `YYYY-MM-DD` only (inclusive) |
| `Clock` use in this slice | each snapshot appends one line to a `snapshots` table (time, sessions read, lines skipped); usage rows carry no timestamp, so repeating a snapshot leaves them equal |
| Manual task map | `task.map_file`, default `<data_dir>/claude-tasks.tsv`; longest matching prefix wins |
| Project prefixes | `claude.project_prefixes` list; each matches project dirs by prefix, so the workspace root also covers its sub-repos |
| `usage` input | Reads the store only (run `snapshot` first); says so when the store is empty |

## Domain model (core)

`UsageRow`: `source, session, day, task, model, long_context, messages, input,
output, reasoning, cache_write_5m, cache_write_1h, cache_read,
cost_reported_usd (nullable)`. Key: `(source, session, day, task, model,
long_context)`. All fields are validated at construction: closed charsets for
ids and labels, non-negative counts, `day` as `YYYY-MM-DD`.

`SessionInfo`: `source, session, task_source (map|heuristic|none),
labels_seen (count of distinct labels)`. `usage` warns when a session saw more
than one label (spec section 6).

Cost: `PriceBook.Lookup(model, longContext) (Price, bool)`. Not found means
unpriced: the report shows the cost as empty and counts the messages under
`unpriced_messages`; totals add priced rows only. Rows with
`cost_reported_usd` use it instead of the estimate (junior rows, later).

## Claude transcript adapter

Reads `<projects_dir>/<prefix>*/<session>.jsonl` and
`<projects_dir>/<prefix>*/<session>/subagents/*.jsonl` (subagents count for the
parent session). Rules from spec 3.1: only `type == "assistant"` with
`message.usage` and `message.id`; dedupe by `message.id` across all files of a
session, last copy wins; skip model `<synthetic>`; split cache creation into
5m/1h, falling back to all-5m when the detail is missing. Decodes only the
fields it needs into typed structs; unknown fields are ignored. Lines that fail
to decode or exceed `claude.max_line_bytes` are skipped and counted; the count
is printed as a warning. User message text is scanned in memory for the task
pattern only; only the matched label survives. Non-regular files and symlinks
are skipped.

## Threats for this slice (spec sections 10 and 12) and their tests

| Threat | Response | Test |
|--------|----------|------|
| Transcript text leaks into the store or output | Typed decoding of usage fields only; only regex-matched labels kept | fixture with a canary string in prompts/responses; assert the DB file and all command output do not contain it |
| Hostile model/session/label values (path or CSV formula injection, huge strings) | Closed charsets and length caps at the boundary; invalid values reject the line (counted) | table test of bad ids, labels starting with `=+@`, `../` |
| Oversized or corrupt lines | Bounded line reader; skip and count, never fatal | fixtures with a corrupt line and an oversized line |
| Symlink in the projects dir pointing elsewhere | Not followed | test with a symlinked session file |
| Usage data committed by mistake | Data dir created `0700` with its own `.gitignore` containing `*`; DB file `0600`; repo `.gitignore` lists `.ai-usage/` | test checks modes and the `.gitignore` file |
| Tampered `prices.json` | Strict decode, unknown fields rejected, negative prices rejected | table test |
| SQL injection | Parameterized queries only | review; contract test with hostile-looking labels |
| Secrets in logs | No logging of content; errors name the file and line number only | canary test covers stderr |

## Release and self-update (decided by the user)

- **CI** (`.github/workflows/ci.yml`): on pull requests and pushes to main:
  `go mod verify`, `go vet`, `go test -race`, `govulncheck`, `scripts/build.sh`.
- **Release** (`.github/workflows/release.yml`): on every push to main (a
  merge), after the same tests and build pass, create a GitHub release tagged
  `v0.0.0-<UTC yyyymmddhhmmss>-<12-char sha>` (sortable, Go pseudo-version
  shape) targeting that commit. Assets: `kanjo-{darwin,linux}-{amd64,arm64}`
  and `SHA256SUMS`, plus a GitHub build provenance attestation. Jobs run with
  read-only tokens except the single publish job; actions pinned by sha;
  `concurrency` serializes releases. No release-please.
- **`kanjo self-update`**: the only command that touches the network, and only
  when run. Reads the latest release of `R0b-1ns/kanjo` from the GitHub API,
  stops if its tag equals the running version, downloads
  `kanjo-<GOOS>-<GOARCH>` and `SHA256SUMS` over HTTPS, checks the hash, writes
  a temp file next to the running binary and renames it over it. `--check`
  only reports whether an update exists.

| Threat | Response | Test |
|--------|----------|------|
| Tampered download | SHA256 must match `SHA256SUMS`, else nothing is replaced; attestation documented for `gh attestation verify` (checksums alone prove integrity, not origin) | fake server serving a wrong hash |
| Redirect or URL from the API to another host | Only `https` URLs on `github.com` / `*.githubusercontent.com` / `api.github.com` are fetched | fake API returning a foreign asset URL |
| Huge or endless response | Size caps on API JSON and binary, request timeouts | fake server streaming past the cap |
| Half-written binary | Temp file in the same dir, fsync, chmod 0755, rename; temp removed on any error | failure mid-download leaves the old binary intact |
| Unwritable install dir | Clear error, no partial state | test with a read-only dir |

## Repository shape

```
cmd/kanjo/main.go
internal/core/            model, ports, cost, aggregate (no adapters, no os/time.Now)
internal/claudecode/      UsageSource adapter
internal/sqlitestore/     Store adapter
internal/pricesfile/      PriceBook adapter (+ embedded prices.json)
internal/sysclock/        Clock adapter
internal/config/          layered TOML/flag config
internal/selfupdate/      release lookup, download, verify, replace (HTTP client injected)
internal/cli/             wiring and commands
internal/kanjotest/       fakes and contract suites (helpers)
testdata/transcripts/     fictional fixtures
docs/                     spec.md, one file per topic
```

## Acceptance for this slice

- `snapshot` twice in a row leaves the DB byte-for-byte equal in content
  (same rows).
- `usage --by task` on the fixture set gives totals computed by hand in the test.
- An unpriced model is reported as unpriced and does not change the total.
- Core tests pass with fakes only.
- `go vet`, `go test -race`, `govulncheck`, crapper, dryer, mutator clean.

## Proposed next slices (for approval)

1. This slice.
2. `serve` read-only page on 127.0.0.1 + `prices check` + global view: a
   user-level list of project data dirs (filled by `snapshot`), and
   `usage --all` / the page aggregating them with a `project` column.
3. Markers (`mark start|end|current`) and per-message attribution.
4. Delegation core: `AgentRunner`, `Workspace`, `Sandbox` ports with fakes; `run`,
   `finish`, `summary`, journal, `report.json`/`report.txt`, watchdog; git
   worktree adapter; `none` sandbox.
5. opencode `AgentRunner` and junior `UsageSource`.
6. Agent JSON returns (schema v1, `invalid-report`, exit 5).
7. Routing config (`routing show|check`, fallbacks) and caps (exit 4).
8. Container sandbox (runtime and allow-list proxy decided then) with attack
   integration tests.
9. `batch`, cost per accepted task, page extensions.

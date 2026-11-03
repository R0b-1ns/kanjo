# Slice 2 design: read-only web page, price check, multi-project view

Source of truth: `docs/spec.md` sections 7, 9, 10, 11. This slice adds three
read-only features. Nothing here writes to a data folder, calls a model or
touches the network beyond listening on the loopback interface.

## Goals

- `kanjo serve`: a local page and a JSON route over the saved usage.
- `kanjo prices check`: offline check of the price file (age, schema, unpriced
  models).
- A global view over several projects, in the CLI and in the page.

## Non-goals

Task markers (§15), journal (§8), delegation engine and `usage --by role` (§5),
opencode (§3.2), JSON agent returns (§16), caps (§13), routing (§14), container
sandbox (§12), batch, cost per accepted task (§17), comparisons and cost per
verdict on the page (§9 "useful to add next"), repository-visibility policy
(§18), automatic price update with diff review (§18). All listed as `open` in
the slice note.

## Threats and abuse cases (written before the code)

| Threat | Answer | Test |
|---|---|---|
| Page reachable from the network | The listen address is fixed to `127.0.0.1`; there is no option to change it in this slice | `serve` test: the listener address is loopback; a dial to a non-loopback address of the machine is refused |
| DNS rebinding | `Host` must be exactly `127.0.0.1:<port>` or `localhost:<port>`, otherwise 403 | request with `Host: evil.example` gets 403 |
| Writes through the page | Only `GET` and `HEAD`; every other method gets 405; no handler writes anything | `POST`, `PUT`, `DELETE`, `OPTIONS` get 405 |
| Cross-origin reads | No CORS headers; `Cross-Origin-Resource-Policy: same-origin` | response has no `Access-Control-*` header |
| Script injection through a session id, task or model | Values are inserted with `textContent` and SVG attributes set by DOM calls, never `innerHTML`; JSON is produced by `encoding/json`; strict CSP (`default-src 'none'`, `script-src 'self'`, no inline script or style) | hostile values round-trip as data in `/api/data`; static check that the embedded JS has no `innerHTML`; CSP header test |
| Slow or huge requests | Read-header, read, write and idle timeouts; header size cap; query string length cap; request body ignored and limited | test of server settings; oversized query gets 414 |
| File listing, path leaks | Only four fixed routes, no file server; any other path 404; no absolute path in any body (project names are folder base names) | unknown route 404; body scan for the temp dir path |
| Other projects' databases are hostile | `OpenReadOnly` (no create, no write, `trusted_schema` off, links refused); rows re-validated on read; one unreadable project is reported by name and does not fail the others | contract tests on a corrupt, a symlinked and a missing project database |
| A cloned repository chooses which projects are read | `projects` is a user-config key, refused in `./kanjo.toml` (allow-list); project databases are always `<folder>/.ai-usage/kanjo.db`, the project's own `kanjo.toml` is never read | `config` tests |
| Secrets or content in pages and logs | The server logs nothing; the JSON holds counts, ids, model names, days, task labels only | canary test through `/api/data` and the HTML |
| Page and CLI disagree on totals | One aggregation (`core.Aggregate`) feeds both | test: `usage --by task` and `/api/data` give identical numbers, computed by hand in the fixture |
| Port clash or bad port | `serve.port` and `--port` validated 0..65535; a busy port is an error, never a fallback | test |

## Decisions

Validated by the user: the project list is a **user config key** (not a registry
written by `snapshot`), and the page is a **single column of cards**.
My calls (to review): fixed loopback address with no override; exit code 3 for
`prices check` warnings; `--all` flag on `usage`; project name = folder base
name, duplicates refused at load.

### Project list

User config only:

```toml
projects = ["/home/me/work/app-a", "/home/me/work/app-b"]
```

Each entry is a project folder, absolute. Its data is
`<folder>/.ai-usage/kanjo.db`. The display name is the folder base name: it
must be unique in the list and pass the task-label character rules, or the
config is refused. The current project is not added implicitly: the page
scopes are "this project" (the current folder's data folder) and "all projects"
(the list, which may include the current one; it is then counted once).

### serve

| Item | Value |
|---|---|
| Command | `kanjo serve [--port N] [--data-dir DIR]` |
| Config | `serve.port` (default `8787`, user and flag only) |
| Routes | `/` HTML, `/app.js`, `/app.css` (embedded), `/api/data` |
| `/api/data` query | `scope=project\|all`, `task=LABEL`, `since=YYYY-MM-DD`; invalid values get 400 |
| Output | total USD split by source (`claude`, `junior`), unpriced message count, groups by model, task, day (cost, messages, all token kinds), multi-label sessions, price source and date, the API-price notice, per-project totals when `scope=all`, projects that could not be read |

The server reads the databases on every request (read-only, small), so a new
`snapshot` shows after a refresh.

Page: one column of cards, filters at the top (scope, task, since), total card
with a Claude / juniors split bar and the API-price notice, cost per model, per
task and per day (SVG bars), output tokens per model, all tokens per model
(table, cache reads included), the multi-task session warning, price source and
date at the bottom. Light and dark through `prefers-color-scheme`. The
colour for `junior` exists even though no junior row does yet.

### prices check

`kanjo prices check [--data-dir DIR] [--all]`. Config `prices.max_age_days`
(default `30`, user and flag-free). Steps: load and validate the file
(`pricesfile.Load`, exit 1 on error); compute age with the injected `Clock`;
warn when age is strictly greater than the limit; read the project database
(or all with `--all`) read-only and list the models with no price. Exit 0
clean, 1 invalid file or unreadable data, 3 warnings. Never downloads.

### Global view in the CLI

`kanjo usage --all [--by project|model|day|task] [--task] [--since] [--csv]`;
`--by` defaults to `project` with `--all`. An unreadable project is reported on
stderr by name and the rest is printed (exit 0, warning). `--by session` is
refused with `--all` (session ids can collide across projects).

## Left to the builder

File layout inside `internal/` (suggestion: `internal/web` for the server and
embedded assets, `internal/core` for the source split and overview
assembly), chart pixel details, CSS.

# kanjo serve

Serves a read-only report of the saved usage on `http://127.0.0.1:<port>/`.
It reads the databases written by [snapshot](snapshot.md) on every request, so
a new snapshot shows after a page refresh. It writes nothing, creates nothing
and logs nothing.

```
kanjo serve [--port N] [--data-dir DIR]
```

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--port` | `serve.port` from the config (`8787`) | 0 to 65535 | Port on 127.0.0.1. `0` picks a free port; the chosen one is printed |
| `--data-dir` | `data_dir` from the config | a folder path | Folder holding `kanjo.db` for the scope "this project" |
| `-h` | | | Print the options and exit 0 |

There is no option to listen on another address: the server accepts
connections from this machine only. No positional arguments are accepted.

On start it prints exactly one line on stdout, `serving on http://127.0.0.1:<port>/`.
It stops on Ctrl-C (SIGINT) or SIGTERM and exits 0.

## Routes

| Route | Answer |
|---|---|
| `/` | The page (HTML) |
| `/app.js`, `/app.css` | The page script and style, embedded in the binary |
| `/api/data` | JSON report, below |
| anything else | 404 |

Only `GET` and `HEAD` are accepted (others get 405). The `Host` header must be
exactly `127.0.0.1:<port>` or `localhost:<port>` (anything else gets 403, which
stops DNS-rebinding pages). Every answer carries a strict
`Content-Security-Policy` (`default-src 'none'; script-src 'self'; ...`),
`X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
`Cross-Origin-Resource-Policy: same-origin` and `Cache-Control: no-store`; no
CORS header is ever sent. A query string over 1024 bytes gets 414.

## `/api/data`

Query parameters, each at most once; anything else gets `400` with
`{"error":"bad request"}`:

| Parameter | Default | Values | Meaning |
|---|---|---|---|
| `scope` | `project` | `project`, `all` | `project`: this folder's data. `all`: every project in `projects` of the user config ([../projects.md](../projects.md)) |
| `task` | all tasks | a task label | Same as `usage --task` |
| `since` | all days | `YYYY-MM-DD` | Same as `usage --since` |

A data folder that cannot be read gives `500` with `{"error":"cannot read data"}`
(the cause is never sent).

Answer fields (the lists are never `null`):

| Field | Meaning |
|---|---|
| `scope` | The scope used |
| `notice` | The sentence "Claude cost is an estimate at API list prices, not a subscription bill." |
| `prices` | `{"source", "fetched"}` of the price file |
| `total_usd` | Estimated cost of the selection; unpriced messages add nothing |
| `usd_by_source` | Cost per source: `claude` now, `junior` later. The page colours the total bar with it |
| `unpriced_messages` | Messages whose model has no price |
| `models`, `tasks`, `days` | Groups, each `{"name", "messages", "input", "output", "reasoning", "cache_write_5m", "cache_write_1h", "cache_read", "usd", "unpriced_messages"}`. `usd` is `null` (never 0) when the group has no priced row |
| `filter_tasks` | Every task of the data, ignoring the `task` filter, to fill the filter list |
| `multi_task_sessions` | Sessions that named several tasks but are counted on one |
| `projects` | With `scope=all`: one group per project; otherwise `[]` |
| `failed_projects` | Names (never paths) of projects that could not be read |

The totals come from the same function as [usage](usage.md): the page and
`kanjo usage --csv` give the same numbers (a test compares them).

## The page

One column of cards (two on wide screens): estimated total with the Claude and
juniors split and the API-price notice, cost per model, per task and per day,
output tokens per model, all tokens per model (cache reads included), cost per
project (scope "all projects"), a warning for sessions counted on one task, and
the price source and date. Filters: scope, task, start date. It follows the
system light or dark theme, has no external dependency and works on a phone.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Stopped normally |
| 1 | Bad config, port already in use, cannot listen |
| 2 | Bad `--port`, unknown option, stray argument |

## Examples

```sh
kanjo serve
kanjo serve --port 9000
curl -s http://127.0.0.1:8787/api/data
curl -s 'http://127.0.0.1:8787/api/data?scope=all&since=2026-11-01'
```

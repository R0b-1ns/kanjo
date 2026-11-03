# kanjo usage

Reports saved usage and its estimated cost. Reads only the database written by
[snapshot](snapshot.md); it never reads transcripts and never creates the data
folder.

```
kanjo usage [--all] [--by task|model|day|session|project] [--task LABEL] [--since YYYY-MM-DD] [--csv] [--data-dir DIR]
```

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--by` | `task` (`project` with `--all`) | `task`, `model`, `day`, `session`; with `--all`: `project`, `task`, `model`, `day` | Group rows by this field. For `task`, a label is cut at its first `/` (`ABC-12/part` counts under `ABC-12`). `session` is refused with `--all` (ids can collide across projects) |
| `--all` | off | flag | Report every project listed in `projects` of the user config instead of this folder's data; see [../projects.md](../projects.md). A project that cannot be read is named on stderr and skipped |
| `--task` | all tasks | a task label such as `ABC-12` or `ABC-12/review` | Keep rows whose task is this label or below it (`ABC-12` also selects `ABC-12/review`; `ABC-12/review` does not select `ABC-12/other`) |
| `--since` | all days | `YYYY-MM-DD` | Keep only days on or after this date (UTC days, inclusive) |
| `--csv` | off | flag | Print CSV on stdout instead of an aligned table |
| `--data-dir` | `data_dir` from the config | a folder path | Folder holding `kanjo.db` |
| `-h` | | | Print the options and exit 0 |

No positional arguments are accepted.

## Columns

| Column | Meaning |
|---|---|
| first column (named after `--by`) | Group name |
| `messages` | Assistant messages counted (after removing duplicates) |
| `input`, `output` | Tokens. `output` includes thinking tokens |
| `reasoning` | Always 0 for Claude (thinking is already inside `output`) |
| `cache_write_5m`, `cache_write_1h` | Tokens written to the 5-minute and 1-hour prompt cache |
| `cache_read` | Tokens read from the prompt cache |
| `est_usd` | Estimated cost in dollars, four decimals. **Empty** when no row of the group has a price |
| `unpriced_messages` | Messages whose model has no price in the price file. Their cost is not in `est_usd` and not in the total |

Cost formula and price file: [../prices.md](../prices.md). A model missing from
the price file is reported as unpriced, never as free.

## Output

Table (default):

```
task    messages  input  output  reasoning  cache_write_5m  cache_write_1h  cache_read  est_usd  unpriced_messages
ABC-12  4         36     164     0          250             340             1003        0.0066   0
XYZ-3   2         90100  250     0          400             0               20300       0.0461   1
total est_usd 0.0527 (1 messages unpriced)
attribution: 0 by marker, 0 by map, 6 by heuristic, 0 unattributed
warning: 6 messages attributed by heuristic, not by marker
warning: session s-one mentions several tasks; counted on one task
Claude cost is an estimate at API list prices, not a subscription bill.
prices: https://platform.claude.com/docs/en/about-claude/pricing, retrieved 2026-11-03
```

CSV (`--csv`): stdout holds only the header and one record per group, so it is
safe to pipe:

```
task,messages,input,output,reasoning,cache_write_5m,cache_write_1h,cache_read,est_usd,unpriced_messages
ABC-12,4,36,164,0,250,340,1003,0.0066,0
XYZ-3,2,90100,250,0,400,0,20300,0.0461,1
```

The total line, the attribution line, warnings and the estimate notice go to
stderr in CSV mode.

Line `attribution: ...`: how the messages of the report found their task
([../markers.md](../markers.md)). `by marker` were assigned by `kanjo mark`;
`by map` by the manual task map; `by heuristic` by the most frequent label in
the session's user messages; `unattributed` have task `-`. The counts follow the
selection (`--task`, `--since`) and are summed over projects with `--all`.

Warning `N messages attributed by heuristic, not by marker`: printed when that
count is above 0. The heuristic is a guess; markers are exact.

Warning `session <id> mentions several tasks; counted on one task`: that
session has messages without a marker, and its user messages named more than
one task label, but those messages are filed under one task. Markers split a
session per message.

When the database is missing or empty the exit code is 0 and the text
`no data; run kanjo snapshot` is printed: on stdout normally, on stderr with
`--csv` (stdout then holds only the CSV header, so it stays a valid CSV).

With `--all`, rows of every configured project are reported together
([../projects.md](../projects.md)); an unreadable project is named on stderr and
skipped, and with no `projects` the text `no projects; set projects in the user
config` is printed (exit 0).

`usage` opens the database read-only: it creates nothing and works on a
read-only data folder.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Report printed (or "no data") |
| 1 | Bad config, unreadable database, a stored row that fails validation |
| 2 | Bad option value (`--by`, `--task`, `--since`), unknown option, stray argument |

## Examples

```sh
kanjo usage
kanjo usage --by model
kanjo usage --by day --since 2026-11-01 --csv > usage-by-day.csv
kanjo usage --task ABC-12 --by session
kanjo usage --all
kanjo usage --all --by model --csv
```

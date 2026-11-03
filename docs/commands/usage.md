# kanjo usage

Reports saved usage and its estimated cost. Reads only the database written by
[snapshot](snapshot.md); it never reads transcripts and never creates the data
folder.

```
kanjo usage [--by task|model|day|session] [--task LABEL] [--since YYYY-MM-DD] [--csv] [--data-dir DIR]
```

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--by` | `task` | `task`, `model`, `day`, `session` | Group rows by this field. For `task`, a label is cut at its first `/` (`ABC-12/part` counts under `ABC-12`) |
| `--task` | all tasks | a task label such as `ABC-12` | Keep only rows of this task (compared after the same cut at `/`) |
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

The total line, warnings and the estimate notice go to stderr in CSV mode.

Warning `session <id> mentions several tasks; counted on one task`: that
session's messages named more than one task label but all its usage is filed
under one task (task markers, which would split it per message, are not built
yet).

When the database is missing or empty, stdout is `no data; run kanjo snapshot`
and the exit code is 0.

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
```

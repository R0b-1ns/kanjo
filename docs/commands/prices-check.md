# kanjo prices check

Checks the price file, offline. It downloads nothing and writes nothing.

```
kanjo prices check [--all] [--data-dir DIR]
```

## What it checks

1. The price file is valid ([../prices.md](../prices.md)): the embedded one, or `prices_file` from the config. An invalid file is an error (exit 1).
2. Its `fetched` date against the clock: warns when the file is **strictly older** than `prices.max_age_days` whole UTC days (default 30: 30 days old is fine, 31 warns). A date in the future also warns.
3. The saved data: every model with no price is listed with its message count.

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--all` | off | flag | Look for unpriced models in every project of `projects` (user config) instead of this folder. A project that cannot be read is an error, so "all priced" is never said by mistake |
| `--data-dir` | `data_dir` from the config | a folder path | Folder holding `kanjo.db`. A missing database is fine (nothing to check) |
| `-h` | | | Print the options and exit 0 |

`kanjo prices` needs the `check` subcommand (exit 2 otherwise). The age limit
is the config key `prices.max_age_days` ([../configuration.md](../configuration.md)).

## Output

```
prices: https://platform.claude.com/docs/en/about-claude/pricing, retrieved 2026-09-20
age: 44 days (limit 30)
warning: prices are 44 days old (limit 30); update the file
warning: model claude-unknown-9 has no price (1 messages)
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Nothing to report |
| 1 | Invalid price file, bad config, unreadable data |
| 2 | Missing subcommand, unknown option, stray argument |
| 3 | At least one warning was printed (old prices, future date, unpriced model) |

## Examples

```sh
kanjo prices check
kanjo prices check --all
kanjo prices check || echo "prices need attention (exit $?)"
```

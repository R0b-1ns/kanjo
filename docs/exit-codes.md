# Exit codes

| Code | Meaning | Used by |
|---|---|---|
| 0 | Success (also: nothing to do, "no data", up to date, `-h`) | all commands |
| 1 | Error: bad config, unreadable or unwritable file, database error, invalid data, network or checksum failure, port in use | `snapshot`, `usage`, `mark`, `prices check`, `serve`, `self-update`, `run`, `finish`, `summary`, `clean`, and any command when the home or working folder cannot be found |
| 2 | Usage error: unknown command, unknown option, bad option value, stray argument. A message is printed on stderr | all commands |
| 3 | `prices check` printed a warning (old prices, unpriced model) | `prices check` |

`mark` also uses 1 when nothing can be recorded: `end` with no active task, a clock behind the last marker, a full journal ([commands/mark.md](commands/mark.md)).

`run` exits with the verdict of the run: 0 for `ok-to-review` and `check-stray-files`, 124 for `timeout`, 1 for `failed`, `quota-exhausted` and `interrupted`; precondition errors (no commit, bad `--ref`, not a git working tree root) are 1 and option errors 2 ([commands/run.md](commands/run.md)). `finish` exits 1 for an unknown or still running session, a moved branch or another verdict already recorded ([commands/finish.md](commands/finish.md)). `summary` is 0 also when there is no run ([commands/summary.md](commands/summary.md)). `clean` is 0 when nothing is to be done or a run is skipped because it is alive, and 1 when a removal failed or was refused ([commands/clean.md](commands/clean.md)).

Running `kanjo` with no command prints the command list on stderr and exits 2.

Codes are per command. 124 is used by `run` (the agent ran out of time). Codes
reserved by [spec.md](spec.md) for later slices, not used yet: 3 for the
delegation commands (free quota exhausted; no clash with `prices check`, which
is another command; until then `run` exits 1 and the verdict is
`quota-exhausted`), 4 (spending cap exceeded), 5 (invalid agent return).

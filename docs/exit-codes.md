# Exit codes

| Code | Meaning | Used by |
|---|---|---|
| 0 | Success (also: nothing to do, "no data", up to date, `-h`) | all commands |
| 1 | Error: bad config, unreadable or unwritable file, database error, invalid data, network or checksum failure, port in use | `snapshot`, `usage`, `mark`, `prices check`, `serve`, `self-update`, and any command when the home or working folder cannot be found |
| 2 | Usage error: unknown command, unknown option, bad option value, stray argument. A message is printed on stderr | all commands |
| 3 | `prices check` printed a warning (old prices, unpriced model) | `prices check` |

`mark` also uses 1 when nothing can be recorded: `end` with no active task, a clock behind the last marker, a full journal ([commands/mark.md](commands/mark.md)).

Running `kanjo` with no command prints the command list on stderr and exits 2.

Codes are per command. Codes reserved by [spec.md](spec.md) for later slices,
not used yet: 3 for the delegation commands (free quota exhausted; no clash with
`prices check`, which is another command), 4 (spending cap exceeded), 5 (invalid agent return), 124
(timeout).

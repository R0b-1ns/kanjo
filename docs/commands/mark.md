# kanjo mark

Records which task you are working on, so that [snapshot](snapshot.md)
can attribute each Claude message to it. Events go to an append-only journal
in the data folder (table `markers` of `kanjo.db`, see
[../data-model.md](../data-model.md)). How markers are read, and the edge
cases, are in [../markers.md](../markers.md).

```
kanjo mark start <task> [--session ID] [--data-dir DIR]
kanjo mark end [--session ID] [--data-dir DIR]
kanjo mark current [--session ID] [--data-dir DIR]
```

## Subcommands

| Subcommand | Effect |
|---|---|
| `start <task>` | The task becomes the active one from now on. A task already active in the same scope is replaced. Starting the task that is already active writes nothing |
| `end` | The active task of the scope ends now. Fails when none is active in that scope |
| `current` | Prints the active task, or `no active task`. Opens the database read-only and creates nothing |

`<task>` follows the task label rules of [../data-model.md](../data-model.md):
`[A-Za-z0-9][A-Za-z0-9._/-]{0,63}`, no empty, `.` or `..` segment, and not `-`.
It may come before or after the options.

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--session` | none (global marker) | a Claude session id | Bind the marker to one session. A session marker wins over the global one for that session's messages. The id is not looked up in the transcripts |
| `--data-dir` | `data_dir` from the config | a folder path | Folder holding `kanjo.db` |
| `-h` | | | Print the options and exit 0 |

With `--session`, `current` prints the effective task of that session: its own
marker, else the global one.

## Output

| Case | Line on stdout |
|---|---|
| `start` recorded | `started ABC-12 (global) at 2026-11-03T12:00:00.000Z -> /work/app/.ai-usage/kanjo.db` |
| `start` of the active task | `ABC-12 is already the active task (global) since 2026-11-03T12:00:00.000Z -> /work/app/.ai-usage/kanjo.db` |
| `end` recorded | `ended the active task (session 4153a2d6) at 2026-11-03T12:30:00.000Z -> /work/app/.ai-usage/kanjo.db` |
| `current` | `ABC-12 since 2026-11-03T12:00:00.000Z (global)` or `no active task` |

Times are UTC, to the millisecond, from the system clock. The path after `->` is the journal file written. The default data folder is `.ai-usage` in the current folder, so a `mark` run from a sub-folder writes another journal than `snapshot` run at the root: check the path, or set `data_dir` in the user config ([../configuration.md](../configuration.md)).

## Exit codes

| Code | Case |
|---|---|
| 0 | Done, nothing to do (task already active), `no active task`, `-h` |
| 1 | `end` with no active task in the scope; the clock is behind the scope's last marker (the message gives that time); the journal holds `markers.max_events` events ([../configuration.md](../configuration.md)); a linked or unreadable database |
| 2 | Unknown subcommand, missing or invalid task, invalid `--session`, stray argument |

## Examples

```sh
kanjo mark start ABC-12                       # all sessions of this data folder
kanjo mark start ABC-12/review --session 4153a2d6
kanjo mark current
kanjo mark end
kanjo snapshot && kanjo usage                 # usage now follows the markers
```

A marker cannot be backdated or edited. To repair a marker that holds a time in
the future (a clock that was set wrong), delete the row by hand:

```sh
sqlite3 .ai-usage/kanjo.db "SELECT id, at, kind, session, task FROM markers ORDER BY id DESC LIMIT 5"
sqlite3 .ai-usage/kanjo.db "DELETE FROM markers WHERE id = 42"
```

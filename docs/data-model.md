# Data model

## Usage row

One row per `(source, session, day, task, model, long_context)`.

| Field | Type | Allowed values | Meaning |
|---|---|---|---|
| `source` | text | `[a-z][a-z0-9-]{0,31}` | Where the row comes from. `claude` for transcripts |
| `session` | text | `[A-Za-z0-9_][A-Za-z0-9_-]{0,127}` (no leading `-`, so a CSV cell never looks like a formula) | Session identifier |
| `day` | text | real date `YYYY-MM-DD`, **UTC** day of the message timestamp | |
| `task` | text | `-`, or `[A-Za-z0-9][A-Za-z0-9._/-]{0,63}` with no empty, `.` or `..` segment | Task label. `-` means no task. Safe to use in a path |
| `model` | text | `[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}` | Exact model id. `<synthetic>` is never stored |
| `long_context` | 0 or 1 | | 1 when the message's prompt (input + cache read + cache writes) exceeded the model's long-context threshold in the price file. Selects the price tier |
| `messages` | integer | 0 or more | Messages counted |
| `input`, `output`, `reasoning` | integer | 0 or more | Tokens. For Claude `reasoning` is 0: thinking tokens are inside `output` |
| `cache_write_5m`, `cache_write_1h`, `cache_read` | integer | 0 or more | Cache tokens |
| `cost_reported_usd` | real or NULL | finite, 0 or more | Cost given by the source. NULL for Claude (the cost is estimated); 0 means free, not missing |
| `marked` | integer | 0 to `messages` | How many of the row's messages were given this task by a marker ([markers.md](markers.md)). The other messages were attributed by the session's fallback (`sessions.task_source`). Not part of the key |

Anything outside these rules is rejected at the boundary; a stored row that
fails them is an error when read.

## Session

One line per `(source, session)` saying how its task was found.

| Field | Values |
|---|---|
| `task_source` | `map` (manual task map), `heuristic` (label found in user messages), `none` |
| `labels_seen` | Number of distinct task labels the session's user messages named. Above 1 triggers the "counted on one task" warning in `usage` |

## Data folder

Default `./.ai-usage/` (`data_dir`):

| File | Mode | Content |
|---|---|---|
| `kanjo.db` | 0600 | SQLite database |
| `.gitignore` | 0644 | `*` followed by a newline, so the folder is never committed. An existing file is kept only if it still ignores everything |
| `claude-tasks.tsv` | yours | Optional manual task map |

The folder is created with mode 0700. Keep it out of version control anyway:
`.ai-usage/` is in this repository's `.gitignore`.

Safety checks when opening the folder (a cloned repository could ship files in it):

- `kanjo.db` and `.gitignore` must not be symbolic links (kanjo refuses, naming the path).
- An existing `.gitignore` must contain a `*` line and no `!` rule, otherwise kanjo refuses, because the database could then be committed.
- The database is opened with `trusted_schema` off, so views or triggers inside a foreign `kanjo.db` cannot call unsafe functions.
- `kanjo usage` opens the database read-only and creates nothing.

## Marker event

One row per `kanjo mark` event ([markers.md](markers.md)), append only.

| Field | Type | Allowed values | Meaning |
|---|---|---|---|
| `id` | integer | assigned, never reused | Orders events with the same time |
| `at` | text | UTC `YYYY-MM-DDTHH:MM:SS.mmmZ` | When the command ran, from the system clock |
| `kind` | text | `start`, `end` | |
| `session` | text | empty (global) or a valid session id | The scope of the event |
| `task` | text or NULL | a valid task label other than `-` for `start`, NULL for `end` | |

There is no free-text field. A stored event that fails these rules is an error when read.

## SQLite schema (`PRAGMA user_version = 2`)

A database with a higher `user_version` is refused with "update kanjo".

```sql
CREATE TABLE usage(
  source TEXT NOT NULL, session TEXT NOT NULL, day TEXT NOT NULL, task TEXT NOT NULL,
  model TEXT NOT NULL, long_context INTEGER NOT NULL,
  messages INTEGER NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
  reasoning INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL,
  cache_read INTEGER NOT NULL, cost_reported_usd REAL, marked INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(source, session, day, task, model, long_context));
CREATE TABLE sessions(
  source TEXT NOT NULL, session TEXT NOT NULL, task_source TEXT NOT NULL, labels_seen INTEGER NOT NULL,
  PRIMARY KEY(source, session));
CREATE TABLE snapshots(
  at TEXT NOT NULL, sessions INTEGER NOT NULL, rows INTEGER NOT NULL, skipped INTEGER NOT NULL);
CREATE TABLE markers(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('start','end')),
  session TEXT NOT NULL,
  task TEXT,
  CHECK((kind = 'start') = (task IS NOT NULL)));
CREATE INDEX markers_scope ON markers(session, at, id);
```

Version 1 databases (before markers) are upgraded in place the first time a
command that writes opens them (`snapshot`, `mark start`, `mark end`): the
`markers` table is created and `usage.marked` added with 0. Commands that only
read (`usage`, `serve`, `mark current`) read a version 1 database as it is,
without changing it, and see no markers.

`snapshots.at` is the RFC 3339 UTC time of a `snapshot` run. Usage rows carry
no timestamp, so repeating a snapshot leaves them identical.

## Merge rule

The unit of replacement is the **slot**: `(source, session, day, model,
long_context)`, as a set of rows. One slot may hold several tasks, since
markers can split a session. For each slot present in the incoming data,
`snapshot` compares the messages of the stored rows (total) with those of the
incoming rows (total):

- **Stored total larger**: the stored rows are kept and the incoming ones dropped. When only part of a session's files were purged, the saved history does not shrink, and a marker added later does not apply to that slot.
- **Otherwise**: the stored rows of the slot are replaced by the incoming rows. This is what stops a changed task label, or a new marker, from double counting.

Every other stored row is kept too, because Claude Code purges old transcripts
and the database must keep what it already saved. To start over, delete
`kanjo.db`.

## Reading the data without kanjo

```sh
sqlite3 .ai-usage/kanjo.db "SELECT task, SUM(output) FROM usage GROUP BY task"
```

## What is never stored

Prompts, answers, code, file contents, paths of your files, and message text.
Only counts, model names, identifiers, task labels, dates and marker times.

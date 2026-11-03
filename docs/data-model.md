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
| `.gitignore` | 0644 | `*` followed by a newline, so the folder is never committed. An existing file is left alone |
| `claude-tasks.tsv` | yours | Optional manual task map |

The folder is created with mode 0700. Keep it out of version control anyway:
`.ai-usage/` is in this repository's `.gitignore`.

## SQLite schema (`PRAGMA user_version = 1`)

A database with a higher `user_version` is refused with "update kanjo".

```sql
CREATE TABLE usage(
  source TEXT NOT NULL, session TEXT NOT NULL, day TEXT NOT NULL, task TEXT NOT NULL,
  model TEXT NOT NULL, long_context INTEGER NOT NULL,
  messages INTEGER NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
  reasoning INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL,
  cache_read INTEGER NOT NULL, cost_reported_usd REAL,
  PRIMARY KEY(source, session, day, task, model, long_context));
CREATE TABLE sessions(
  source TEXT NOT NULL, session TEXT NOT NULL, task_source TEXT NOT NULL, labels_seen INTEGER NOT NULL,
  PRIMARY KEY(source, session));
CREATE TABLE snapshots(
  at TEXT NOT NULL, sessions INTEGER NOT NULL, rows INTEGER NOT NULL, skipped INTEGER NOT NULL);
```

`snapshots.at` is the RFC 3339 UTC time of a `snapshot` run. Usage rows carry
no timestamp, so repeating a snapshot leaves them identical.

## Merge rule

`snapshot` replaces a stored row when an incoming row has the same
`(source, session, day, model, long_context)`, whatever its task; this is what
stops a changed task label from double counting. **If the stored row counts
more `messages` than the incoming one, the stored row is kept** and the
incoming one is dropped: when only part of a session's files were purged, the
saved history does not shrink. Every other stored row is kept too, because
Claude Code purges old transcripts and the database must keep what it already
saved. To start over, delete `kanjo.db`.

## Reading the data without kanjo

```sh
sqlite3 .ai-usage/kanjo.db "SELECT task, SUM(output) FROM usage GROUP BY task"
```

## What is never stored

Prompts, answers, code, file contents, paths of your files, and message text.
Only counts, model names, identifiers, task labels and dates.

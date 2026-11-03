# Slice 3 design: task markers

Source of truth: `docs/spec.md` sections 6, 8 and 15. This slice adds
`kanjo mark start|end|current`, an append-only journal of marker events, and a
per-message task attribution for Claude usage that prefers markers.

## Goals

- `mark start <task>`, `mark end`, `mark current`: dated events in a journal.
- Each Claude message is attributed to the task active at its timestamp.
- Priority: session-bound marker, then global marker, then the manual map
  (`task.map_file`), then the heuristic (`task.pattern`, `task.ignore`), then `-`.
- `usage` and the page say how many messages were attributed by marker and how
  many by heuristic.
- `snapshot` stays idempotent.
- The journal reads time from `core.Clock` (system clock in production, fixed
  clock in tests).

## Non-goals

Backdating (`--at`, `--ago`) and editing or deleting a marker; a `--note`
(decision below); reading a session id from the environment; markers for
junior runs; the run and lead journals of section 8; delegation, opencode,
JSON returns, routing, caps, sandbox, batch, cost per accepted task,
automatic price update. All stay `open`.

## Decisions

Given by the user: the journal is a SQLite table (spec section 8 and
`docs/data-model.md` change), the core knows no tool (journal behind an
injected port, with a fake and a contract test), labels pass `core.ValidTask`,
new config keys are user-only, the repository allow-list stays closed.

My calls, to review:

1. **No `--note` in this slice.** Nothing would read it, and free text is where
   a pasted secret ends up in stored data, against the rule "no content in the
   data". A later slice can add a bounded column with one additive migration.
   Spec section 15 is amended to say so.
2. **Table named `markers`, not `journal`.** Runs and leads of section 8 have
   other columns; they get their own tables later.
3. **Same database file** (`kanjo.db`, schema version 2), so the existing folder
   protections apply: mode 0600, links refused, ignore file, untrusted schema.
4. **No backdating.** A marker is stamped by the clock when the command runs.
   This keeps each timeline ordered without insertion rules. The cost: a
   forgotten `mark start` cannot be repaired (see risks).
5. **Scope is the data folder.** "Global" means global within one `data_dir`.
   With the default per-project folder, markers are per project; a shared
   `data_dir` in the user config shares them.

### Data model

```sql
CREATE TABLE markers(
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  at      TEXT NOT NULL,                 -- UTC, 2006-01-02T15:04:05.000Z
  kind    TEXT NOT NULL CHECK(kind IN ('start','end')),
  session TEXT NOT NULL,                 -- '' = global, else a valid session id
  task    TEXT,                          -- a valid task label for start, NULL for end
  CHECK((kind = 'start') = (task IS NOT NULL)));
CREATE INDEX markers_scope ON markers(session, at, id);
ALTER TABLE usage ADD COLUMN marked INTEGER NOT NULL DEFAULT 0;
```

- A task is one `start` (with its label) followed by the next event of the same
  scope. The interval is `[start, next event)`: a message stamped exactly at
  the start belongs to the new task, one stamped exactly at an end does not.
- A `start` without `end` is simply the last event: the task is still active.
- A scope is a session id, or `''` for global. There are two kinds of
  timeline: one per session id, and the global one.
- Ordering is by `(at, id)`; `id` never repeats (`AUTOINCREMENT`).
- `usage.marked` is the number of the row's messages attributed by a marker
  (0 to `messages`). It is **stored**, not computed: transcripts are purged, so
  the attribution of old rows cannot be rebuilt. It is not part of the key.
- The rest of a row's messages (`messages - marked`) were attributed by the
  fallback recorded in `sessions.task_source` (`map`, `heuristic`, `none`).
  This is approximate for a session whose fallback changed between snapshots;
  chosen to keep the migration additive (no key change, no table copy).

### Attribution of one message

`TaskAt(session, t)`: take the session's own timeline, the latest event with
`at <= t`; if it is a `start`, that task wins. Otherwise (no event, or an
`end`) take the global timeline the same way. Otherwise the existing session
level rules apply (map, heuristic, `-`). Sub-agent messages belong to their
session and follow the same rule.

### Edge cases

| Case | Behaviour |
|---|---|
| Two `start` without `end` in a scope | The second supersedes the first (it ends it at that instant). Nothing is refused |
| `start` of the task already active in that scope | Nothing is written, message says so, exit 0 (no journal growth, safe to script) |
| `end` with nothing active in the scope | Nothing written, message on stderr, exit 1 |
| `end --session S` while only a global task is active | Same: exit 1. Ending the global task needs `mark end` without `--session` |
| Clock behind the scope's last event | Refused, exit 1, the message gives the last event time. No clamping: invented times would corrupt the timeline |
| Marker in the future | Cannot be written (no `--at`). A stored future event (clock moved back, edited file) blocks writes to its scope until the clock reaches it, is ignored by `mark current` (evaluated at now), and affects only messages stamped after it |
| Unknown session id | Accepted if the id is well formed: the transcript may not be read yet. It matches nothing until a session of that id appears. Not verified against transcripts, on purpose |
| Messages before the first marker, or between an end and the next start | Fall back to map, heuristic, `-` |
| Session-bound `end` | Removes the session override; the global timeline applies again for that session |
| Journal at `markers.max_events` | `start`/`end` refused, exit 1, message names the key |

### Recompute and idempotence

`snapshot` reads the journal once, builds the timeline, and attributes every
message it still finds. The result is a pure function of transcripts, journal,
map and config, so a second run with unchanged inputs writes identical rows.

Marker added after the fact: the next snapshot re-attributes the messages that
are still in the transcripts. For this the merge rule changes, because one
`(session, day, model, long_context)` slot can now hold several tasks:

> The unit of replacement is the **slot** (`source, session, day, model,
> long_context`) as a set of rows. If the stored rows of the slot count more
> messages in total than the incoming rows of that slot, the stored rows are
> kept and the incoming ones dropped. Otherwise the stored rows of the slot are
> replaced by the incoming rows. Slots the source no longer has are kept.

The key `(source, session, day, task, model, long_context)` does not change.
The rule "never delete what the transcripts no longer contain" holds. Limit,
accepted: if Claude has purged part of a slot, the stored slot is larger than
the incoming one, so a marker added later is not applied to that slot (stored
attribution stays). Re-attribution is guaranteed only while the whole slot is
still in the transcripts.

### Reporting heuristic attribution

`core.Aggregate` computes `Attribution{Marker, Map, Heuristic, None}` message
counts over the rows that match the query: `marked` goes to Marker, and
`messages - marked` goes to the session's `task_source` (a session without a
session record counts as None). The multi-label warning now fires only for a
session that has unmarked messages in the selected rows.

- `usage` (text): one extra line after the totals, e.g.
  `attribution: 120 by marker, 40 by heuristic, 5 by map, 3 unattributed`, and
  the existing multi-task warning. CSV output is unchanged (stdout stays a
  fixed table).
- `/api/data`: a new `attribution` object with the four counts; the page shows
  one line under the totals, and a warning when the heuristic count is above 0.
- `--all`: counts are summed over the projects.

### Commands

```
kanjo mark start <task> [--session ID] [--data-dir DIR]
kanjo mark end [--session ID] [--data-dir DIR]
kanjo mark current [--session ID] [--data-dir DIR]
```

| Item | Value |
|---|---|
| `start` / `end` output | One line on stdout: what was recorded or why nothing was (`task`, `session`, time) |
| `current` | Without `--session`: the global task. With it: the effective task of that session (its own marker, else the global one). Prints `no active task` and exits 0 when none. Opens the database read-only; creates nothing |
| Exit codes | 0 ok or nothing to do; 1 refused state or database error; 2 bad label, bad session id, wrong arguments |
| Config | `markers.max_events`, integer, default 100000, greater than 0, **user config only** |

### Concurrency

`start` and `end` read the scope's events and append in one exclusive
(`IMMEDIATE`) transaction, behind the port
`Journal.Update(ctx, scope, fn)`: `fn` receives the scope's events and
returns the event to append or an error. Two simultaneous `mark` calls
serialize on the SQLite write lock (busy timeout 5 s); none is lost and the
rules above apply to the second one against the state left by the first.

## Threats and abuse cases (written before the code)

| Threat | Answer | Test (planned) |
|---|---|---|
| Hostile label (traversal, formula, huge text, control characters) | `core.ValidTask` before any use, also on read from the database; a start cannot carry `-`; usage error exit 2; labels never echoed unprinted | `core.TestMarkerValidate`, `cli.TestMarkStartRejectsBadLabel`, `sqlitestore.TestHostileMarkerValuesStayData` |
| Hostile session id | `core.ValidSession`; `''` is the only global value | same tests |
| Journal grown without bound | `markers.max_events` checked inside the same transaction; each event is a few dozen bytes; same-task `start` writes nothing | `core.TestStartRefusedAtTheCap`, `cli.TestMarkStartAtTheCapExits1`, `cli.TestRepeatedStartWritesOnce` |
| Marker written by a cloned repository | A repository cannot add events: `kanjo.toml` has no marker key, and `markers.max_events` is refused in it. A committed `.ai-usage/kanjo.db` is the existing residual risk (a tracked database stays tracked); markers in it could mislabel usage, never run code, and labels are validated on read | `config.TestRepoConfigMayNotSetMarkers`, `sqlitestore.TestTamperedMarkerRowFailsTheRead` |
| Data file replaced by a link | `kanjo.db` and the folder go through the existing `Open`/`OpenReadOnly` checks; `mark` uses no other file | `sqlitestore.TestOpenRefusesSymlinkedFiles` (existing), `cli.TestMarkRefusesALinkedDatabase` |
| Two `mark` at once | Exclusive transaction around read and append | `kanjotest` journal contract "concurrent starts are all kept in order", `sqlitestore` run of it with goroutines |
| Secret or content in the journal | No free text exists: only a clock time, a validated label and a validated session id. No `--note` | `cli.TestCanaryNeverInOutputOrDatabase` (extended with a marker run), `cli.TestMarkStoresNoPathOrText` |
| Clock moved back (or forward) to corrupt a timeline | Writes refused behind the last event; reads order by `(at, id)`; a future event is inert for earlier messages | `core.TestStartRefusedWhenClockIsBehind`, `core.TestFutureEventDoesNotAffectEarlierMessages` |
| Marker steals another session's usage | A session-bound marker matches only its own session id; the global one is the only one that crosses sessions, by design | `core.TestSessionMarkerOnlyAffectsItsSession` |
| Terminal escapes in marker output | Everything printed goes through `printable` | `cli.TestMarkOutputCannotCarryEscapes` |
| Page and CLI disagree on attribution | Both use `core.Aggregate` | `cli.TestPageAndCLIGiveTheSameAttribution` |
| Old database opened by a new kanjo, or the reverse | v1 migrates to v2 by additive DDL; a read-only open of v1 still works; a higher version is refused ("update kanjo") | `sqlitestore.TestMigratesVersion1`, `sqlitestore.TestOpenReadOnlyReadsVersion1`, existing newer-version test |

## Risks and open points

- A forgotten `mark start` cannot be repaired afterwards (no backdating).
  Recommended next step if it hurts: `--ago 20m`, which needs an ordered
  insert rule.
- The "approximate fallback split" of unmarked messages (above).
- Re-attribution stops once a slot is partly purged (above).
- A far-future stored event blocks marking in its scope; recovery is deleting
  the row with `sqlite3` (documented), since the CLI is append-only.
- The session id must be passed by hand with `--session`; I did not verify
  that Claude Code exposes it in the environment of a command.

## Documentation (one topic per file)

| File | Change |
|---|---|
| `docs/commands/mark.md` | New: the three subcommands, flags, output, exit codes, examples |
| `docs/markers.md` | New: scopes, interval rule, priority, edge-case table, recompute and its limit |
| `docs/data-model.md` | Schema version 2, `markers` table, `usage.marked`, slot-set merge rule |
| `docs/claude-transcripts.md` | Task attribution list gains the marker levels; remove "planned, not built" |
| `docs/configuration.md` | `markers.max_events`; repository allow-list row |
| `docs/commands/usage.md`, `docs/commands/serve.md` | Attribution line and `attribution` field |
| `docs/security.md`, `docs/exit-codes.md`, `docs/README.md` | Threat rows with tests, `mark` codes, index entries |
| `docs/spec.md` | Section 8 (journal is a table), section 15 (no `--note`, session option, scope) |

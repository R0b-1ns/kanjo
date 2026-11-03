# kanjo snapshot

Reads Claude Code transcripts and merges their usage into the local database.
Idempotent: running it twice in a row changes nothing. Run it at the end of
each task, because Claude Code deletes old transcripts after a few weeks and
the database keeps what was saved.

```
kanjo snapshot [--data-dir DIR]
```

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--data-dir` | `data_dir` from the config (`./.ai-usage`) | a folder path (relative paths resolve against the current folder) | Folder holding `kanjo.db`. Beats every config file. Created (mode 0700) if missing |
| `-h` | | | Print the options and exit 0 without doing anything |

No positional arguments are accepted.

## What it does

1. Loads the configuration ([../configuration.md](../configuration.md)).
2. Reads the price file only to learn each model's long-context threshold.
3. Reads the manual task map `task.map_file` if it exists ([../claude-transcripts.md](../claude-transcripts.md)) and the marker journal ([../markers.md](../markers.md)). A stored marker that fails validation stops the snapshot (exit 1, nothing written).
4. Reads every transcript under `claude.projects_dir` whose project folder name equals one of `claude.project_prefixes` or starts with it followed by `-`.
5. Validates every row. If any row is invalid nothing is written (exit 1).
6. In one transaction: for each slot (session, day, model, long-context flag) in the incoming rows, replaces the stored rows of the slot, whatever their tasks, **unless the stored rows count more messages in total**, in which case they are kept and the incoming rows dropped; records how each session's task was found; appends one line to the `snapshots` table. Stored rows that the transcripts no longer have are kept. Saved usage therefore never shrinks when Claude Code purges old files.

A session with no readable usage rows is not recorded, so a damaged or
emptied transcript cannot erase saved history.

## Output

stdout, one line:

```
snapshot: <sessions> sessions, <rows> rows, <skipped> lines skipped -> <path to kanjo.db>
```

stderr, only when `<skipped>` is above 0:

```
warning: <skipped> transcript lines skipped (unreadable or invalid)
```

"Skipped" counts transcript lines that could not be used (not JSON, truncated,
longer than `claude.max_line_bytes`, no valid timestamp, invalid model id,
negative token counts), plus files or folders that could not be read and files
whose session id is not valid. It is never fatal.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Done (also when there are no transcripts: `0 sessions, 0 rows`) |
| 1 | Bad config, unreadable task map, data folder not writable, invalid row from the source, database error |
| 2 | Bad option or stray argument |

## Examples

```sh
kanjo snapshot
kanjo snapshot --data-dir /tmp/usage-test
```

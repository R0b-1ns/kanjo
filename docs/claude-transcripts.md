# Claude Code transcripts

How `kanjo snapshot` reads usage from Claude Code's local transcripts.

## Where

```
<claude.projects_dir>/<project folder>/<session>.jsonl
<claude.projects_dir>/<project folder>/<session>/subagents/*.jsonl
```

A project folder is read when its name starts with one of
`claude.project_prefixes` ([configuration.md](configuration.md)). Sub-agent
files belong to their parent session. If the same session id appears under two
matching project folders it is one session. Only regular `.jsonl` files and
real folders are read: symbolic links are never followed.

Within a session, main files are read before sub-agent files, and the last
copy of a message wins.

## Which lines count

A line counts only if it is a JSON object with `type` equal to `assistant` and
a `message` that has both `id` and `usage`. Everything else is ignored (it is
not counted as skipped): other line types, assistant lines without usage or
id, and the model `<synthetic>`.

| Rule | Detail |
|---|---|
| Duplicates | One message appears on several lines (one per content block). Messages are deduplicated by `message.id` across all files of the session; the last copy read wins |
| Tokens | `input_tokens`, `output_tokens` (thinking included), `cache_read_input_tokens` |
| Cache writes | `cache_creation.ephemeral_5m_input_tokens` and `ephemeral_1h_input_tokens`. If `cache_creation` is absent, `cache_creation_input_tokens` is counted as 5-minute and 1-hour is 0 |
| Day | The message `timestamp` converted to UTC, as `YYYY-MM-DD` |
| Long context | Decided per message from its prompt size and the price file ([prices.md](prices.md)) |
| Unknown fields | Ignored |

## What is skipped (and counted)

A skipped item adds 1 to the "lines skipped" count of `snapshot`. Skipped:

- a line that is not valid JSON (including a truncated last line of a file still being written),
- a line longer than `claude.max_line_bytes`,
- an assistant message with a missing or unparseable timestamp, an invalid model id, or a negative token count,
- a file or folder that cannot be read,
- a file whose session id (its name without `.jsonl`) is not a valid session id.

A session whose files produce no usable row is not recorded at all, so it
cannot overwrite what was saved earlier.

## Task attribution

Every row of a session gets one task, chosen in this order:

1. **Manual map** (`task.map_file`, default `<data_dir>/claude-tasks.tsv`): the longest session-id prefix that matches. `task_source` is `map`.
2. **Heuristic**: the most frequent label matching `task.pattern` in the session's own user messages (ties go to the alphabetically first label). `task_source` is `heuristic`.
3. Otherwise `-`, `task_source` `none`.

Only text typed by the user counts for the heuristic: messages of sidechains
(sub-agents), tool results, and assistant text are ignored. The text is
scanned in memory; only the matched labels are kept.

Manual map file format, one entry per line, tab-separated; blank lines and
lines starting with `#` are ignored; an error names the line number:

```
# <session id prefix><TAB><task>
4153a2d6	ABC-12
```

A session id prefix is `[A-Za-z0-9_-]+`; a task follows the `task` rules of
[data-model.md](data-model.md) and may not be `-`. A prefix may appear once.

## Known limits

- A session that works on several tasks is counted entirely on one task (the map or the most frequent label). `usage` warns when a session named more than one label. Task markers that split a session per message are planned, not built.
- Claude's cost is an estimate at API list prices, not a subscription bill.
- The transcript format can change; unknown fields are ignored and unreadable lines are skipped and counted rather than failing.

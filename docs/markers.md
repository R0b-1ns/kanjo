# Task markers

A marker says "from now on, this is the task". `snapshot` uses the markers to
give each Claude message the task that was active when the message was written.
Commands: [commands/mark.md](commands/mark.md).

## Events

A journal of events, one row each, never edited ([data-model.md](data-model.md)):

| Field | Meaning |
|---|---|
| `kind` | `start` (with a task label) or `end` |
| `at` | UTC time to the millisecond, from the system clock |
| `session` | `''` for a global marker, else the Claude session id it is bound to |

A scope is one session id, or the global scope. A task runs from a `start`
to the next event of the same scope. A `start` with no later event is still
active. The interval includes its start instant and excludes its end instant.
The journal belongs to one data folder: with the default per-project folder,
markers are per project.

## Which task a message gets

Each message, by its own timestamp, in order:

1. The task of the message's session, if a session-bound marker is active.
2. The task of the global scope, if a global marker is active.
3. The manual map (`task.map_file`).
4. The heuristic (`task.pattern`, `task.ignore`).
5. `-`.

Levels 3 to 5 are decided once per session, as before
([claude-transcripts.md](claude-transcripts.md)). Sub-agent messages follow their session.

## Edge cases

| Case | Behaviour |
|---|---|
| Two `start` without an `end` | The second replaces the first from its instant |
| `start` of the task already active in that scope | Nothing written, exit 0 |
| `end` with nothing active in that scope | Nothing written, exit 1. `end --session S` does not end the global task |
| Clock behind the scope's last event | Refused, exit 1. Markers are never given an invented time |
| Marker time in the future (clock moved back, edited file) | Cannot be written. If present, `current` ignores it and it only affects messages stamped after it. It blocks new events in its scope until the clock reaches it, or you delete the row ([commands/mark.md](commands/mark.md)) |
| Unknown session id | Accepted if well formed; matches nothing until a session of that id exists |
| Message before the first marker, or after an `end` | Falls to the map, the heuristic, `-` |
| Session-bound `end` | The global task applies again to that session |

There is no backdating: a marker is stamped when you run the command.

## Recompute

`snapshot` reads transcripts and markers together; the result depends only on
them, the map and the configuration, so repeating it changes nothing.

When you add a marker later, the next `snapshot` re-attributes the messages
still present in the transcripts. A day-model slot (`session`, `day`, `model`,
long-context tier) of a session is replaced as a whole; if Claude has already
purged part of that slot, the saved slot is larger than what the transcripts
now give, so it is kept and the new marker does not apply to it. Saved usage
never shrinks ([data-model.md](data-model.md)).

## Reading the attribution

`usage` and the page report how many messages were attributed by marker, map,
heuristic or not at all ([commands/usage.md](commands/usage.md)). The unmarked
messages of a session count under the fallback recorded for the session at the
last `snapshot` that saw it.

## What is stored

Only a time, `start` or `end`, a validated task label and a validated session
id. There is no note or other free text. See [security.md](security.md).

# kanjo clean

Lists, and with `--yes` removes, the worktrees and branches left by
[runs](run.md) that have no verdict from you, and records those runs as
`abandoned`. It only touches what the run journal recorded: a branch or a folder
you made yourself, even one named `delegate/...`, is never listed or removed.

```
kanjo clean [--yes] [--repo DIR] [--data-dir DIR]
```

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--yes` | off | | Remove what is listed. Without it the command is read-only: it opens the database read-only, reads git, and creates nothing |
| `--repo` | the current folder | a git working tree root | The repository the runs were made in |
| `--data-dir` | `data_dir` from the config | a folder path | Folder holding `kanjo.db` and `worktrees/` |
| `-h` | | | Print the options and exit 0 |

## Output

Without `--yes`, one line per run without a verdict:

```
<session> <task> <state> <worktree folder> <branch>
```

`<task>` is `-` for a run without a task. `<state>` is `finished` (ended,
waiting for your verdict), `crashed` (recorded as running but its process is
gone, it is completed as `interrupted` first) or `running` (its lock is held:
it is alive and is never touched, even with `--yes`; a row younger than 10 seconds is also listed as `running`, because its process may be about to take the lock).

With `--yes`, one line per run: `removed <session>`, `skipped (running)
<session>` or `failed <session>: <reason>`. A failure (for example a branch that
moved since the run ended, which is never removed) does not stop the others.
With nothing to do it prints `nothing to clean`.

## Exit codes

| Code | Case |
|---|---|
| 0 | Done, nothing to clean, runs skipped because they are alive, `-h` |
| 1 | A removal failed or was refused; a linked or unreadable database; a git problem |
| 2 | Stray argument or invalid option |

Two `clean --yes` at once are safe: the verdict is recorded once per run and
the second sees it as done.

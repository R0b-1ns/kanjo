# kanjo summary

Totals of the run journal: how many runs, how long, how many retries, and how
they ended according to the script and according to you. Text only. It opens the
database read-only and creates nothing.

```
kanjo summary [--task LABEL] [--since YYYY-MM-DD] [--data-dir DIR]
```

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--task` | all | a task label | Only runs of this task |
| `--since` | all | a date `YYYY-MM-DD`, UTC | Only runs started on or after that day |
| `--data-dir` | `data_dir` from the config | a folder path | Folder holding `kanjo.db` |
| `-h` | | | Print the options and exit 0 |

## Output

```
runs  3
tasks  1
runs without a task  1
launches  4
retries  1
duration  10s
script verdicts
  ok-to-review  1
  failed  1
  timeout  1
lead verdicts
  accepted  1
  rejected  1
  no lead verdict  1
```

`tasks` counts distinct labels; runs started without `--task` are counted on
their own line. `script verdicts` are the verdicts of [run](run.md) (see
[../delegation.md](../delegation.md)), `lead verdicts` those you gave with
[finish](finish.md) or [clean](clean.md) (`abandoned`). A run still `running`
shows under the script verdicts. Counts of zero are not listed. With no
database, a database from before schema version 3, or no run matching, it prints
`no runs`.

## Exit codes

| Code | Case |
|---|---|
| 0 | Done, including `no runs`; `-h` |
| 1 | A linked or unreadable database; a stored run that fails validation |
| 2 | Invalid `--task` or `--since`, stray argument |

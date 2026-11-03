# kanjo finish

Records your verdict on a [run](run.md), removes its worktree and its branch,
and shows the command that applies the patch. It then takes a Claude snapshot
and prints the totals of the run's task.

```
kanjo finish <session> --verdict accepted|modified|rejected [--repo DIR] [--data-dir DIR]
```

`<session>` is the id printed by `kanjo run` (`ses_20261103T120000_a1b2c3`). It
may come before or after the options. Only runs of the journal in the data
folder are known: a folder or a branch the journal does not name is never
touched.

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--verdict` | none (required) | `accepted`, `modified`, `rejected` | Your decision. `abandoned` is written by [clean](clean.md) only and is refused here |
| `--repo` | the current folder | a git working tree root | The repository the run was made in |
| `--data-dir` | `data_dir` from the config | a folder path | Folder holding `kanjo.db`, `worktrees/` and `runs/` |
| `-h` | | | Print the options and exit 0 |

## What it does, in this order

1. Finds the run in the journal. Unknown: exit 1, nothing removed.
2. A run recorded as running whose lock is free (its process died) is completed
   as `interrupted`. A run whose lock is held is alive: exit 1, wait for it.
3. Removes the worktree, then its branch, only if the branch still points to the
   commit recorded at the end of the run. If it moved (a commit was made on
   it), nothing is removed and no verdict is recorded: resolve it with git, then
   run `finish` again. There is no `--force`.
4. Records the verdict. It cannot be changed afterwards.
5. Prints the result, the apply command, the snapshot line and the task totals.

Running it again is safe: the same verdict prints `already recorded` and exits
0; another verdict exits 1 and changes nothing; if the worktree is already gone
it says so and still records the verdict (this repairs a `finish` that stopped
between removing and recording).

The run's files in `<data_dir>/runs/<session>/` (report, patch, logs) are kept.

## Output

```
finish: ses_20261103T120000_a1b2c3 accepted
workspace removed
apply: git -C '/work/app' apply '/work/app/.ai-usage/runs/ses_20261103T120000_a1b2c3/changes.patch'
snapshot: 2 sessions, 6 rows, 0 lines skipped -> /work/app/.ai-usage/kanjo.db
task  messages  input  ...
```

The `apply:` line is not printed for `rejected`. The task totals are the output
of `kanjo usage --task <task>`, and are skipped for a run without a task. A
snapshot that fails prints `warning: snapshot failed: ...` on stderr and the
exit code stays 0, because the verdict is already recorded.

## Exit codes

| Code | Case |
|---|---|
| 0 | Verdict recorded, or already recorded with the same verdict; `-h` |
| 1 | Unknown session; the run is still running; the branch moved; another verdict is already recorded; the folder is not a worktree kanjo created; a database or git problem |
| 2 | Missing or invalid session or verdict, `abandoned`, stray argument |

## Example

```sh
kanjo run --agent script --script ./agent.sh --model m --prompt-file p.txt --task ABC-12
git apply .ai-usage/runs/ses_20261103T120000_a1b2c3/changes.patch   # if you want the change
kanjo finish ses_20261103T120000_a1b2c3 --verdict accepted
```

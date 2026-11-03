# Delegation

`kanjo run` hands one task to an agent in a disposable git worktree, checks the
result and writes a report. `kanjo finish` records your verdict and cleans up.
`kanjo summary` totals the journal. `kanjo clean` removes what a run left when
you never gave a verdict. Commands: [run](commands/run.md),
[finish](commands/finish.md), [summary](commands/summary.md),
[clean](commands/clean.md). Design source: [spec.md](spec.md) sections 5, 8, 10,
11 and 12.

**No sandbox, no model yet.** The agent, `--setup`, `--test` and `--check` run
with your rights and can read or change anything you can, including the
repository's `.git`. The only agent today is `script`, a test aid that runs a
script you wrote. The tool hardens its own git calls but cannot undo what the
agent does. Use it only with scripts you wrote.

The tool never commits, pushes or applies anything.

## Lifecycle

```
run:    validate options -> resolve --ref -> record the run (running)
        -> create the worktree (takes the lock) -> mark it created
        -> open the box -> --setup -> read the state before
        -> agent [-> one retry] -> read the state after
        -> judge the changes -> write changes.patch -> --test, --check
        -> verdict -> report files -> usage rows -> complete the run -> release the lock
finish: complete a crashed run -> remove the worktree and branch -> record the verdict
        -> snapshot -> task totals
clean:  complete a crashed run -> (--yes) remove -> record abandoned
```

The run is recorded **before** anything is created, so a crash never leaves a
worktree that no row can name. A completed row is never changed again.

## Verdicts

| Verdict (journal) | When | Exit of `run` |
|---|---|---|
| `ok-to-review` | agent exit 0, all expected files present, nothing forbidden, no unsafe link, test and checks pass, no other file changed | 0 |
| `check-stray-files` | as above but files outside `--expect` changed (only judged when `--expect` is given) | 0 |
| `failed` | setup, agent (non-zero, not killed), test or check non-zero or timed out, expected file missing after the retry, forbidden path touched, unsafe link, patch over the cap, workspace or sandbox error | 1 |
| `timeout` | the agent's watchdog fired | 124 |
| `quota-exhausted` | the agent reported its quota exhausted | 1 (3 is reserved) |
| `interrupted` | SIGINT or SIGTERM during the run, or a crashed run completed by `finish` or `clean` | 1 |
| `running` | between the start and the end of a run only | |

Precedence when several apply: `interrupted`, `timeout`, `quota-exhausted`,
`failed`, `check-stray-files`, `ok-to-review`. The report lists every cause in
`reasons`, not only the winning one.

`reasons` values: `workspace-failed`, `sandbox-failed`, `setup-failed`,
`agent-failed`, `agent-timeout`, `quota-exhausted`, `interrupted`,
`missing-files`, `forbidden-path`, `unsafe-symlink`, `test-failed`,
`check-failed`, `patch-too-large`, `stray-files`.

Your verdict (`accepted`, `modified`, `rejected`; `abandoned` for `clean`) is
the second journal, in `leads`. A lead row is the only thing that closes a run
for `clean`.

## What is judged

The state of the worktree is a tree id taken with a temporary git index, so the
real index is untouched and a symbolic link is stored as a link, never
followed. Files ignored by git are not seen (an `--expect` path that is ignored
is reported missing).

- **Forbidden:** a changed path that is, or is under, a `--forbid` path (compared without regard to case), or has a `.git` segment.
- **Missing:** an `--expect` path not added or modified.
- **Stray:** a changed path outside `--expect` (not judged without `--expect`).
- **Unsafe link:** an added or changed link whose target is absolute or leaves the worktree. It fails the run even if the file was expected.
- **Names:** a file name with a control character or invalid UTF-8 is reported as `<invalid-name-N>`.

Forbidden paths, unsafe links and strays are judged from the state **after the
agent and before `--test`/`--check`**: what the checks write is not the agent's
doing and is not in the patch.

## Retry

One retry, in the same session, only if all hold: the first launch ended
normally (exit 0, not killed, no quota), expected files are missing, no earlier
retry. The retry prompt is a fixed text naming the missing paths (validated
repository paths only). The watchdog covers both launches together: one delay.
No retry after a quota, a kill or a non-zero exit.

## Files of a run

Under the data folder (mode 0700, git-ignored by its own `.gitignore`):

| Path | Mode | Content |
|---|---|---|
| `worktrees/<session>/` | 0700 | The disposable checkout. Removed by `finish` or `clean` |
| `worktrees/<session>.lock` | 0600 | The run lock |
| `runs/<session>/report.json` | 0600 | Machine report (below) |
| `runs/<session>/report.txt` | 0600 | The same facts, one per line |
| `runs/<session>/changes.patch` | 0600 | `git diff` from the base commit to the final state, new files and binaries included. Apply it with `git apply`. Not written when it is over `delegate.max_patch_bytes` |
| `runs/<session>/agent.log`, `setup.log`, `test.log`, `check-N.log` | 0600 | Output, capped by `delegate.max_log_bytes` (a `[truncated]` line says so). They may contain code; nothing reads them back or prints them |

The run folder is kept after `finish`: you apply the patch after the worktree is
gone. kanjo never deletes it.

### report.json (version 1)

Written once, atomically. Consumers ignore unknown fields; a field is added
without changing `schema_version`, and the version changes only when a field
changes meaning. Counts, codes, ids and repository-relative paths only: never a
command, a prompt, an answer or code.

```json
{
  "schema_version": 1,
  "session": "ses_20261103T120000_a1b2c3",
  "task": "ABC-12",
  "adapter": "script",
  "model": "test-model",
  "verdict": "ok-to-review",
  "reasons": [],
  "exit_code": 0,
  "started_at": "2026-11-03T12:00:00.000Z",
  "duration_ms": 8120,
  "launches": 2,
  "retries": 1,
  "timed_out": false,
  "tokens": {"status": "available", "by_model": [{"model": "test-model", "input": 0, "output": 0,
             "reasoning": 0, "cache_write_5m": 0, "cache_write_1h": 0, "cache_read": 0, "cost_usd": null}]},
  "files": {"expected": ["src/a.go"], "missing": [], "forbidden_touched": [], "unexpected": [], "unsafe_symlinks": []},
  "steps": [{"name": "setup", "exit": 0, "duration_ms": 10, "timed_out": false},
            {"name": "agent", "exit": 0, "duration_ms": 8000, "timed_out": false},
            {"name": "test", "exit": 0, "duration_ms": 100, "timed_out": false},
            {"name": "check-1", "exit": 0, "duration_ms": 10, "timed_out": false}],
  "base": "<commit id>",
  "branch": "delegate/ses_20261103T120000_a1b2c3",
  "patch": "changes.patch"
}
```

`tokens.status` is `available` or `unavailable` (the agent reported no usage).
`cost_usd` is null when unknown and 0 for a free model. Steps are named
(`setup`, `agent`, `test`, `check-1`, ...), never quoted: commands can carry a
token. `patch` is empty when no patch was kept.

`report.txt` ends with `patch:` and `apply:` lines (paths relative to the data
folder; `kanjo run` prints them absolute and quoted), a `finish:` line, and a
neutral `note:` line.

## Watchdog

There is no GNU `timeout`. The `nosandbox` adapter runs each command in its own
process group. When the deadline passes it sends SIGTERM to the whole group,
waits `delegate.kill_grace_s`, then SIGKILL to the group; what is left in the
group when a command ends is killed too. Output goes to a writer owned by the
caller, and the wait for output pipes is bounded, so a child that keeps a pipe
open cannot hang the run.

Limit: a descendant that leaves the group (`setsid`, a double fork) survives.
Only a container closes that.

## Locks and crashes

A run holds an exclusive `flock` on `worktrees/<session>.lock` for its whole
life; the lock disappears with the process, even when it is killed. "Running"
is decided by that lock, not by a flag. A row recorded as running whose lock is
free is a crashed run: `finish` and `clean` complete it as `interrupted` first.
`clean` never touches a run whose lock is held, even with `--yes`.

## Edge cases

| Case | Behaviour |
|---|---|
| Worktree folder or branch already exists | Refused, never reused, never deleted. The run is `failed`, `created` stays 0, exit 1. Session ids are generated (time plus 24 random bits): this means a collision or a stranger |
| Repository with no commit | Exit 1 before anything is written; commit once or pass `--ref` |
| Ref not found, not a commit | Exit 1 before anything is written. A ref starting with `-`, or with a space or control character, is exit 2 |
| SIGINT or SIGTERM during a run | The box is killed like a timeout, the report and the patch are written for what exists, the row is `interrupted`, the lock is released, exit 1. The worktree stays for you. A second signal during cleanup is ignored |
| SIGKILL or power loss mid-run | The row stays `running`, the lock is gone; the next `finish` or `clean` completes it as `interrupted` |
| `finish` twice | Same verdict: exit 0 `already recorded`. Another verdict: exit 1, nothing changed |
| `finish` on a run still running | Exit 1 |
| `finish` when the worktree is already gone | Records the verdict, says `workspace already removed` |
| `finish` when the branch moved | Exit 1, nothing deleted, no verdict (recover with git, then `finish` again). A run that ended without a known tip is held to its base commit |
| A folder replaced by a link, or not listed by git on the run's branch | Never inspected or removed (`not a worktree kanjo created`) |
| A task label with `/`, a file name from the agent | Never used in a path: folders, branches and lock names come only from the generated session id |
| Git hooks, filters, external programs | The tool's own git calls disable hooks, fsmonitor, external diff and text conversion, ignore global and system configuration and run with a rebuilt environment |
| A crash between creating the worktree and marking it created | A worktree the journal marks `created = 0`: it is never deleted by kanjo (a leak, not a loss). Remove it with git |

## Environment of the agent and the commands

Nothing is inherited except `PATH`, `LANG` and `LC_ALL` (when set) and the names
listed in `delegate.env_pass`. `HOME` and `TMPDIR` point to private folders of
the run (removed at the end). The script agent also gets `KANJO_SESSION`,
`KANJO_MODEL` and `KANJO_RESUME`. The prompt is on standard input.

## Configuration

All `delegate.*` keys are in [configuration.md](configuration.md) and user-only:
a repository's `kanjo.toml` cannot set any.

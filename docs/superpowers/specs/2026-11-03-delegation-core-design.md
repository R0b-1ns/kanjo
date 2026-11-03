# Slice 4 design: delegation core with fakes

Source of truth: `docs/spec.md` sections 2.1, 5, 6, 8, 10, 11 and 12 (threats).
This slice adds the ports `AgentRunner`, `Workspace` and `Sandbox` (plus two
small ones, below), their fakes and contract tests, the run and lead journals,
and four commands: `kanjo run`, `finish`, `summary` and `clean`. The real git
worktree adapter enters the slice. No model and no container are used.

## Goals

- `kanjo run`: a disposable worktree on `delegate/<session>`, an agent started
  through `AgentRunner` in a `Sandbox`, a watchdog (past the delay: `timeout`,
  exit 124), one retry in the same session when expected files are missing,
  `--test` and `--check` run by the tool in the same sandbox, a before/after
  comparison of the git state, `changes.patch`, a usage row, a journal row,
  `report.json` and `report.txt`. The tool never commits, pushes or applies.
- `kanjo finish`: the lead's verdict (`accepted`, `modified`, `rejected`),
  removal of the worktree and of its branch, the command that applies the
  patch, then a Claude snapshot and the task totals.
- `kanjo summary`: totals of the journal.
- `kanjo clean`: lists, and with `--yes` removes, the worktrees and branches the
  journal recorded and that have no lead verdict; records `abandoned`.
- Every external thing behind a port with a fake and a contract test in
  `internal/kanjotest`. The core runs end to end on fakes.

## Non-goals (stay `open`)

JSON agent returns (exit 5), routing, caps and exit 4, the real container, the
opencode adapter, `batch`, cost per accepted task, fallback models, exit 3 as a
process code (the verdict exists, see call 6), secret injection for providers,
automatic price update, a repository-visibility policy, `--force` on `finish`,
re-judging a run, listing branches the journal does not know. A "local script"
agent exists for tests only; it never runs model-produced code as an agent.

## Anti-goals

A run that leaves a branch, a folder or a lock behind without a journal row
that can find it. A deletion of anything the journal did not record. Content
(prompt, answer, code) or a secret in the database, the journal or a report.

## Decisions given by the user

Journal in SQLite tables of `kanjo.db`, schema version 3, same `Clock` as the
markers (spec section 8 and `docs/data-model.md` change). Core knows no tool.
The real `Workspace` has an integration test on a real temporary repository,
separate and marked. A local test-only script agent. No automatic branch
deletion: `clean` is explicit and deletes only what the journal recorded.
Codes of this slice: 0, 1, 2, 124 (3 and 4 reserved). No visibility policy; no
automatic price update.

## My calls, to review

Numbered so you can answer by number.

1. **A run row is written before anything is created** (verdict `running`) and
   completed once at the end. Otherwise a crash leaves a worktree that no row
   can name, and `clean` (which only deletes recorded things) could never find
   it. A completed row is never changed again.
2. **No absolute path in the database.** The journal stores `session`, `branch`
   (`delegate/<session>`) and `dir` (`worktrees/<session>`, relative to the data
   folder). Both are derived from the session, and checked equal to the
   derivation when read. Data-model rule "paths of your files are never stored"
   holds.
3. **`finish`/`clean` delete a branch only if its tip equals the tip recorded
   at the end of the run** (no `--force`). If the lead or the agent moved the
   branch, nothing is deleted and the message says why. Protects commits made
   on the branch.
4. **"Running" is decided by a lock, not by a flag.** The workspace adapter holds
   an exclusive `flock` on `worktrees/<session>.lock` for the whole run. A
   `running` row whose lock is free is a crashed run: `finish` and `clean`
   complete it as `interrupted` first. `clean` never touches a locked run.
5. **`finish` removes first, then records the verdict**, and is idempotent. A
   crash between the two leaves a recoverable state (run without verdict, folder
   gone): run `finish` again. The same verdict twice: exit 0, nothing written.
   A different verdict: exit 1, nothing changed (the journal is append-only).
6. **Quota exhaustion in this slice**: the fake can simulate it, `run` detects
   it (it blocks the retry), the verdict `quota-exhausted` is recorded, and the
   process exits **1**. Exit 3 is switched on in the slice that owns quota.
7. **Junior usage rows set `marked = messages`.** The task was fixed by the
   command line, the strongest attribution; no new `task_source` value, so
   `usage` shows these messages as "by marker". Documented.
8. **Run output files are kept**: agent, setup, test and check output go to
   `runs/<session>/*.log` (mode 0600, capped by `delegate.max_log_bytes`, never
   read back, never printed, never in the database or the report). They may
   contain code. Alternative if you prefer: no logs by default.
9. **The no-sandbox adapter is called `nosandbox`** and prints a warning on
   every run (spec section 12: "with a warning").
10. **Two extra small ports**: `RunJournal` (the decided SQLite tables, behind
    an interface) and `Artifacts` (creates the files of `runs/<session>/`).
    Without `Artifacts` the core would need `os`.
11. **The git state is a tree id**, taken with a temporary index
    (`GIT_INDEX_FILE` + `git add -A` + `git write-tree`), so it never touches the
    real index and never follows a symbolic link (checked: `git hash-object`
    follows links, a temporary index stores them as mode 120000). Verdicts about
    forbidden and stray files use the state taken **after the agent and before
    `--test`/`--check`**: what the checks write is not the agent's doing and is
    not in the patch.
12. **Reports hold counts, codes and repository-relative paths only.** Commands
    are never stored (a `--test` string can carry a token): steps are named
    `setup`, `test`, `check-1`, `check-2`. File names from the agent that
    contain control characters or are not UTF-8 are reported as
    `<invalid-name-N>`.
13. **Integration tests use the build tag `integration`** (`go test -race -tags
    integration ./...`). They need `git` and nothing else. I add that command to
    `CLAUDE.md` and a step to `ci.yml`; the workflow is not verified on GitHub.
14. **`report.txt` ends with a neutral note line**, not a command of another
    tool, so nothing in the repository names a tool kanjo does not depend on.
15. **Seven user-only config keys** under `delegate.` (list below). A repository
    `kanjo.toml` cannot set any.
16. **`--agent` is required** (no default): the only adapter today is `script`.
17. **The tool runs git with hooks and external programs disabled**
    (`-c core.hooksPath=/dev/null`, no fsmonitor, `--no-ext-diff
    --no-textconv`, global and system config ignored). Checked: without it a
    `post-checkout` hook runs at `git worktree add`.
18. **An added or changed symbolic link whose target is absolute or leaves the
    worktree makes the run `failed`** (`unsafe-symlink`), even if the file was
    expected.
19. **`summary` counts tasks as distinct labels other than `-`**, and prints runs
    without a label separately.
20. **The prompt reaches the agent on stdin**, never in an argument (visible in
    the process list).

## Lifecycle

```
run:    validate flags -> resolve ref -> RunJournal.Begin (running)
        -> Workspace.Create (takes the lock) -> RunJournal.Created
        -> Sandbox.Open -> setup -> state0 -> agent [-> one retry] -> state1
        -> analysis -> patch -> test, checks -> verdict -> report files
        -> usage row -> RunJournal.Finish -> release lock
finish: Reap -> Workspace.Remove (idempotent) -> RunJournal.Verdict
        -> snapshot -> totals
clean:  Reap -> (--yes) Workspace.Remove -> RunJournal.Verdict(abandoned)
```

Order inside `run` after the agent: forbidden paths, unsafe symlinks, expected
files and stray files are judged from `state0` and `state1`; `changes.patch` is
the diff `base..state1`; then `--test` and each `--check`, in this order, each
with its own timeout; a non-zero code or a timeout is a failure and the next
command still runs (the report lists every result).

### Verdict and exit code

| Verdict (journal) | When | Exit |
|---|---|---|
| `ok-to-review` | agent exit 0, all expected files present, nothing forbidden, no unsafe link, test and checks pass, no other file changed | 0 |
| `check-stray-files` | as above but files outside `--expect` changed | 0 |
| `failed` | setup, agent (non-zero, not killed), test or check non-zero or timed out, expected file missing after the retry, forbidden path touched, unsafe link, patch over the cap | 1 |
| `timeout` | the agent's watchdog fired | 124 |
| `quota-exhausted` | the agent runner reported quota exhaustion | 1 (call 6) |
| `interrupted` | SIGINT or SIGTERM during the run, or a crashed run completed by `Reap` | 1 |
| `running` | between `Begin` and `Finish` only | |

Precedence when several apply: `interrupted`, `timeout`, `quota-exhausted`,
`failed`, `check-stray-files`, `ok-to-review`. The report lists every cause
(`reasons`, a closed list of identifiers), not only the winning one. Exit 2:
usage error before anything is created. Exit 1: precondition errors (no commit,
bad ref, data folder, database, worktree already present).

### Retry rule

One retry, in the same session, only if all hold: the first launch ended
normally (exit 0, not killed, no quota), expected files are missing, no earlier
retry. The retry prompt is a fixed template naming the missing paths. The
watchdog covers both launches together (one delay for the agent phase).

## Ports (all in `internal/core`, fakes and contracts in `internal/kanjotest`)

Signatures are minimal; the Go names are fixed in the plan.

```go
// AgentRunner runs one launch of an agent inside a box.
type AgentRunner interface {
    Run(ctx context.Context, box Box, req AgentRequest) (AgentResult, error)
}
// AgentRequest: Session, Model, Prompt (string, untrusted size-capped), Resume (bool), Log io.Writer.
// AgentResult: Exit int; Killed bool (ctx ended it); Quota bool;
//              Usage []ModelUsage (nil = unavailable); Duration time.Duration.
// error = the adapter could not start (a killed or failing agent is a result).

// Sandbox opens an execution environment for one workdir.
type Sandbox interface {
    Open(ctx context.Context, spec BoxSpec) (Box, error)
}
// BoxSpec: Dir string; Env []string (the whole environment, nothing inherited).
type Box interface {
    Exec(ctx context.Context, cmd Cmd) (ExecResult, error)
    Close() error
}
// Cmd: Argv []string or Shell string (exactly one), Stdin io.Reader, Out io.Writer, Grace time.Duration.
// ExecResult: Exit int; Killed bool; Duration time.Duration.

// Workspace owns the disposable worktrees.
type Workspace interface {
    Resolve(ctx context.Context, ref string) (commit string, err error) // ErrNoCommit, ErrBadRef
    Create(ctx context.Context, session, commit string) (Workdir, error) // takes the lock; ErrExists
    State(ctx context.Context, w Workdir) (State, error)
    Diff(ctx context.Context, w Workdir, to State, dst io.Writer, max int64) error // base..to
    Release(w Workdir) error // drops the lock
    Inspect(ctx context.Context, o Owned) (Presence, error)
    Remove(ctx context.Context, o Owned) error // ErrBusy, ErrMoved, ErrNotOurs
}
// Workdir: Session, Dir, Branch, Base. State: opaque Tree id plus Entries (path, mode, link target).
// Owned: Session, Branch, Dir, Tip (what the journal recorded).
// Presence: DirExists, BranchExists, Busy, TipMatches.

// Artifacts creates the files of one run (runs/<session>/).
type Artifacts interface {
    Create(ctx context.Context, session string, name ArtifactName) (io.WriteCloser, error)
    Write(ctx context.Context, session string, name ArtifactName, data []byte) error // atomic
}

// RunJournal is the append-only journal of runs and lead verdicts.
type RunJournal interface {
    Begin(ctx context.Context, r Run) error            // ErrDuplicate
    Created(ctx context.Context, session string) error
    Finish(ctx context.Context, session string, r RunResult) error // running -> final, once
    Judge(ctx context.Context, l Lead) error           // ErrNoRun, ErrJudged(sameVerdict)
    Runs(ctx context.Context) ([]RunRecord, error)     // each with its lead, if any
}
```

`Workdir.Dir` is opaque to the core (a path for the real adapter, a name for the
fake). The core never reads a file. `State.Entries` lets the core judge forbidden
paths and symbolic links without knowing git; `Diff` is a method of the
workspace because only git knows the format.

### What the scripted fake agent can do

A `ScriptedAgent` is built from steps, applied to a `FakeWorkspace`: `Write`
(create or change a file), `Remove`, `Symlink`, `Succeed`, `ForgetFile` (write
all but one expected path), `TouchForbidden`, `Loop` (block until the context
ends, then report `Killed`), `ExhaustQuota`, `Crash(code)`, `ReportUsage`, and
different steps for the first launch and the retry. Not simulated yet: invalid
JSON (slice of section 16).

### Contracts (every adapter of a port must pass its suite)

- **AgentRunner**: a cancelled context ends the call within a bound and sets
  `Killed`; a failing agent is a result, not an error; the result carries no
  prompt or output text (fields are numbers, codes and model ids); `Resume`
  is accepted; a second call after the first works.
- **Sandbox**: the environment is exactly `BoxSpec.Env`; exit codes pass through;
  a cancelled context kills the command **and its children** and sets `Killed`
  within `Grace` plus a bound; `Out` is written and capped by the caller; after
  `Close` the box refuses to run. Each adapter supplies probe commands.
- **Workspace**: distinct sessions give distinct workdirs and branches; the same
  session twice is `ErrExists` and changes nothing; `State` equals after a
  no-op and differs after a change; `Diff` includes new, changed and deleted
  files; `Remove` of an unknown or foreign session is `ErrNotOurs` and deletes
  nothing; `Remove` while the lock is held is `ErrBusy`; a moved tip is
  `ErrMoved`; `Remove` twice is a success.
- **Artifacts**: names are a closed set; a session id that fails `ValidSession`
  is refused; `Write` is atomic; a name is created once.
- **RunJournal**: `Begin` twice is `ErrDuplicate`; `Finish` only once and only
  from `running`; `Judge` once per run, same verdict again is reported as such;
  concurrent `Judge` calls keep exactly one; invalid records are refused on
  write and on read; a tampered `branch` or `dir` fails the read.

## Data model (schema version 3)

```sql
CREATE TABLE runs(
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  session     TEXT NOT NULL UNIQUE,
  task        TEXT NOT NULL,                  -- label or '-'
  adapter     TEXT NOT NULL,                  -- same charset as usage.source
  model       TEXT NOT NULL,
  started_at  TEXT NOT NULL,                  -- core.MarkerTimeLayout, UTC, from Clock
  finished_at TEXT,
  verdict     TEXT NOT NULL CHECK(verdict IN ('running','ok-to-review','check-stray-files',
              'failed','timeout','quota-exhausted','interrupted')),
  exit_code   INTEGER,
  launches    INTEGER NOT NULL DEFAULT 0 CHECK(launches BETWEEN 0 AND 2),
  retries     INTEGER NOT NULL DEFAULT 0 CHECK(retries BETWEEN 0 AND 1),
  duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(duration_ms >= 0),
  base        TEXT NOT NULL,                  -- commit id, 40 or 64 hex
  tip         TEXT,                           -- branch tip at the end of the run
  branch      TEXT NOT NULL,                  -- 'delegate/' || session
  dir         TEXT NOT NULL,                  -- 'worktrees/' || session
  created     INTEGER NOT NULL DEFAULT 0 CHECK(created IN (0,1)),
  CHECK((verdict = 'running') = (finished_at IS NULL)));
CREATE INDEX runs_task ON runs(task, started_at);
CREATE TABLE leads(
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  at      TEXT NOT NULL,
  session TEXT NOT NULL UNIQUE REFERENCES runs(session),
  verdict TEXT NOT NULL CHECK(verdict IN ('accepted','modified','rejected','abandoned')));
```

- A lead row is the only thing that closes a run for `clean`. `abandoned` is
  written only by `clean`; `finish --verdict abandoned` is a usage error.
- `Finish` is the only update of `runs` and only while `verdict = 'running'`
  (the code checks it inside the transaction; a contract case proves it).
- Reads re-validate every field (`ValidSession`, `ValidTask` or `-`, `branch`
  and `dir` equal to their derivation, hex ids, verdict sets, `retries <=
  launches`). A bad row fails the whole read; the error never repeats a value.
- `PRAGMA user_version = 3`. Version 2 to 3 is additive (`CREATE TABLE IF NOT
  EXISTS`), done by commands that write. Read-only commands read a version 2
  database as "no runs" without changing it. An older kanjo refuses version 3:
  copy `kanjo.db` before the first `run` and update every kanjo you use.
- A usage row for a run: `source = adapter`, `session = run session`, `day` =
  UTC day of `started_at`, `task`, `model`, `marked = messages`,
  `cost_reported_usd` from the adapter (0 for a free model, NULL if unknown).
  One row per model of `AgentResult.Usage`; none when usage is unavailable.
  The session record has `task_source = none` (all messages are marked).

## Files of a run

Under the data folder (mode 0700, already git-ignored by its own `.gitignore`):

| Path | Mode | Content |
|---|---|---|
| `worktrees/<session>/` | 0700 | The disposable checkout. Removed by `finish` or `clean` |
| `worktrees/<session>.lock` | 0600 | The run lock (call 4). Removed with the worktree |
| `runs/<session>/report.json` | 0600 | Section "report.json" |
| `runs/<session>/report.txt` | 0600 | Readable lines |
| `runs/<session>/changes.patch` | 0600 | Diff `base..state1`, new files included. The only file with code besides the logs |
| `runs/<session>/agent.log`, `setup.log`, `test.log`, `check-N.log` | 0600 | Output, capped (call 8) |

The run folder is kept after `finish` (the lead applies the patch after the
worktree is gone). `kanjo` never deletes it.

### report.json (version 1)

Written atomically. Consumers ignore unknown fields; a field is added without
changing `schema_version`, and the version changes only when a field changes
meaning.

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
  "tokens": { "status": "available",
              "by_model": [ { "model": "test-model", "input": 0, "output": 0, "reasoning": 0,
                              "cache_write_5m": 0, "cache_write_1h": 0, "cache_read": 0, "cost_usd": null } ] },
  "files": { "expected": ["src/a.go"], "missing": [], "forbidden_touched": [],
             "unexpected": [], "unsafe_symlinks": [] },
  "steps": [ { "name": "setup", "exit": 0, "duration_ms": 10, "timed_out": false },
             { "name": "agent", "exit": 0, "duration_ms": 8000, "timed_out": false },
             { "name": "test",  "exit": 0, "duration_ms": 100, "timed_out": false },
             { "name": "check-1", "exit": 0, "duration_ms": 10, "timed_out": false } ],
  "base": "<commit id>",
  "branch": "delegate/ses_20261103T120000_a1b2c3",
  "patch": "changes.patch"
}
```

`reasons` values: `setup-failed`, `agent-failed`, `agent-timeout`,
`quota-exhausted`, `interrupted`, `missing-files`, `forbidden-path`,
`unsafe-symlink`, `test-failed`, `check-failed`, `patch-too-large`,
`stray-files`. `tokens.status` is `available` or `unavailable`.
`report.txt` has the same facts, one per line, ends with
`apply: git apply <path>` and `finish: kanjo finish <session> --verdict ...`
(paths relative to the data folder) and one neutral note line
(`note: <task> <session> <verdict> <duration>`). Stdout shows the same text with
absolute paths.

Spec section 16 fields (`cap`, `fallback_used`, validated agent return) are not
in version 1; they are added later without a version change.

## Watchdog (no GNU `timeout`)

Implemented once, in the `nosandbox` adapter, and required of every `Sandbox` by
its contract:

1. The command starts in its own process group (`Setpgid`).
2. The core passes a context with the deadline (`WithTimeoutCause`). At
   `ctx.Done` the adapter sends SIGTERM to the whole group, waits `Grace`
   (`delegate.kill_grace_s`, default 5), then SIGKILL to the group.
3. Output goes to a writer owned by the caller (a file), not to pipes that a
   surviving child could hold open; `cmd.WaitDelay` bounds the wait anyway.
4. `ExecResult.Killed` is true; the core tells timeout (the cause is the
   deadline) from interruption (the parent context ended) and maps them to
   `timeout`/124 or `interrupted`/1.

Limit, documented: a descendant that leaves the group (`setsid`, double fork)
survives. Only the container slice closes this.

## Edge cases

| Case | Behaviour |
|---|---|
| Worktree folder already exists | `Create` returns `ErrExists`; nothing is deleted or reused; the run row is completed as `failed`, `created = 0`; exit 1. Session ids are generated (time plus 24 random bits), so this means a collision or a stranger |
| Repository with no commit | `Resolve` returns `ErrNoCommit` before any row is written; exit 1 and the message says to commit once or pass `--ref` |
| Branch `delegate/<session>` already exists | Same as the folder: refused, never reused, never force-created, never deleted (`created = 0`) |
| Ref not found or not a commit | `ErrBadRef`, exit 1, before any row. A ref starting with `-` or with control characters is exit 2 |
| SIGINT/SIGTERM during a run | The box is killed (same path as the watchdog), the report and patch are written for what exists, the row is completed as `interrupted`, the lock is released, exit 1. The worktree stays for the lead (`finish` or `clean`). A second signal during cleanup is ignored (the bookkeeping context is not cancellable, bounded by 10 s) |
| SIGKILL or power loss mid-run | The row stays `running`, the lock is gone; `Reap` completes it as `interrupted` at the next `finish`/`clean` |
| `finish` twice | Same verdict: exit 0 "already recorded". Different: exit 1, nothing changed |
| `finish` on a run that has not finished | Lock held: exit 1 "still running". Lock free: `Reap` then proceeds |
| `finish` when the worktree is already gone | Succeeds: records the verdict, prints "workspace already removed" |
| `finish` when the branch moved | Exit 1, nothing deleted, no verdict written (call 3). Recover with git by hand, then `finish` again |
| `clean` while a run is alive | Listed as `running`, skipped, never removed, even with `--yes` |
| `clean` with two processes at once | `Judge` is unique per run; the loser sees `ErrJudged` and reports it as done |
| Symbolic link at the worktree root, or the worktree folder replaced by a link | `Remove` and `Inspect` refuse (`ErrNotOurs`): the folder must be a real directory whose resolved path equals the derived one and that git lists as a worktree on the recorded branch |
| `--expect` path that is git-ignored | Reported `missing` (the state sees only non-ignored files). Documented |
| Task file names, task labels with `/` | Never used in a path: all folders, branches and lock names come from the session id |

## Configuration (all user-only, none in the repository allow-list)

| Key | Default | Meaning |
|---|---|---|
| `delegate.timeout_s` | 900 | Agent watchdog (both launches). `--timeout` overrides |
| `delegate.command_timeout_s` | 600 | Each `--setup`, `--test`, `--check` |
| `delegate.kill_grace_s` | 5 | SIGTERM to SIGKILL delay |
| `delegate.max_log_bytes` | 1048576 | Cap per log file; the rest is dropped, a marker line notes it |
| `delegate.max_patch_bytes` | 16777216 | Cap of `changes.patch`; over it the run is `failed` |
| `delegate.max_prompt_bytes` | 1048576 | Cap of the prompt file |
| `delegate.env_pass` | `[]` | Names of caller variables forwarded to the box (threat 4). `HOME` and `TMPDIR` are always set by the adapter to private per-run folders |

## Commands

```
kanjo run --agent NAME --model MODEL --prompt-file FILE [--task LABEL]
          [--ref REF] [--expect PATH]... [--forbid PATH]... [--setup CMD]
          [--test CMD] [--check CMD]... [--timeout SECONDS]
          [--script FILE]  [--repo DIR] [--data-dir DIR]
kanjo finish <session> --verdict accepted|modified|rejected [--repo DIR] [--data-dir DIR]
kanjo summary [--task LABEL] [--since YYYY-MM-DD] [--data-dir DIR]
kanjo clean [--yes] [--repo DIR] [--data-dir DIR]
```

- `--repo` defaults to the current folder and must be a git working tree root.
  `--script` is valid only with `--agent script`: an absolute or relative file,
  a regular executable, owned by the user, not group- or world-writable, **not
  inside the worktree** (the worktree is a copy; the script is never read from
  a place the agent can write).
- `--expect`, `--forbid`: repository-relative, slash separated, no `..`, no
  empty segment, no control character, at most 256 bytes, at most 64 each. A
  forbidden path matches itself and everything under it, case-insensitively.
  `.git` is always forbidden. Passed to git after `--`.
- `--setup`, `--test`, `--check`: a non-empty string without NUL, at most 4096
  bytes, run by `/bin/sh -c` in the workdir with the box environment. They come
  from the command line, never from a repository file.
- `run` prints `report.txt` plus a one-line "apply" and "finish" hint; stderr
  carries `warning: nosandbox ...`. `finish` prints the apply command
  (`git -C <repo> apply <patch>`, quoted) unless the verdict is `rejected`, then
  the snapshot line and the task totals (as `usage --task`). A snapshot that
  fails is a warning, not a failure: the verdict is already recorded.
- `clean` without `--yes` is read-only: it opens the database read-only, runs
  `git` read-only, creates nothing, and prints one line per candidate
  (`session task state dir branch`). `summary` is read-only too.
- `summary` prints: runs, distinct tasks (and runs without a label), launches,
  retries, total duration, a count per script verdict, a count per lead verdict
  and runs without a lead. Text only.

Exit codes: `run` 0, 1, 2, 124; `finish` 0, 1, 2; `summary` 0, 1, 2; `clean` 0
(also when nothing to do), 1 (a removal failed or was refused), 2.

## Threats and abuse cases (written before the code)

| # | Threat | Answer | Test (planned) |
|---|---|---|---|
| 1 | Task label or file name injected into a path, branch, lock or argument | Paths, branches and locks come only from the generated session id (checked with `ValidSession`); labels pass `ValidTask`; repo paths pass `ValidRepoPath` and go after `--`; refs pass `ValidRef` and `rev-parse --verify --end-of-options` | `core.TestValidRepoPath`, `core.TestValidRef`, `cli.TestRunRejectsHostileFlagsBeforeCreatingAnything`, `gitworktree.TestIntegrationHostileRefIsData` |
| 2 | Deleting the wrong branch or folder | Only `Owned` values from the journal; `branch`/`dir` re-derived from `session`; `created = 1`; folder must be a real directory listed by git on that branch; tip must equal the recorded tip; never a pattern, never `--force` | `sqlitestore.TestTamperedRunRowFailsTheRead`, `gitworktree.TestIntegrationRemoveRefusesForeignBranch`, `...RefusesMovedTip`, `...RefusesAReplacedFolder`, `kanjotest` workspace contract, `core.TestFinishNeverRemovesWhatTheJournalDoesNotName`, `core.TestCleanSkipsUnrecordedBranches` |
| 3 | `--test`, `--check`, `--setup` run with the user's rights | Documented risk until the container slice; strings come only from the command line; each has a timeout; process group killed; env purged; warning printed on every run | `nosandbox.TestKillsTheWholeGroup`, `cli.TestRunWarnsThatThereIsNoSandbox`, `docs/security.md` residual row |
| 4 | Caller secrets inherited by the agent or the checks | `BoxSpec.Env` is the whole environment: `PATH`, per-run `HOME` and `TMPDIR`, `LANG`, plus `delegate.env_pass`; nothing else | `nosandbox.TestEnvironmentIsExactlyTheSpec`, `cli.TestRunDoesNotPassCallerSecrets`, `kanjotest` sandbox contract |
| 5 | Prompt, answer, code or secret in the database, the journal, the report or the output | Typed records only (counts, codes, ids, labels, times); commands never stored; the prompt travels in memory and on stdin; logs are files nobody reads back; reports hold repo-relative paths | `cli.TestCanaryNeverInRunOutputReportOrDatabase` (canary in prompt file, agent output, test output, commands, file contents), `core.TestReportHasNoCommandText` |
| 6 | Symbolic link in the worktree (reading outside, bypassing `--forbid`, or planting a link in the patch) | State from a temporary index (links stored, never followed); an added or changed link with an absolute or escaping target is `unsafe-symlink`; `Remove` does not follow links | `gitworktree.TestIntegrationStateDoesNotFollowSymlinks`, `core.TestUnsafeSymlinkFailsTheRun`, `gitworktree.TestIntegrationRemoveDoesNotFollowLinks` |
| 7 | Git hooks, filters or external programs run by the tool's own git calls | Every call: `-c core.hooksPath=/dev/null`, `core.fsmonitor=false`, `--no-ext-diff --no-textconv`, `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`, a clean environment | `gitworktree.TestIntegrationHooksDoNotRun` (hook in the repo and one planted in the worktree's git dir) |
| 8 | Usage data or run files committed by mistake | Everything is under the data folder (own `.gitignore`, 0700); report files 0600 | `runfiles.TestFilesArePrivate`, `sqlitestore.TestOpenCreatesPrivateDirAndGitignore` (existing) |
| 9 | Hostile values in the journal (formulas, traversal, huge text) | Closed charsets and verdict sets, `CHECK` constraints, re-validation on read | `core.TestRunRecordValidate`, `sqlitestore.TestHostileRunValuesStayData` |
| 10 | Two processes at once corrupt the journal | One `BEGIN IMMEDIATE` per operation; `UNIQUE(session)` on both tables; `Finish` only from `running` | `kanjotest` RunJournal contract ("concurrent judges keep exactly one", "finish only once"), run on both implementations |
| 11 | `clean` removes a live run | Run lock held for the whole run; `clean` and `finish` try it without waiting | `core.TestCleanNeverTouchesALockedRun`, `gitworktree.TestIntegrationLockIsHeldWhileRunning`, `cli.TestIntegrationCleanDuringARun` |
| 12 | A hung command never ends (silent hang) | Watchdog per command and for the agent; kill the group; exit 124 for the agent | `core.TestAgentTimeoutIsVerdictTimeoutAndExit124`, `nosandbox.TestKillsGrandchildren`, `nosandbox.TestDoesNotWaitForAPipeHeldOpenByAChild` |
| 13 | Interrupt leaves things unrecorded | Row first; bookkeeping context not cancellable; `Reap` for hard kills | `core.TestInterruptCompletesTheRowAsInterrupted`, `cli.TestIntegrationSigintLeavesAFinishableRun` |
| 14 | A model-written script is used as the "agent" | `--script` must live outside the worktree, be a regular executable not writable by others; the adapter is named test-only and never reads the repository | `scriptagent.TestRefusesAScriptInsideTheWorkdir`, `scriptagent.TestRefusesAWritableScript` |
| 15 | Terminal control sequences in output (file names, labels, paths) | `printable` on everything printed; invalid names reported as `<invalid-name-N>` | `cli.TestRunOutputCannotCarryEscapes`, `core.TestHostileFileNameIsReportedWithoutControlCharacters` |
| 16 | Log or patch fills the disk | `max_log_bytes`, `max_patch_bytes`; over the patch cap the run is `failed` | `runfiles.TestLogIsCapped`, `core.TestPatchOverTheCapFailsTheRun` |
| 17 | A cloned repository's `kanjo.toml` changes timeouts, env, caps | `delegate.*` are user-only | `config.TestRepoConfigMayNotSetDelegate` |
| 18 | The agent reads or alters files outside the worktree | Not prevented without a container: the box is the user. Documented residual. The git state sees only the worktree | residual row in `docs/security.md` |
| 19 | Quota exhaustion triggers a retry or a substitute model | Retry only after a normal finish; no fallback exists | `core.TestNoRetryAfterQuota`, `core.TestNoRetryAfterAKill` |
| 20 | Old and new kanjo on one database | Version 2 read-only reads "no runs"; version 3 refused by older kanjo | `sqlitestore.TestMigratesVersion2KeepingEverything`, `sqlitestore.TestOpenReadOnlyReadsVersion2`, `sqlitestore.TestOpenRejectsNewerSchema` (existing, version 4) |

## Risks and open points

- Without a container the agent, the setup, the tests and the checks run with
  your rights and can read or change anything you can, including the
  repository's `.git` and the worktree's git directory. The tool hardens its own
  git calls (threat 7) but cannot undo that. Use the script agent only with
  scripts you wrote.
- The tool does not detect a changed `.git` file or git directory of the
  worktree (git does not report it).
- A descendant that leaves the process group survives the watchdog.
- A crash between `Workspace.Create` and `RunJournal.Created` leaves a worktree
  the journal marks `created = 0`: it is never deleted by kanjo (a leak, not a
  loss). Remove it with git.
- A moved branch tip blocks `finish` until the lead resolves it by hand; there
  is no `--force` in this slice.
- `git worktree` objects: the temporary-index trees and blobs stay in the
  repository's object store (unreachable, collected by `git gc`). They contain
  the agent's files.
- Case-insensitive `--forbid` over-matches on case-sensitive file systems on
  purpose; Unicode normalization differences on macOS are not handled.
- Not verified here: behaviour on Linux (only macOS was tried for the git
  checks), GitHub workflow for the `integration` tag, Windows (out of scope).

## Documentation (one topic per file)

| File | Change |
|---|---|
| `docs/commands/run.md`, `finish.md`, `summary.md`, `clean.md` | New: flags with defaults, output, exit codes, examples |
| `docs/delegation.md` | New: lifecycle, verdict table, retry rule, files of a run, `report.json` and `report.txt`, watchdog, edge cases |
| `docs/data-model.md` | Version 3, `runs` and `leads`, usage rows of runs, data folder files |
| `docs/exit-codes.md` | `run`, `finish`, `summary`, `clean`; 124; 3 and 4 still reserved |
| `docs/security.md` | Threat rows with tests; residual risks; "Not yet built" trimmed |
| `docs/adding-an-adapter.md` | The new ports, fakes, contracts, how to add an `AgentRunner`, `Workspace` or `Sandbox` |
| `docs/configuration.md` | The seven `delegate.*` keys; repository allow-list row |
| `docs/spec.md` | Section 8 (tables `runs` and `leads`), section 5.3 and 5.4 wording where it differs |
| `docs/README.md`, `CLAUDE.md` | Index entries; the `integration` test command |

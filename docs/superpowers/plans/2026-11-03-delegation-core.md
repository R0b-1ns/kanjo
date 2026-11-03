# Slice 4 Implementation Plan: delegation core with fakes

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** the ports `AgentRunner`, `Workspace`, `Sandbox` (plus `Artifacts` and `RunJournal`) with fakes and contract tests; the `runs`/`leads` tables (schema 3); the commands `run`, `finish`, `summary`, `clean`; the real git worktree adapter, the `nosandbox` adapter and a test-only script agent.

**Architecture:** `internal/core` gets the domain types, the ports and the pure logic (`Delegate`, `Finish`, `Reap`, `Clean`, `Summarize`), and imports no adapter. `internal/kanjotest` gets a fake and a contract suite per port. Adapters: `sqlitestore` (RunJournal), `nosandbox` (Sandbox), `gitworktree` (Workspace), `runfiles` (Artifacts), `scriptagent` (AgentRunner, test-only). `internal/cli` wires them.

**Design:** `docs/superpowers/specs/2026-11-03-delegation-core-design.md` (decisions, "my calls" numbered 1 to 20, threat table numbered 1 to 20). Founding: `docs/spec.md` sections 2.1, 5, 6, 8, 10, 11, 12.

## Global Constraints

- Everything committed is public: English, nothing about other projects, no personal path. Conventional commits, one logical change each, never a broken build; trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Never push, merge, open a PR or publish without the user's agreement at that moment. Do not touch the git identity.
- Before starting: `git switch main && git pull --ff-only`, `git status` clean, then `git switch -c slice-4-delegation-core`. The first commit of the slice is the two documents (design and plan) as validated: `docs: slice 4 delegation core design and plan`.
- TDD: write the test, run it, **see it fail for the right reason**, then implement. `/ub-mode` on each change. Tests offline, fixtures fictional. No real model, no container. Tests that use a real `git` are in files with `//go:build integration` and named `TestIntegration...`; they need only `git` and `sh`, and skip with a message when `git` is missing.
- `internal/core` imports no adapter and calls no `os`, `net`, `os/exec` or `time.Now`; time comes from `core.Clock`; ids from the injected `NewSession`.
- No secret or content in logs or data. Commands (`--test` strings) are never stored. No absolute path in the database.
- Any new config key goes in `docs/configuration.md`, any new flag in its `docs/commands/*.md` (existing tests enforce both).
- Gates before review: `go vet ./... && gofmt -l .`, `go test -race -count=1 ./...`, `go test -race -count=1 -tags integration ./...`, `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`, `crapper --changed` (CRAP < 10), `dryer <path>` (< 0.8), `mutator --changed --no-coverage --mutate-all` (kill survivors). Then `/code-review`, `/simplify`, security review of the full diff; every finding gets a red test first.
- Exit codes of this slice: 0, 1, 2, 124. Quota exhaustion exits 1 (verdict `quota-exhausted`); 3, 4, 5 stay reserved.

## Review Focus

- Deletion paths: `Remove` only from journal values, tip check, real directory check, no `--force`, no pattern.
- The run lock: held for the whole run, released on every exit path, `clean` never waits on it.
- Row-first ordering: every path out of `Delegate` completes the row; `Reap` for hard kills.
- No content: the canary test over stdout, stderr, database bytes, `report.json`, `report.txt` (logs and the patch excepted by design).
- The watchdog kills grandchildren and does not wait on a pipe a child holds.
- Schema 2 databases: read-only commands keep working before any migration.

---

### Task 1: Delegation domain types and validation

**Files:** Create `internal/core/delegate_types.go`, `internal/core/delegate_types_test.go`.

**Interfaces — Produces:** `type RunVerdict string` (`RunRunning`, `RunOK` `ok-to-review`, `RunStray` `check-stray-files`, `RunFailed`, `RunTimeout`, `RunQuota` `quota-exhausted`, `RunInterrupted`); `type LeadVerdict string` (`Accepted`, `Modified`, `Rejected`, `Abandoned`); `type Run struct{ Session, Task, Adapter, Model string; StartedAt time.Time; Base string }`; `type RunResult struct{ Verdict RunVerdict; Exit int; FinishedAt time.Time; Launches, Retries int; Duration time.Duration; Tip string }`; `type Lead struct{ At time.Time; Session string; Verdict LeadVerdict }`; `type RunRecord struct{ Run; RunResult; Created bool; Lead *Lead }`; `func BranchOf(session string) string` (`delegate/<session>`); `func DirOf(session string) string` (`worktrees/<session>`); `func (v RunVerdict) ExitCode() int` (0 ok and stray, 124 timeout, else 1); `func NewSessionID(now time.Time, rnd [3]byte) string` (`ses_20261103T120000_a1b2c3`); `ValidRepoPath(string) bool`, `ValidRef(string) bool`, `ValidCommitID(string) bool`; `Validate()` on `Run`, `RunResult`, `Lead`, `RunRecord`.

- [ ] **Step 1: Red tests:** `TestValidRepoPath` (table: `src/a.go` ok; `..`, `a/../b`, `/abs`, `a//b`, `./a`, empty, newline, NUL, 257 bytes refused; a leading `-` ok), `TestValidRef` (`main`, `origin/main`, `v1.2`, `HEAD~1`, `abc123` ok; `-x`, `a b`, `a\nb`, empty, 201 bytes refused), `TestValidCommitID` (40 and 64 hex ok, uppercase and 41 chars refused), `TestNewSessionIDIsAValidSession`, `TestBranchAndDirAreDerivedFromTheSession`, `TestRunValidate` (bad session, task `-` ok, task `../x` refused, zero time, bad base), `TestRunResultValidate` (retries above launches, launches 3, negative duration, `running` with a finish time, final verdict without one), `TestLeadValidate`, `TestVerdictExitCodes` (the table of the design), `TestRunErrorsNeverEchoTheValue`.
- [ ] **Step 2: Run** `go test ./internal/core -run 'TestValid|TestNewSession|TestBranch|TestRun|TestLead|TestVerdict'`; expected FAIL (undefined).
- [ ] **Step 3: Implement** with `ValidSession`, `ValidTask`.
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(core): run and lead types with validation`.

### Task 2: RunJournal port, in-memory journal and contract

**Files:** Create `internal/core/delegate_ports.go` (the `RunJournal` part and its errors), `internal/kanjotest/runjournal.go`, `internal/kanjotest/runjournal_test.go`.

**Interfaces — Produces:** `RunJournal` as in the design (`Begin`, `Created`, `Finish`, `Judge`, `Runs`), `Judge` returning `(existing LeadVerdict, err error)`; `ErrDuplicate`, `ErrNoRun`, `ErrNotRunning`, `ErrJudged`; `func NewMemRunJournal() *MemRunJournal`; `func RunJournalContract(t *testing.T, open func(t *testing.T) core.RunJournal)`.

- [ ] **Step 1: Red tests:** run the contract on `MemRunJournal`. Cases: "begin records a running run", "begin twice is ErrDuplicate and keeps the first", "created marks the run", "created of an unknown run is ErrNoRun", "finish completes a running run once", "finish of a final run is ErrNotRunning and changes nothing", "finish refuses an invalid result", "judge records one verdict", "judge twice returns ErrJudged with the existing verdict", "judge of an unknown run is ErrNoRun", "concurrent judges keep exactly one" (16 goroutines, mixed verdicts), "runs come back in start order with their lead", "an invalid record is refused on write".
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** `MemRunJournal` with a mutex.
- [ ] **Step 4: Run** `go test -race ./internal/kanjotest ./internal/core`; PASS.
- [ ] **Step 5: Commit** `test(kanjotest): in-memory run journal and its contract`.

### Task 3: Schema version 3 and migration

**Files:** Modify `internal/sqlitestore/store.go`, `internal/sqlitestore/migrate_test.go`.

**Behaviour:** `schemaVersion = 3`; `runs`, `leads` and `runs_task` added to `schema` (all `IF NOT EXISTS`); `migrate` needs no ALTER from 2. `OpenReadOnly` of a version 2 database works and a later `Runs` read returns an empty list without error when `s.version < 3`.

- [ ] **Step 1: Red tests:** `TestMigratesVersion2KeepingEverything` (v2 file built with raw SQL incl. usage and markers; `Open`; rows and markers intact; version 3; new tables exist), `TestFreshDatabaseHasTheRunTables`, `TestOpenReadOnlyReadsVersion2WithoutMigrating` (no write, version still 2), `TestOpenRejectsNewerSchema` updated to version 4, `TestRunsCheckConstraintsRefuseABadVerdict` (raw insert), `TestLeadsSessionIsUnique`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/sqlitestore`; PASS (existing tests unchanged except the version number).
- [ ] **Step 5: Commit** `feat(sqlitestore): schema version 3 with the runs and leads tables`.

### Task 4: SQLite run journal

**Files:** Create `internal/sqlitestore/runs.go`, `internal/sqlitestore/runs_test.go`.

**Behaviour:** `(*Store)` implements `core.RunJournal`. Every method is one `BEGIN IMMEDIATE` (`s.immediate`). `Finish` checks `verdict = 'running'` in the transaction. `Runs` joins `leads`, re-validates each record (`branch`/`dir` equal to `BranchOf`/`DirOf`), returns an empty list on a version 2 database, and its errors never repeat a stored value.

- [ ] **Step 1: Red tests:** `RunJournalContract` on SQLite (including the 16-goroutine case, `-race`); `TestHostileRunValuesStayData` (label `'; DROP TABLE runs;--` refused by validation, nothing stored); `TestTamperedRunRowFailsTheRead` (raw insert with `branch = 'delegate/other'`, `dir = '../x'`, a bad session, a bad verdict: each makes `Runs` fail; error text has no value); `TestRunsSurviveReopen`; `TestRunsOfAVersion2DatabaseAreEmpty`; `TestReadOnlyStoreRefusesToBegin` (`OpenReadOnly` then `Begin` fails, nothing written).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/sqlitestore`; PASS.
- [ ] **Step 5: Commit** `feat(sqlitestore): run and lead journal`.

### Task 5: Configuration keys `delegate.*`

**Files:** Modify `internal/config/config.go`, `internal/config/config_test.go`, `docs/configuration.md`.

**Behaviour:** `DelegateConfig{TimeoutS, CommandTimeoutS, KillGraceS int; MaxLogBytes, MaxPatchBytes, MaxPromptBytes int64; EnvPass []string}` with the defaults of the design; positive values required; `env_pass` names match `^[A-Za-z_][A-Za-z0-9_]{0,63}$`, at most 32; all keys user-only (absent from the repository allow-list).

- [ ] **Step 1: Red tests:** `TestDelegateDefaults`, `TestDelegateValuesMustBePositive`, `TestDelegateEnvPassRejectsBadNames`, `TestRepoConfigMayNotSetDelegate`, `TestUserConfigMaySetDelegate`; `TestEveryConfigKeyIsDocumented` (existing) fails until the doc rows exist.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** and document the seven keys.
- [ ] **Step 4: Run** `go test -race ./internal/config ./internal/cli`; PASS.
- [ ] **Step 5: Commit** `feat(config): delegate.* keys, user config only`.

### Task 6: Sandbox port, fake and contract

**Files:** Modify `internal/core/delegate_ports.go` (`Sandbox`, `Box`, `BoxSpec`, `Cmd`, `ExecResult`); create `internal/kanjotest/sandbox.go`, `internal/kanjotest/sandbox_test.go`.

**Interfaces — Produces:** as in the design; `func NewFakeSandbox() *FakeSandbox` (records every `BoxSpec` and `Cmd`; a `Handler func(Cmd) ExecResult` answers; a handler may block until the context ends and then returns `Killed`); `type SandboxProbe struct{ Exit func(n int) core.Cmd; Sleep func() core.Cmd; PrintEnv func() core.Cmd; SpawnChild func() core.Cmd; IgnoreTerm func() core.Cmd }`; `func SandboxContract(t *testing.T, open func(t *testing.T, env []string) core.Sandbox, p SandboxProbe)`.

- [ ] **Step 1: Red tests** (contract on `FakeSandbox` with scripted probes): "exit codes pass through", "the command sees every BoxSpec.Env entry and no host variable" (host canary set with `t.Setenv`), "a cancelled context kills and sets Killed within the bound", "children are killed with the command", "a command that ignores the polite signal is killed after Grace", "Out receives the output", "exec after Close is refused", "a relative or empty Dir is refused by Open".
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** the types and the fake.
- [ ] **Step 4: Run** `go test -race ./internal/kanjotest`; PASS.
- [ ] **Step 5: Commit** `test(kanjotest): sandbox port, fake and contract`.

### Task 7: `nosandbox` adapter (watchdog without GNU timeout)

**Files:** Create `internal/nosandbox/nosandbox.go`, `internal/nosandbox/nosandbox_test.go`.

**Behaviour:** `Sandbox{}` implements `core.Sandbox`. `Exec` runs `/bin/sh -c` (Shell) or the argv in `Dir` with environment = `BoxSpec.Env` plus `HOME` and `TMPDIR` set to a private folder (`os.MkdirTemp`, mode 0700, removed by `Close`); nothing inherited. `Setpgid`; on context end: SIGTERM to `-pgid`, wait `Grace`, SIGKILL to `-pgid`; `cmd.WaitDelay` bounds the wait; stdout and stderr go to `Cmd.Out` through a file, never a pipe held by the parent loop; `Killed` is true when the context ended it. Open refuses a relative or missing `Dir` or a symbolic link.

- [ ] **Step 1: Red tests:** `SandboxContract` with `sh` probes; `TestEnvironmentIsExactlyTheSpec` (host `SECRET_TOKEN` absent; `HOME`/`TMPDIR` are private and outside `Dir`), `TestKillsTheWholeGroup` (command starts a child `sleep` and a grandchild; after cancel none is alive, checked by pid), `TestTermThenKillAfterGrace` (`trap '' TERM`), `TestDoesNotWaitForAPipeHeldOpenByAChild` (child keeps stdout open; `Exec` returns within bound), `TestExitCodePassesThrough`, `TestCloseRemovesThePrivateFolder`, `TestOpenRefusesALinkedDir`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.** Document the limit (setsid escapes) in the package comment.
- [ ] **Step 4: Run** `go test -race ./internal/nosandbox`; PASS.
- [ ] **Step 5: Commit** `feat(nosandbox): local sandbox with a process-group watchdog`.

### Task 8: Workspace port, fake and contract

**Files:** Modify `internal/core/delegate_ports.go` (`Workspace`, `Workdir`, `State`, `Entry`, `Owned`, `Presence`, errors `ErrNoCommit`, `ErrBadRef`, `ErrExists`, `ErrBusy`, `ErrMoved`, `ErrNotOurs`); create `internal/kanjotest/workspace.go`, `internal/kanjotest/workspace_test.go`.

**Interfaces — Produces:** as in the design. `Entry{Path, Mode, Blob, Link string}`; `State{Tree string; Entries []Entry}`; `func NewFakeWorkspace() *FakeWorkspace` with `Put(session, path, content)`, `Link(session, path, target)`, `Delete(session, path)`, `Holding(session) bool` and a lock per session; `func WorkspaceContract(t *testing.T, open func(t *testing.T) WorkspaceFixture)` where the fixture exposes `Workspace`, `Write(w, path, content)`, `Symlink(w, path, target)`, `Delete(w, path)`, `Lock(session)`, `MoveTip(session)`.

- [ ] **Step 1: Red tests** (contract on `FakeWorkspace`): "resolve a missing ref is ErrBadRef", "resolve in a repository without commit is ErrNoCommit", "distinct sessions give distinct workdirs and branches", "the same session twice is ErrExists and changes nothing", "create takes the lock until Release", "state is equal after a no-op and different after a write", "state lists a link with its target and mode", "diff includes new, changed and deleted files", "diff over the byte cap fails with a clear error", "remove of an unknown session is ErrNotOurs and deletes nothing", "remove with the lock held is ErrBusy", "remove with a moved tip is ErrMoved and deletes nothing", "remove twice succeeds", "inspect reports folder, branch, busy and tip".
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/kanjotest`; PASS.
- [ ] **Step 5: Commit** `test(kanjotest): workspace port, fake and contract`.

### Task 9: Artifacts port, fake and contract

**Files:** Modify `internal/core/delegate_ports.go` (`Artifacts`, `ArtifactName`: `ArtReportJSON`, `ArtReportTXT`, `ArtPatch`, `ArtAgentLog`, `ArtSetupLog`, `ArtTestLog`, `ArtCheckLog(n)`); create `internal/kanjotest/artifacts.go`, `internal/kanjotest/artifacts_test.go`.

- [ ] **Step 1: Red tests:** `ArtifactsContract` on `MemArtifacts`: "a name is created once", "write is whole or absent (a failing writer leaves nothing)", "a session that fails ValidSession is refused", "names outside the closed set are refused", "check logs are numbered from 1 and capped at 32".
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `test(kanjotest): artifacts port, fake and contract`.

### Task 10: `runfiles` adapter (Artifacts on disk)

**Files:** Create `internal/runfiles/runfiles.go`, `internal/runfiles/runfiles_test.go`.

**Behaviour:** `New(dataDir string, maxLog int64)`; files under `<dataDir>/runs/<session>/`, folder 0700, files 0600, created with `O_EXCL`; `Write` uses temp file plus rename; `Create` returns a writer capped at `maxLog` (extra bytes dropped, one marker line `[truncated]`); a link at `runs`, at the session folder or at a target file is refused.

- [ ] **Step 1: Red tests:** `ArtifactsContract` on the adapter; `TestFilesArePrivate` (modes), `TestLogIsCapped`, `TestRefusesALinkedRunsFolder`, `TestRefusesALinkedSessionFolder`, `TestWriteIsAtomic`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/runfiles`; PASS.
- [ ] **Step 5: Commit** `feat(runfiles): private, capped files for each run`.

### Task 11: AgentRunner port, scripted fake and contract

**Files:** Modify `internal/core/delegate_ports.go` (`AgentRunner`, `AgentRequest`, `AgentResult`, `ModelUsage{Model string; Tokens Tokens; CostUSD *float64}`); create `internal/kanjotest/agent.go`, `internal/kanjotest/agent_test.go`.

**Interfaces — Produces:** `func NewScriptedAgent(w *FakeWorkspace, launches ...[]AgentStep) *ScriptedAgent`; steps `Write(path, content)`, `Remove(path)`, `Symlink(path, target)`, `Succeed()`, `Fail(code)`, `ForgetFile(path)`, `TouchForbidden(path)`, `Loop()` (blocks until ctx ends, returns `Killed`), `ExhaustQuota()`, `ReportUsage(model, tokens, cost)`; the nth launch uses the nth step list (the retry is launch 2); `func AgentRunnerContract(t *testing.T, open func(t *testing.T) AgentFixture)`.

- [ ] **Step 1: Red tests:** the contract on `ScriptedAgent`: "a cancelled context ends the call within the bound and sets Killed", "a failing agent is a result, not an error", "the result carries no prompt or output text" (canary in the prompt, reflect over `AgentResult` fields: only numbers, bools, model ids), "Resume is accepted", "two calls in a row work", "usage nil means unavailable, not zero"; plus fake-specific tests for each step.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/kanjotest`; PASS.
- [ ] **Step 5: Commit** `test(kanjotest): agent runner port, scripted fake and contract`.

### Task 12: `Delegate`, happy path and row-first ordering

**Files:** Create `internal/core/delegate.go`, `internal/core/delegate_test.go` (tests use the kanjotest fakes through an external test package `core_test`).

**Interfaces — Produces:** `DelegateDeps{Journal RunJournal; Workspace Workspace; Sandbox Sandbox; Agent AgentRunner; Artifacts Artifacts; Store Store; Clock Clock; NewSession func(now time.Time) string}`; `DelegateRequest{Task, Adapter, Model, Prompt, Ref string; Expect, Forbid []string; Setup, Test string; Checks []string; Timeout, CommandTimeout, Grace time.Duration; BoxEnv []string; MaxPatchBytes int64}`; `DelegateOutcome{Session string; Result RunResult; Report RunReport; Text string}`; `func Delegate(ctx context.Context, d DelegateDeps, req DelegateRequest) (DelegateOutcome, error)`. `error` is for failures before a row exists (bad input, `ErrNoCommit`, `ErrBadRef`); everything after `Begin` is a verdict.

- [ ] **Step 1: Red tests:** `TestDelegateHappyPathOnFakes` (verdict `ok-to-review`, row completed, lock released, tip recorded), `TestDelegateWritesTheRowBeforeCreatingTheWorkspace` (fake workspace asserts the journal already holds a `running` row when `Create` runs), `TestDelegateCompletesTheRowWhenCreateFails` (`ErrExists`: row `failed`, `created` stays 0, nothing removed), `TestDelegateReleasesTheLockOnEveryPath`, `TestDelegateRejectsInvalidInputBeforeAnyRow` (bad label, bad path, empty model: error, journal empty), `TestDelegateNoCommitWritesNothing`, `TestDelegateUsesTheJournalClock` (started and finished times from the fixed clock).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** the skeleton: validate, `Resolve`, `Begin`, `Create`, `Created`, `Open`, setup, agent launch, `State`, `Finish`, `Release`, `Close` (deferred, in this order).
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(core): delegate a task on the ports, row first`.

### Task 13: Watchdog, interruption, quota, crash and setup failure

**Files:** Modify `internal/core/delegate.go`; tests in `delegate_test.go`.

**Behaviour:** the agent phase runs under `context.WithTimeoutCause(ctx, req.Timeout, errAgentTimeout)`; `Killed` plus that cause is `timeout` (`RunTimeout`, exit 124, reason `agent-timeout`); `Killed` with the parent context ended is `interrupted`; bookkeeping after an interrupt uses `context.WithoutCancel` bounded by 10 s of the injected clock-free timer (`context.WithTimeout` on the background-derived context); `Quota` is `quota-exhausted`; a non-zero exit is `failed` (`agent-failed`); a failing `--setup` is `failed` (`setup-failed`) and the agent is not launched.

- [ ] **Step 1: Red tests:** `TestAgentTimeoutIsVerdictTimeoutAndExit124` (looping agent, 50 ms timeout), `TestInterruptCompletesTheRowAsInterrupted` (cancel the parent context mid-launch; row `interrupted`, workspace kept, lock released), `TestInterruptStillWritesThePatchOfWhatExists`, `TestQuotaIsVerdictQuotaExhaustedWithExit1`, `TestAgentNonZeroExitFailsTheRun`, `TestFailingSetupSkipsTheAgent`, `TestTimeoutWinsOverFailed` (precedence), `TestCommandTimeoutIsAFailureNotExit124`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/core`; PASS.
- [ ] **Step 5: Commit** `feat(core): watchdog, interruption and quota verdicts`.

### Task 14: The single retry

**Files:** Modify `internal/core/delegate.go`; tests in `delegate_test.go`.

**Behaviour:** after launch 1, if expected files are missing and launch 1 ended normally (exit 0, not killed, no quota) and no retry yet: launch 2 with `Resume = true` and a fixed prompt template naming the missing paths (paths are re-validated with `ValidRepoPath`); `launches = 2`, `retries = 1`. The delay covers both launches together. Usage of both launches is summed per model.

- [ ] **Step 1: Red tests:** `TestRetriesOnceWhenAFileIsMissing` (agent forgets a file, then writes it: `ok-to-review`, `retries = 1`), `TestStillMissingAfterTheRetryFails` (`missing-files`, no third launch), `TestNoRetryAfterQuota`, `TestNoRetryAfterAKill`, `TestNoRetryAfterANonZeroExit`, `TestNoRetryWhenNothingIsMissing`, `TestRetryUsesTheSameSession` (`Resume` true, same `Session` in the request), `TestRetryPromptNamesOnlyValidatedPaths`, `TestTimeoutCoversBothLaunches`, `TestUsageOfBothLaunchesIsSummedPerModel`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(core): one retry in the same session for missing files`.

### Task 15: Judging the state: forbidden, stray, missing, links, names

**Files:** Create `internal/core/delegate_state.go`, `internal/core/delegate_state_test.go`; modify `internal/core/delegate.go`.

**Interfaces — Produces:** `func ChangedPaths(before, after State) []Change` (`Change{Path, Kind, Mode, Link}`); `func JudgeChanges(changes []Change, expect, forbid []string) Findings` (`Findings{Missing, ForbiddenTouched, Unexpected, UnsafeSymlinks []string; Invalid int}`); a forbidden path matches itself and everything below it, case-insensitively; any `.git` segment is forbidden; a link is unsafe when added or changed with an absolute target or one that leaves the worktree; names with control characters or invalid UTF-8 become `<invalid-name-N>`.

- [ ] **Step 1: Red tests:** `TestChangedPathsAddedModifiedDeleted`, `TestChangedPathsEqualTreesAreEmptyWithoutComparingEntries`, `TestForbiddenMatchesItselfAndChildren`, `TestForbiddenIsCaseInsensitive`, `TestForbiddenDoesNotMatchASiblingPrefix` (`src` vs `src2`), `TestDotGitIsAlwaysForbidden`, `TestStrayFilesAreChangesOutsideExpect`, `TestExpectedAndForbiddenTouchedIsForbidden`, `TestMissingExpectedFile`, `TestUnsafeSymlinkAbsoluteTarget`, `TestUnsafeSymlinkEscapingTarget`, `TestSafeRelativeSymlinkInsideIsAllowed`, `TestHostileFileNameIsReportedWithoutControlCharacters`; run-level: `TestForbiddenPathTouchedFailsTheRun`, `TestStrayFilesGiveCheckStrayFilesAndExit0`, `TestUnsafeSymlinkFailsTheRun`, `TestChecksSideEffectsAreNotJudged` (a file written by the fake `--test` is not stray).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(core): judge forbidden, stray, missing files and unsafe links`.

### Task 16: Patch, `--test` and `--check`

**Files:** Modify `internal/core/delegate.go`; tests in `delegate_test.go`.

**Behaviour:** `Diff(ctx, workdir, state1, dst, max)` (base..state1) is written through `Artifacts.Create(ArtPatch)` with `MaxPatchBytes` (over it: `failed`, `patch-too-large`, the partial patch removed by the adapter contract: nothing kept); then `--test`, then each `--check` in order via `Box.Exec` with `req.CommandTimeout`, each logged to its own artifact; a non-zero code or timeout is a failure (`test-failed`, `check-failed`), the next command still runs.

- [ ] **Step 1: Red tests:** `TestPatchIsWrittenForNewChangedAndDeletedFiles`, `TestPatchOverTheCapFailsTheRun`, `TestTestAndChecksRunInOrderInTheSameBox` (fake sandbox records the order and the single `Open`), `TestFailingTestStillRunsTheChecks`, `TestFailingCheckFailsTheRun`, `TestCommandsRunInTheWorkdirWithTheBoxEnvironment`, `TestCommandsAreNotRunWhenTheAgentWasKilled`, `TestEachCommandHasItsOwnLog`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(core): patch, test and check steps`.

### Task 17: Report, usage row and the canary

**Files:** Create `internal/core/runreport.go`, `internal/core/runreport_test.go`; modify `internal/core/delegate.go`.

**Interfaces — Produces:** `RunReport` (the JSON of the design, `json` tags, `schema_version` 1) and `func (r RunReport) Text(dataRel bool) string`; usage row per model of `AgentResult.Usage` (`source = adapter`, `marked = messages`, `messages = launches`), a session record (`task_source none`), written through `Store.Merge`; no row and `tokens.status = unavailable` when usage is nil.

- [ ] **Step 1: Red tests:** `TestReportJSONHasTheDocumentedFields` (golden file in `testdata/`), `TestReportTextEndsWithApplyFinishAndNote`, `TestReportHasNoCommandText` (strings `--test`, `--check`, `--setup` with a canary never appear), `TestReportStepsAreNamedNotQuoted` (`setup`, `agent`, `test`, `check-1`), `TestUsageRowIsWrittenForEachModel`, `TestUnavailableUsageIsNotAFailure` (no row, `status unavailable`, verdict unchanged), `TestFreeModelCostZeroIsKeptAsZero`, `TestUnknownCostIsNull`, `TestUsageRowPassesValidate`, `TestCanaryNeverInReportOrRows` (canary in prompt, in test command, in agent output, in file contents: absent from `report.json`, `report.txt`, rows, journal records).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/core`; PASS.
- [ ] **Step 5: Commit** `feat(core): report files and usage row for a run`.

### Task 18: `Reap` and `Finish`

**Files:** Create `internal/core/finish.go`, `internal/core/finish_test.go`.

**Interfaces — Produces:** `func Reap(ctx, d FinishDeps, rec RunRecord) (RunRecord, error)` (a `running` row whose `Inspect.Busy` is false is completed as `interrupted`); `FinishDeps{Journal; Workspace; Clock}`; `func Finish(ctx, d FinishDeps, session string, v LeadVerdict) (FinishOutcome, error)` with `FinishOutcome{Run RunRecord; AlreadyRecorded, WorkspaceGone bool}`; errors `ErrStillRunning`, `ErrOtherVerdict`, `ErrUnknownRun`. Order: find the run, `Reap`, refuse if busy, `Remove` (idempotent), `Judge`.

- [ ] **Step 1: Red tests:** `TestFinishRemovesThenRecords`, `TestFinishTwiceWithTheSameVerdictIsANoOp`, `TestFinishWithAnotherVerdictIsRefusedAndChangesNothing`, `TestFinishOfARunningRunIsRefused` (lock held), `TestFinishReapsACrashedRun` (row `running`, lock free: completed as `interrupted` then judged), `TestFinishWhenTheWorkspaceIsAlreadyGone`, `TestFinishWithAMovedTipDeletesAndRecordsNothing`, `TestFinishNeverRemovesWhatTheJournalDoesNotName` (session unknown to the journal: `ErrUnknownRun`, `Remove` never called), `TestFinishRefusesAbandoned`, `TestFinishRemoveOnlyWhenCreated` (`created = 0`: no `Remove`), `TestFinishAfterACrashBetweenRemoveAndJudgeCompletes`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(core): reap crashed runs and record the lead verdict`.

### Task 19: `Clean`

**Files:** Create `internal/core/clean.go`, `internal/core/clean_test.go`.

**Interfaces — Produces:** `func Candidates(ctx, d CleanDeps) ([]Candidate, error)` (`Candidate{Run RunRecord; Presence Presence; State string}` where state is `running`, `finished`, `crashed`; only runs with no lead; read-only: no `Reap`, no write); `func Clean(ctx, d CleanDeps, cs []Candidate) []CleanResult` (per candidate: `Reap`, `Remove`, `Judge(abandoned)`; a busy run is skipped; one failure does not stop the others).

- [ ] **Step 1: Red tests:** `TestCandidatesAreRunsWithoutALead`, `TestCandidatesDoNotWrite` (journal wrapped to fail on any write), `TestCandidatesIgnoreBranchesTheJournalDoesNotKnow` (fake workspace holds `delegate/foreign`; never listed, never removed), `TestCleanSkipsUnrecordedBranches`, `TestCleanNeverTouchesALockedRun`, `TestCleanRecordsAbandoned`, `TestCleanReapsACrashedRunFirst`, `TestCleanContinuesAfterOneFailure`, `TestCleanTwiceInParallelRecordsOnce`, `TestCleanLeavesRunsWithALead`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(core): list and clean abandoned runs`.

### Task 20: `Summarize`

**Files:** Create `internal/core/summary.go`, `internal/core/summary_test.go`.

**Interfaces — Produces:** `type Summary struct{ Runs, Tasks, Unlabelled, Launches, Retries int; Duration time.Duration; Script map[RunVerdict]int; Lead map[LeadVerdict]int; NoLead int }`; `func Summarize(recs []RunRecord, task string, since time.Time) Summary`.

- [ ] **Step 1: Red tests:** `TestSummaryTotalsOnAHandComputedJournal`, `TestSummaryCountsDistinctTasksNotRuns`, `TestSummaryCountsUnlabelledRunsSeparately`, `TestSummaryFiltersByTaskAndSince`, `TestSummaryCountsRunsWithoutALead`, `TestSummaryOfNothingIsZeros`, `TestSummaryCountsRunningRuns` (verdict `running` counted under its own key).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(core): summary of the run journal`.

### Task 21: `gitworktree` adapter, part 1: Resolve, Create, State, Diff

**Files:** Create `internal/gitworktree/git.go`, `internal/gitworktree/workspace.go`, `internal/gitworktree/integration_test.go` (`//go:build integration`).

**Behaviour:** `New(repo, dataDir string)`. Every git call goes through one helper that sets `-c core.hooksPath=/dev/null -c core.fsmonitor=false`, `--no-ext-diff --no-textconv` where relevant, a clean environment (`GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`, `LC_ALL=C`), and `--` before every path or ref. `Resolve` uses `rev-parse --verify --end-of-options <ref>^{commit}`. `Create` takes the `flock` on `worktrees/<session>.lock` (non-blocking, `O_NOFOLLOW`), refuses an existing folder or branch (`ErrExists`), runs `worktree add -b delegate/<session> <dir> <commit>`. `State` builds a temporary index (`GIT_INDEX_FILE`), `git add -A`, `write-tree`, `ls-tree -r -z`, and reads link targets with `cat-file`. `Diff` is `diff --cached --binary --no-renames <base>` on the temporary index, copied through a byte limit.

- [ ] **Step 1: Red tests** (real temporary repository, `-tags integration`): `WorkspaceContract` against the adapter (parts that need `Remove` are skipped until Task 22 by a compile-time flag in the fixture); `TestIntegrationResolveOfARepoWithoutCommit` (`ErrNoCommit`), `TestIntegrationHostileRefIsData` (`--upload-pack=x`, `$(x)`), `TestIntegrationHooksDoNotRun` (a `post-checkout` hook and a `pre-commit` hook write a marker file; none runs during `Create`, `State`, `Diff`), `TestIntegrationStateDoesNotFollowSymlinks` (link to a file outside: entry is mode 120000, target text, content not read), `TestIntegrationStateDoesNotTouchTheRealIndex` (`git status` identical before and after), `TestIntegrationDiffIncludesNewDeletedAndBinaryFiles`, `TestIntegrationCreateRefusesAnExistingBranch`, `TestIntegrationCreateRefusesAnExistingFolder`, `TestIntegrationIgnoredFilesAreNotInTheState`.
- [ ] **Step 2: Run** `go test -tags integration ./internal/gitworktree`; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race -tags integration ./internal/gitworktree`; PASS.
- [ ] **Step 5: Commit** `feat(gitworktree): disposable worktrees with a link-safe state and diff`.

### Task 22: `gitworktree` adapter, part 2: Inspect, Remove, the lock

**Files:** Modify `internal/gitworktree/workspace.go`, `internal/gitworktree/integration_test.go`.

**Behaviour:** `Inspect` and `Remove` accept only an `Owned` whose `Branch`/`Dir` equal `BranchOf`/`DirOf(Session)`; the folder must be a real directory (no link at any component below the data folder, `Lstat`), its resolved path must equal the derived one and appear in `git worktree list --porcelain` on that exact branch; the lock is tried without waiting (`ErrBusy`); the branch tip must equal `Owned.Tip` (`ErrMoved`); then `worktree remove --force`, `branch -D`, and the lock file is removed. Anything absent is skipped (idempotent). A mismatch is `ErrNotOurs`, deleting nothing.

- [ ] **Step 1: Red tests:** `WorkspaceContract` fully, plus `TestIntegrationRemoveRefusesForeignBranch` (a user branch `delegate/mine` and a user worktree never match any `Owned`), `TestIntegrationRemoveRefusesMovedTip`, `TestIntegrationRemoveRefusesAReplacedFolder` (folder swapped for a link to another worktree), `TestIntegrationRemoveDoesNotFollowLinks` (a link inside the worktree to a folder with a canary file: canary survives), `TestIntegrationRemoveDeletesDirtyWorktreeAndItsBranch`, `TestIntegrationRemoveTwiceSucceeds`, `TestIntegrationLockIsHeldWhileRunning` (second process-like handle gets `ErrBusy`), `TestIntegrationLockIsFreedWhenTheHolderIsKilled` (child process holds it, killed, then `Remove` works), `TestIntegrationInspectReportsPresence`, `TestIntegrationMainRepositoryIsUntouched` (branches, worktree list and status of the main checkout equal before and after a full create, change, remove).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race -tags integration ./internal/gitworktree`; PASS.
- [ ] **Step 5: Commit** `feat(gitworktree): remove only what the journal recorded`.

### Task 23: `scriptagent` (test-only agent)

**Files:** Create `internal/scriptagent/scriptagent.go`, `internal/scriptagent/scriptagent_test.go`.

**Behaviour:** `New(script string)` validates the file (absolute after resolution, regular, executable, owned by the user, not group- or world-writable) and refuses a path inside `Dir` at run time. `Run` executes it through `Box.Exec` with the prompt on stdin, environment variables `KANJO_SESSION`, `KANJO_MODEL`, `KANJO_RESUME` (added to the box environment), working folder = the box `Dir`. Exit code 75 means quota exhausted (documented, so tests can simulate it). No usage: `Usage` is nil. The package comment says it is a test aid and never runs code from the repository.

- [ ] **Step 1: Red tests:** `AgentRunnerContract` with a `FakeSandbox` fixture; `TestRefusesAScriptInsideTheWorkdir`, `TestRefusesAWritableScript`, `TestRefusesANonExecutableScript`, `TestRefusesALinkedScript`, `TestPromptGoesToStdinNotArgv` (fake sandbox records `Cmd`; canary absent from `Argv`), `TestExit75IsQuota`, `TestResumeIsPassedInTheEnvironment`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/scriptagent`; PASS.
- [ ] **Step 5: Commit** `feat(scriptagent): test-only agent that runs a script you wrote`.

### Task 24: `kanjo run`

**Files:** Create `internal/cli/run.go`, `internal/cli/run_test.go`, `docs/commands/run.md`; modify `internal/cli/cli.go` (register `run`), `internal/cli/docs_test.go`.

**Behaviour:** flags of the design; validation before any file is touched (exit 2); config via `setup`; opens the store (write); builds the deps (`sqlitestore`, `runfiles`, `nosandbox`, `gitworktree`, `scriptagent`, `sysclock`, crypto `NewSession`); box environment = `PATH`, `LANG`, `LC_ALL` if set, plus `delegate.env_pass`; prompt read with the cap; `signal.NotifyContext` (SIGINT, SIGTERM) in `Run` only, `env.ctx` in tests; prints `report.txt` with absolute paths, the warning `no sandbox: the agent and the commands run with your rights` on stderr; exit code from `RunVerdict.ExitCode`.

- [ ] **Step 1: Red tests** (cli tests with fakes injected through `env`; no git): `TestRunRejectsHostileFlagsBeforeCreatingAnything` (bad task, `../x`, `-x` ref, path with newline, empty model, unknown agent, `--script` with another agent, 65 `--expect`), `TestRunWithoutRequiredFlagsIsAUsageError`, `TestRunWarnsThatThereIsNoSandbox`, `TestRunDoesNotPassCallerSecrets` (host `SECRET_TOKEN`; the script writes its environment to a file outside the worktree; absent), `TestRunExitCodes` (0 ok, 0 stray, 1 failed, 124 timeout, 1 quota), `TestRunOutputCannotCarryEscapes`, `TestCanaryNeverInRunOutputReportOrDatabase`, `TestRunPromptFileOverTheCapIsRefused`, `TestRunPrintsApplyAndFinishHints`; docs test `TestEveryCommandHasAPageDocumentingItsFlags` fails until `run.md` exists.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** and write `docs/commands/run.md`.
- [ ] **Step 4: Run** `go test -race ./internal/cli`; PASS.
- [ ] **Step 5: Commit** `feat(cli): kanjo run`.

### Task 25: `kanjo finish`

**Files:** Create `internal/cli/finish.go`, `internal/cli/finish_test.go`, `docs/commands/finish.md`; modify `internal/cli/snapshot.go` (extract `runSnapshot(ctx, cfg, book, st, clock) (SnapshotLog, error)` used by `snapshot` and `finish`), `internal/cli/cli.go`, `internal/cli/docs_test.go`.

**Behaviour:** `finish <session> --verdict V`; session validated (exit 2); calls `core.Finish`; prints the verdict line, `workspace removed` or `already removed`, the apply command `git -C '<repo>' apply '<patch>'` (single-quote escaped) unless `rejected`, then runs the snapshot (a failure is a warning on stderr, exit stays 0) and prints the task totals as `usage --task` does (skipped for task `-`). Exit 1 for `ErrStillRunning`, `ErrOtherVerdict`, `ErrMoved`, `ErrUnknownRun`.

- [ ] **Step 1: Red tests:** `TestFinishPrintsTheApplyCommandQuoted` (path with a space and a quote), `TestFinishRejectedPrintsNoApplyCommand`, `TestFinishRunsASnapshotAndPrintsTheTaskTotals`, `TestFinishSnapshotFailureIsAWarning`, `TestFinishTwiceSameVerdictExits0`, `TestFinishOtherVerdictExits1`, `TestFinishUnknownSessionExits1WithoutRemoving`, `TestFinishBadSessionAndBadVerdictExit2`, `TestFinishAbandonedIsAUsageError`, `TestFinishOutputCannotCarryEscapes`, `TestSnapshotCommandIsUnchanged` (existing tests keep passing).
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** and write `docs/commands/finish.md`.
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(cli): kanjo finish`.

### Task 26: `kanjo summary`

**Files:** Create `internal/cli/summary.go`, `internal/cli/summary_test.go`, `docs/commands/summary.md`; modify `internal/cli/cli.go`, `internal/cli/docs_test.go`.

**Behaviour:** opens the database read-only; a missing database or a version 2 database prints `no runs` and exits 0; `--task`, `--since YYYY-MM-DD`; table output through the existing renderer.

- [ ] **Step 1: Red tests:** `TestSummaryPrintsTheTotals`, `TestSummaryBeforeAnyRunCreatesNothing`, `TestSummaryOnAVersion2DatabaseSaysNoRuns`, `TestSummaryFilters`, `TestSummaryOnAReadOnlyDataFolder`, `TestSummaryBadSinceExits2`, `TestSummaryRefusesALinkedDatabase`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** and write `docs/commands/summary.md`.
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(cli): kanjo summary`.

### Task 27: `kanjo clean`

**Files:** Create `internal/cli/clean.go`, `internal/cli/clean_test.go`, `docs/commands/clean.md`; modify `internal/cli/cli.go`, `internal/cli/docs_test.go`.

**Behaviour:** without `--yes`: read-only database, read-only git, prints one line per candidate (`session task state dir branch`) or `nothing to clean`, creates nothing; with `--yes`: opens the store for writing, calls `core.Clean`, prints one result line per candidate, exit 1 if any removal failed or was refused, exit 0 otherwise (also when a busy run was skipped, which is reported as `skipped (running)`).

- [ ] **Step 1: Red tests:** `TestCleanWithoutYesChangesNothing` (database bytes and workspace unchanged), `TestCleanWithoutYesOnAReadOnlyFolder`, `TestCleanYesRemovesAndRecordsAbandoned`, `TestCleanSkipsARunningRun`, `TestCleanNothingToDoExits0`, `TestCleanListsOnlyRecordedRuns`, `TestCleanOutputCannotCarryEscapes`, `TestCleanOneFailureExits1AndContinues`.
- [ ] **Step 2: Run**; FAIL.
- [ ] **Step 3: Implement** and write `docs/commands/clean.md`.
- [ ] **Step 4: Run**; PASS.
- [ ] **Step 5: Commit** `feat(cli): kanjo clean`.

### Task 28: End-to-end integration on a real repository

**Files:** Create `internal/cli/e2e_delegate_test.go` (`//go:build integration`); modify `.github/workflows/ci.yml` (one step `go test -race -count=1 -tags integration ./...`) and `CLAUDE.md` (the command in the Commands block).

**Behaviour:** real `git` in a temporary repository, `nosandbox`, a shell script as the agent. No model, no container.

- [ ] **Step 1: Red tests:** `TestIntegrationRunAcceptFinish` (acceptance criterion of spec 11: report, usage row where the script reports none = no row, patch applies cleanly with `git apply --check` on the main checkout, nothing left after `finish`: no folder, no branch, no lock), `TestIntegrationRetryForMissingFile`, `TestIntegrationForbiddenPathFails`, `TestIntegrationStrayFileGivesCheckStrayFiles`, `TestIntegrationTimeoutExits124AndKillsChildren` (script spawns a background `sleep`; no process left), `TestIntegrationSigintLeavesAFinishableRun` (send SIGINT to a child `kanjo` process built with `go build`; row `interrupted`; `finish` works), `TestIntegrationCleanDuringARun` (a long script; `clean --yes` skips it and the run completes), `TestIntegrationCleanRemovesACrashedRun` (kill -9 the child `kanjo`), `TestIntegrationFinishTwice`, `TestIntegrationRepoWithoutCommit`, `TestIntegrationMainCheckoutIsUntouched` (status, branches and worktree list of the repository equal before and after, apart from the run's own), `TestIntegrationUnrecordedBranchSurvivesClean` (a user branch `delegate/mine` and a user worktree survive `clean --yes` and `finish`).
- [ ] **Step 2: Run** `go test -tags integration ./internal/cli`; FAIL.
- [ ] **Step 3: Fix** whatever the tests expose; no new feature.
- [ ] **Step 4: Run** `go test -race -count=1 -tags integration ./...`; PASS.
- [ ] **Step 5: Commit** `test(cli): end-to-end delegation on a real repository`.

### Task 29: Documentation

**Files:** Create `docs/delegation.md`. Modify `docs/data-model.md`, `docs/exit-codes.md`, `docs/security.md`, `docs/adding-an-adapter.md`, `docs/configuration.md` (already done in Task 5; check), `docs/spec.md` (sections 5.3, 5.4 wording; section 8: tables `runs` and `leads`, shared `Clock`), `docs/README.md`, `CLAUDE.md` (check the integration command from Task 28).

- [ ] **Step 1: Red test:** add `TestEveryPortIsDocumented` in `internal/cli/docs_test.go` (every interface in `internal/core/delegate_ports.go` and `ports.go` appears in `docs/adding-an-adapter.md`, with its fake and contract names) and `TestSecurityTestsExist` (every `Test...` named in the `docs/security.md` Test column of the new rows exists in the repository, searched by name). Run; FAIL.
- [ ] **Step 2: Write the docs.** `delegation.md`: lifecycle, verdict table, retry, files of a run, `report.json` and `report.txt` formats, watchdog, edge cases, the risk of running without a sandbox. `security.md`: the 20 threat rows with their real test names, residual risks of the design, trim "Not yet built" to what is still not built. `data-model.md`: version 3, `runs`, `leads`, run usage rows, data folder files, the backup warning (copy `kanjo.db` before the first `run`; update every kanjo).
- [ ] **Step 3: Run** `go test -race ./...`; PASS.
- [ ] **Step 4: Commit** `docs: delegation core, run files, security and data model`.

### Task 30: Gates and review

- [ ] **Step 1:** `go vet ./... && gofmt -l .`, `go test -race -count=1 ./...`, `go test -race -count=1 -tags integration ./...`, `govulncheck`.
- [ ] **Step 2:** `crapper --changed` (< 10 per function), `dryer internal/kanjotest` and the new packages (< 0.8), `mutator --changed --no-coverage --mutate-all`: kill each survivor with a red test or document it as equivalent.
- [ ] **Step 3:** `/code-review`, `/simplify`, then a security review of the full diff against the 20 threats. Each finding: red test, fix, commit.
- [ ] **Step 4:** write ONE cairn note for the slice (`cairn add`): changes, decisions, open points, helpers with path and role (`core.Delegate`, `core.Finish`, the five fakes and contracts, `gitworktree`, `nosandbox`). Only after the user validated the plan and the work.
- [ ] **Step 5:** report to the user what was not verified (Linux, the CI step, real concurrency on slow disks) and wait for the merge decision. Never push, merge or open a pull request unasked.

# Adding an adapter

The core (`internal/core`) holds the data model, cost and aggregation. It
imports no adapter and makes no system call. Everything outside (transcripts,
database, price file, clock, later: agent CLIs, git, containers, the web page)
sits behind an interface and is assembled in `internal/cli`.

## Ports

Defined in `internal/core/ports.go` (usage) and `internal/core/delegate_ports.go`
(delegation).

| Port | Methods | Adapters today | Fake (`internal/kanjotest`) | Contract suite |
|---|---|---|---|---|
| `UsageSource` | `Read(ctx) (Batch, error)` | `claudecode.Source` | `StaticSource` | `UsageSourceContract(t, src)` |
| `Store` | `Merge(ctx, Batch, SnapshotLog) error`, `Rows(ctx)`, `Sessions(ctx)` | `sqlitestore.Store` | `MemStore` | `StoreContract(t, open)` |
| `PriceBook` | `Lookup(model, longContext) (Price, bool)`, `LongContext(model, promptTokens) bool` | `pricesfile.Book` | `TablePrices` | `PriceBookContract(t, pb, knownModel)` |
| `Clock` | `Now() time.Time` | `sysclock.Clock` | `FixedClock` | none |
| `Journal` | `Update(ctx, session, fn) error`, `Markers(ctx)` | `sqlitestore.Store` | `MemJournal` | `JournalContract(t, open)` |
| `RunJournal` | `Begin`, `Created`, `Finish`, `Judge`, `Runs` | `sqlitestore.Store` | `MemRunJournal` | `RunJournalContract(t, open)` |
| `Sandbox` | `Open(ctx, BoxSpec) (Box, error)` | `nosandbox.Sandbox` | `FakeSandbox` | `SandboxContract(t, open, probe)` |
| `Box` | `Exec(ctx, Cmd) (ExecResult, error)`, `Close()` | `nosandbox` | `FakeSandbox` | `SandboxContract` |
| `Workspace` | `Resolve`, `Create`, `State`, `Diff`, `Release`, `Inspect`, `Remove` | `gitworktree.Workspace` | `FakeWorkspace` | `WorkspaceContract(t, open)` |
| `Artifacts` | `Create(ctx, session, name)`, `Write(ctx, session, name, data)` | `runfiles.Files` | `MemArtifacts` | `ArtifactsContract(t, open)` |
| `AgentRunner` | `Run(ctx, Box, AgentRequest) (AgentResult, error)` | `scriptagent.Agent` | `ScriptedAgent` | `AgentRunnerContract(t, open)` |

`Batch` is `{Rows []UsageRow, Sessions []SessionInfo, Skipped int}`. Rows and
sessions must pass `Validate()` (rules in [data-model.md](data-model.md)); every
row's session must be listed in `Sessions`.

## Add a usage source (another agent CLI)

1. Create `internal/<name>/` with a type that implements `core.UsageSource`.
2. Fill `UsageRow.Source` with a new lowercase name (`[a-z][a-z0-9-]{0,31}`).
3. Never store prompt, answer or code. Keep only counts, model ids, session ids, task labels and UTC days. Skip and count unreadable input (`Batch.Skipped`); return an error only for a bad configuration or a cancelled context.
4. Do not list a session that produced no rows (listing it would let `Merge` treat it as seen).
5. Test it with fixtures and run the contract:

```go
func TestSourceContract(t *testing.T) {
	kanjotest.UsageSourceContract(t, mySource)
}
```

6. Wire it in `internal/cli` (build it from config, then call `core.Snapshot(ctx, src, store, clock)`). The core is not edited.

## Add an agent (delegation)

An `AgentRunner` runs one launch of an agent inside a `Box`.

1. Create `internal/<name>/` with a type that implements `core.AgentRunner`.
2. A failing, timed-out or killed agent is a **result** (`AgentResult.Exit`, `Killed`), not an error; the error is for an agent that could not be started. A quota-exhausted agent sets `Quota`.
3. The result carries numbers, codes and model ids only: never the prompt or the output. Output goes to `AgentRequest.Log`.
4. Usage: `nil` means unavailable, an empty-looking `ModelUsage` with zero tokens means zero. `CostUSD` is nil when unknown and 0 for a free model.
5. The prompt reaches the agent in memory or on its standard input, never as an argument (other users see arguments in the process list). The agent runs its commands through the box (`Box.Exec`), which owns the watchdog.
6. Run the contract with a `FakeSandbox` box (`kanjotest.AgentFixture`): `kanjotest.AgentRunnerContract(t, open)`. `New(AgentReportsUsage)` may return nil when the adapter cannot report usage.
7. Wire it in `internal/cli/run.go`. The core is not edited.

## Add a sandbox (container)

Implement `core.Sandbox` and `core.Box` and run `kanjotest.SandboxContract(t,
open, probe)` with the commands of your environment (`SandboxProbe`: exit with a
code, sleep, print the environment, start a child, ignore the polite signal,
tell whether a child is alive). The contract fixes the watchdog: a cancelled
context stops the command **and its children** (polite signal, then a forced one
after `Cmd.Grace`) and sets `Killed`; the environment is exactly `BoxSpec.Env`
plus the command's own `Cmd.Env`; `Open` refuses a relative or empty folder;
nothing runs after `Close`. `nosandbox` is the reference: it uses a process
group, which a descendant that calls `setsid` can leave (a container closes
that).

## Add a workspace

Implement `core.Workspace` and run `kanjotest.WorkspaceContract(t, open)`, where
`open(t, commits)` returns a `WorkspaceFixture` over a repository holding
`README.md` and `gone.txt` (no commit when `commits` is false) plus the means to
write, link, delete, hold the lock and move the branch from outside. The
contract fixes the deletion rules: only a workspace named by `core.Owned`
(branch and folder derived from the session) is touched; a held lock is
`ErrBusy`; a moved branch tip is `ErrMoved`; a foreign folder is `ErrNotOurs`;
removing twice succeeds; `State` never follows a link. The real adapter has its
tests behind the build tag `integration` (`go test -tags integration ./...`).

## Add a store

Implement `core.Store`, then:

```go
func TestStoreContract(t *testing.T) {
	kanjotest.StoreContract(t, func(t *testing.T) core.Store { return openFresh(t) })
}
```

The contract fixes the merge rule: an incoming row replaces the stored row with
the same source, session, day, model and long-context flag whatever its task,
unless the stored row counts more messages (then the stored row stays and the
incoming one is dropped); every other stored row is kept. Also provide a
read-only open that creates and writes nothing, for report commands.

## Add a price book

Implement `core.PriceBook` and run `kanjotest.PriceBookContract(t, pb, "some-priced-model")`.
An unknown model must return `ok == false`, never a zero price.

## Rules

- `internal/core` must not import any adapter package and must not call `time.Now`, `os`, or `net`.
- Tests never call a real model, a real container or the network (use `httptest` servers on localhost).
- Configurable values go in `internal/config` and [configuration.md](configuration.md), not in code. A test (`TestEveryConfigKeyIsDocumented`) fails when a key is undocumented; another does the same for command options.
- Run `go vet ./...`, `go test -race ./...`, and the quality tools listed in `CLAUDE.md`.

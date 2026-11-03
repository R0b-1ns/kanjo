# Adding an adapter

The core (`internal/core`) holds the data model, cost and aggregation. It
imports no adapter and makes no system call. Everything outside (transcripts,
database, price file, clock, later: agent CLIs, git, containers, the web page)
sits behind an interface and is assembled in `internal/cli`.

## Ports

All defined in `internal/core/ports.go`.

| Port | Methods | Adapters today | Fake (`internal/kanjotest`) | Contract suite |
|---|---|---|---|---|
| `UsageSource` | `Read(ctx) (Batch, error)` | `claudecode.Source` | `StaticSource` | `UsageSourceContract(t, src)` |
| `Store` | `Merge(ctx, Batch, SnapshotLog) error`, `Rows(ctx)`, `Sessions(ctx)` | `sqlitestore.Store` | `MemStore` | `StoreContract(t, open)` |
| `PriceBook` | `Lookup(model, longContext) (Price, bool)`, `LongContext(model, promptTokens) bool` | `pricesfile.Book` | `TablePrices` | `PriceBookContract(t, pb, knownModel)` |
| `Clock` | `Now() time.Time` | `sysclock.Clock` | `FixedClock` | none |

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

## Add a store

Implement `core.Store`, then:

```go
func TestStoreContract(t *testing.T) {
	kanjotest.StoreContract(t, func(t *testing.T) core.Store { return openFresh(t) })
}
```

The contract fixes the merge rule: an incoming row replaces the stored row with
the same source, session, day, model and long-context flag whatever its task;
every other stored row is kept.

## Add a price book

Implement `core.PriceBook` and run `kanjotest.PriceBookContract(t, pb, "some-priced-model")`.
An unknown model must return `ok == false`, never a zero price.

## Rules

- `internal/core` must not import any adapter package and must not call `time.Now`, `os`, or `net`.
- Tests never call a real model, a real container or the network (use `httptest` servers on localhost).
- Configurable values go in `internal/config` and [configuration.md](configuration.md), not in code. A test (`TestEveryConfigKeyIsDocumented`) fails when a key is undocumented; another does the same for command options.
- Run `go vet ./...`, `go test -race ./...`, and the quality tools listed in `CLAUDE.md`.

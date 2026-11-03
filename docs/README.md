# kanjo documentation

kanjo measures AI consumption. It reads Claude Code transcripts, saves
usage in a local SQLite database, estimates cost at API list prices, and
reports tokens and dollars per task, model, day or session, in the terminal or
on a read-only local page, for one project or several. Everything stays
on the machine; only counts, model names, identifiers and dates are stored.

Founding document: [spec.md](spec.md) (what the finished tool must do). The
pages below describe what exists now.

## Pages

| File | Topic |
|---|---|
| [commands/snapshot.md](commands/snapshot.md) | `kanjo snapshot`: read transcripts into the data folder |
| [commands/usage.md](commands/usage.md) | `kanjo usage`: report tokens and estimated cost |
| [commands/serve.md](commands/serve.md) | `kanjo serve`: read-only report page and `/api/data` on 127.0.0.1 |
| [commands/mark.md](commands/mark.md) | `kanjo mark`: say which task you are on |
| [markers.md](markers.md) | Task markers: scopes, which task a message gets, edge cases, recompute |
| [commands/run.md](commands/run.md) | `kanjo run`: hand a task to an agent in a disposable worktree |
| [commands/finish.md](commands/finish.md) | `kanjo finish`: record your verdict, remove the worktree, show the apply command |
| [commands/summary.md](commands/summary.md) | `kanjo summary`: totals of the run journal |
| [commands/clean.md](commands/clean.md) | `kanjo clean`: remove worktrees left by runs without a verdict |
| [delegation.md](delegation.md) | Delegation: lifecycle, verdicts, retry, files of a run, `report.json`, watchdog, edge cases |
| [commands/prices-check.md](commands/prices-check.md) | `kanjo prices check`: age and coverage of the price file |
| [projects.md](projects.md) | Several projects: the `projects` key, `usage --all`, the page scope |
| [commands/self-update.md](commands/self-update.md) | `kanjo self-update`: install the latest release |
| [commands/version.md](commands/version.md) | `kanjo version` |
| [configuration.md](configuration.md) | Config files, layers, every key, flags |
| [data-model.md](data-model.md) | Usage rows, SQLite schema, data folder, validation rules |
| [claude-transcripts.md](claude-transcripts.md) | How Claude Code transcripts are read; task attribution |
| [prices.md](prices.md) | `prices.json` format, cost formula, unpriced models |
| [adding-an-adapter.md](adding-an-adapter.md) | Ports, fakes, contract tests, wiring a new adapter |
| [release.md](release.md) | CI, release on every merge, checksums, attestation |
| [security.md](security.md) | Threats handled, with the test that covers each |
| [exit-codes.md](exit-codes.md) | Process exit codes |

## Quick start

```sh
kanjo snapshot                       # read ~/.claude/projects into ./.ai-usage/kanjo.db
kanjo usage                          # table by task
kanjo usage --by model --csv         # CSV by model
kanjo usage --since 2026-11-01 --task ABC-12
kanjo serve                          # read-only page on http://127.0.0.1:8787/
kanjo mark start ABC-12              # from now on, usage goes to ABC-12
kanjo mark end
kanjo run --agent script --script ./agent.sh --model m --prompt-file p.txt --expect src/a.go --task ABC-12
kanjo finish <session> --verdict accepted
kanjo summary                        # totals of the run journal
kanjo prices check                   # warns when prices are older than 30 days
kanjo usage --all                    # every project listed in the user config
```

`usage` reads only the saved data: run `snapshot` first (at the end of each
task is a good habit). The Claude cost is an estimate at API list prices, not a
subscription bill.

## Not built yet

Real model adapters (the only agent today is a test aid that runs a script you
wrote), container sandbox, spending caps, routing, `batch`, validated agent
returns, cost per accepted task, and automatic price updates. See
[spec.md](spec.md) and [delegation.md](delegation.md).

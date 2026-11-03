# kanjo

A command-line tool that measures what AI work costs. It reads Claude Code
usage from local transcripts, estimates the cost at public API prices, and
reports tokens and dollars per task, model, day and session. Delegating small
tasks to cheaper models, spending caps and a local web page are planned (see
[docs/spec.md](docs/spec.md)).

Everything stays on your machine: only counts, model names, identifiers and
dates are stored, never prompts, answers or code.

## Install

Download the binary for your platform from the latest GitHub release, check it
against `SHA256SUMS`, and put it on your `PATH`. Every merge to `main` that
passes the tests publishes a release. Once installed, update with:

```sh
kanjo self-update
```

## Use

```sh
kanjo snapshot                    # read Claude Code transcripts into ./.ai-usage/
kanjo usage                       # tokens and estimated cost by task
kanjo usage --by model --since 2026-11-01
kanjo usage --by day --csv
```

The Claude cost is an estimate at API list prices, not a subscription bill.

## Documentation

See [docs/](docs/README.md): commands, configuration, data model, price file
format, release process, security notes and how to add an adapter.

## License

MIT, see [LICENSE](LICENSE).

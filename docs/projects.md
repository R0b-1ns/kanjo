# Several projects at once

Usage stays **per project**: each project keeps its own database at
`<project folder>/.ai-usage/kanjo.db`, written by `kanjo snapshot` run in that
project. The global view only reads those databases.

## Choosing the projects

List the project folders in your **user** config
(`~/.config/kanjo/config.toml`, see [configuration.md](configuration.md)). The
key is a top-level array, written before any `[table]`:

```toml
projects = ["/home/me/work/app-a", "/home/me/work/app-b"]
```

| Rule | Why |
|---|---|
| Absolute folders only | No dependence on where kanjo runs |
| The folder's base name (`app-a`) is the project name: letters, digits, `.`, `_`, `-`, starting with a letter or digit, unique in the list | It is the only thing shown of a project, never its path |
| User config only: a repository `kanjo.toml` that sets `projects` is refused | A cloned repository must not choose which folders kanjo reads |
| The data is always `<folder>/.ai-usage/kanjo.db`; the project's own `kanjo.toml` is never read | That file is untrusted |

Why a config key and not a registry written by `snapshot`: the list is
explicit, `snapshot` never writes outside its own project, and a repository you
only cloned and snapshotted does not join the view by itself.

## How other projects' databases are read

Read-only, never created, never repaired: a link in place of the database is
refused, the schema is not trusted, and every stored value is checked again. A
project with no database yet counts as empty. A project that cannot be read
(corrupt file, link, invalid value) is named (never with its path) and skipped;
the others are still reported.

## In the terminal

```sh
kanjo usage --all                     # one line per project
kanjo usage --all --by model          # models summed over all projects
kanjo usage --all --by day --since 2026-11-01 --csv
```

`--by` is `project` (default), `task`, `model` or `day`; `session` is refused
because session ids can collide across projects. A project that cannot be read
prints `warning: project NAME could not be read` on stderr. See
[commands/usage.md](commands/usage.md).

## In the page

Choose "All projects" in the scope list of [kanjo serve](commands/serve.md), or
call `/api/data?scope=all`. The page then adds a cost-per-project card and names
unreadable projects.

## Limits

- A warning about a session that named several tasks gives the session id without its project.
- The current folder is not added to the list by itself; add it if you want it in the global view.

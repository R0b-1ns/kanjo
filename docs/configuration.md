# Configuration

kanjo works with no config file. Defaults are used, then two optional TOML
files are applied in order, then command-line options.

## Layers (later wins)

| Order | Source | Trust | Notes |
|---|---|---|---|
| 1 | Built-in defaults | | Listed in the key table below |
| 2 | User file: `$XDG_CONFIG_HOME/kanjo/config.toml`, or `~/.config/kanjo/config.toml` when `XDG_CONFIG_HOME` is unset | trusted | Can point anywhere |
| 3 | Repository file: `./kanjo.toml` in the current folder | **untrusted** (it comes from whatever repository you are in) | Restricted, see below |
| 4 | Options | trusted | Only `--data-dir` overrides config today |

A missing file is fine. A file that exists but cannot be read, is larger than
1 MiB, is not valid TOML, has an unknown key, or has an invalid value is an
error that names the file; kanjo exits 1 and never silently ignores it.
Arrays replace, they do not merge. Relative paths resolve against the current
folder.

## Keys

| Key | Type | Default | Allowed values | Meaning |
|---|---|---|---|---|
| `data_dir` | string | `.ai-usage` | non-empty path | Folder holding `kanjo.db`, its `.gitignore` and the default task map |
| `prices_file` | string | `""` (embedded prices) | path to a prices file | See [prices.md](prices.md) |
| `claude.projects_dir` | string | `~/.claude/projects` | path | Folder Claude Code writes transcripts to |
| `claude.project_prefixes` | array of strings | `[<current folder with every non-alphanumeric character replaced by "-">]` | 1 or more non-empty strings | A project folder is read when its name **starts with** one of these. The default is `-Users-me-Project-app` for `/Users/me/Project/app`, so it also matches `-Users-me-Project-app-api` |
| `claude.max_line_bytes` | integer | `67108864` (64 MiB) | greater than 0 | Longer transcript lines are skipped and counted, never loaded |
| `task.pattern` | string (regular expression, Go RE2 syntax) | `\b[A-Z][A-Z0-9]+-[0-9]+(\.[0-9]+)?\b` | any valid regex, or `""` to disable | Finds task labels such as `ABC-12` or `ABC-12.3` in the user's messages |
| `task.map_file` | string | `<data_dir>/claude-tasks.tsv` | path | Manual map "session id prefix, tab, task"; see [claude-transcripts.md](claude-transcripts.md) |

## What a repository `kanjo.toml` may set

A repository file is untrusted: opening a cloned repository must not make
kanjo write elsewhere or read other projects. These keys are refused (exit 1,
the message names the file) in `./kanjo.toml`; put them in the user file or
pass the option instead.

| Key | Rule in `./kanjo.toml` |
|---|---|
| `data_dir`, `prices_file`, `task.map_file` | The path, once symbolic links are resolved, must be the current folder or inside it. A dangling symbolic link is refused |
| `claude.projects_dir` | Not allowed |
| `claude.project_prefixes` | Every prefix must start with the current folder's own project name (the default prefix), so it can only widen to sub-folders of this project |
| other keys | Allowed |

## Examples

User file `~/.config/kanjo/config.toml`, reading every project of a workspace
and keeping data in one place:

```toml
data_dir = "/home/me/.local/share/kanjo"

[claude]
project_prefixes = ["-home-me-work"]
```

Repository file `./kanjo.toml`, a custom label pattern (`TASK-123`):

```toml
[task]
pattern = '\bTASK-[0-9]+\b'
```

Disable the label heuristic (every session is filed under `-` unless the task
map names it):

```toml
[task]
pattern = ""
```

## Notes

- `--data-dir` also moves the default task map (`<data_dir>/claude-tasks.tsv`).
- Claude Code names a project folder after its path with every character other than a letter or digit replaced by `-` (`/a/.b/c_d` becomes `-a--b-c-d`).
- Environment-variable and per-role routing configuration come with later slices.

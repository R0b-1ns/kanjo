# Configuration

kanjo works with no config file. Defaults are used, then two optional TOML
files are applied in order, then command-line options.

## Layers (later wins)

| Order | Source | Trust | Notes |
|---|---|---|---|
| 1 | Built-in defaults | | Listed in the key table below |
| 2 | User file: `$XDG_CONFIG_HOME/kanjo/config.toml`, or `~/.config/kanjo/config.toml` when `XDG_CONFIG_HOME` is unset | trusted | Can point anywhere |
| 3 | Repository file: `./kanjo.toml` in the current folder | **untrusted** (it comes from whatever repository you are in) | Allow-list of keys, see below |
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
| `claude.project_prefixes` | array of strings | `[<current folder with every non-alphanumeric character replaced by "-">]` | 1 or more non-empty strings | A project folder is read when its name **equals** a prefix or **starts with the prefix plus `-`**. The default is `-Users-me-Project-app` for `/Users/me/Project/app`: it reads `-Users-me-Project-app` and `-Users-me-Project-app-api`, not `-Users-me-Project-app2`. A prefix ending in `-` reads only the sub-names. Claude Code writes `/` and `-` the same way, so a sibling folder `app-legacy` cannot be told from the sub-folder `app/legacy` |
| `claude.max_line_bytes` | integer | `67108864` (64 MiB) | greater than 0 | Longer transcript lines are skipped and counted, never loaded |
| `task.pattern` | string (regular expression, Go RE2 syntax) | `\b[A-Z][A-Z0-9]+-[0-9]+(\.[0-9]+)?\b` | any valid regex, or `""` to disable | Finds task labels such as `ABC-12` or `ABC-12.3` in the user's messages |
| `task.map_file` | string | `<data_dir>/claude-tasks.tsv` | path | Manual map "session id prefix, tab, task"; see [claude-transcripts.md](claude-transcripts.md) |

## What a repository `kanjo.toml` may set

A repository file is untrusted: opening a cloned repository must not make
kanjo write elsewhere, read other projects, run out of memory, or copy a secret
you typed into a prompt into the database (a repository-chosen `task.pattern`
could do that). Only these keys are accepted in `./kanjo.toml`; any other key is
refused (exit 1, the message names the file and the key). Put the others in the
user file or pass the option.

| Key | Rule in `./kanjo.toml` |
|---|---|
| `data_dir`, `prices_file`, `task.map_file` | The path, once symbolic links are resolved, must be the current folder or inside it. A dangling symbolic link is refused |
| `claude.project_prefixes` | Every prefix must equal the current folder's own project name (the default prefix) or start with it followed by `-`, so it can only widen to sub-folders of this project |
| `claude.projects_dir`, `claude.max_line_bytes`, `task.pattern` | Not allowed (user config only) |

The default `data_dir` (`.ai-usage`) follows the same rule when no user config
or `--data-dir` sets it: if `.ai-usage` is a symbolic link leading outside the
current folder, kanjo refuses to start.

## Examples

User file `~/.config/kanjo/config.toml`, reading every project of a workspace
and keeping data in one place:

```toml
data_dir = "/home/me/.local/share/kanjo"

[claude]
project_prefixes = ["-home-me-work"]
```

User file (a repository `kanjo.toml` may not set `task.pattern`), a custom label pattern (`TASK-123`):

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

## The default task pattern is a heuristic

The default `task.pattern` matches any upper-case word, a dash and digits
(`ABC-12`). It also matches `UTF-8`, `SHA-256`, `GPT-4` or `CVE-2024` when they
appear in your messages, and the most frequent label wins, so a session can be
filed under the wrong task. Restrict it to your tracker's project keys, or use
the manual task map:

```toml
[task]
pattern = '\b(?:ABC|XYZ)-[0-9]+\b'
```

## Notes

- `--data-dir` also moves the default task map (`<data_dir>/claude-tasks.tsv`).
- Claude Code names a project folder after its path with every character other than a letter or digit replaced by `-` (`/a/.b/c_d` becomes `-a--b-c-d`).
- Environment-variable and per-role routing configuration come with later slices.

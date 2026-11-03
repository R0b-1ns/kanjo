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
| `task.ignore` | array of strings | `["UTF-8", "UTF-16", "SHA-1", "SHA-256", "SHA-512", "ISO-8601", "GPT-4"]` | no empty entry | Labels the heuristic never reports, compared exactly (case-sensitive, no pattern). Setting it replaces the default list; `[]` ignores nothing. **User file only** |
| `task.map_file` | string | `<data_dir>/claude-tasks.tsv` | path | Manual map "session id prefix, tab, task"; see [claude-transcripts.md](claude-transcripts.md) |
| `projects` | array of strings | `[]` | absolute folders, each with a distinct base name made of letters, digits, `.`, `_`, `-` | Projects read by `kanjo usage --all` and the page scope "all projects"; see [projects.md](projects.md). The data of a project is always `<folder>/.ai-usage/kanjo.db`. **User file only** |
| `serve.port` | integer | `8787` | 1 to 65535 | Port of `kanjo serve` on 127.0.0.1; `--port` overrides it. **User file only** |
| `prices.max_age_days` | integer | `30` | greater than 0 | `kanjo prices check` warns when the price file is strictly older than this. **User file only** |
| `markers.max_events` | integer | `100000` | greater than 0 | Most events (starts and ends) the marker journal may hold; `kanjo mark start` and `mark end` are refused beyond it. See [markers.md](markers.md). **User file only** |
| `delegate.timeout_s` | integer | `900` | greater than 0 | Watchdog of the agent phase in `kanjo run`, both launches together; `--timeout` overrides it. Past it the agent is killed and the run is `timeout` (exit 124). See [delegation.md](delegation.md). **User file only** |
| `delegate.command_timeout_s` | integer | `600` | greater than 0 | Time limit of each `--setup`, `--test` and `--check` command. **User file only** |
| `delegate.kill_grace_s` | integer | `5` | greater than 0 | Seconds between the polite stop (SIGTERM) and the forced one (SIGKILL) of a command that ran out of time. **User file only** |
| `delegate.max_log_bytes` | integer | `1048576` | greater than 0 | Cap of each log file of a run; the rest is dropped and a marker line says so. **User file only** |
| `delegate.max_patch_bytes` | integer | `16777216` | greater than 0 | Cap of `changes.patch`; a larger patch makes the run `failed`. **User file only** |
| `delegate.max_prompt_bytes` | integer | `1048576` | greater than 0 | Cap of the `--prompt-file`; a larger file is refused. **User file only** |
| `delegate.env_pass` | array of strings | `[]` | at most 32 names matching `[A-Za-z_][A-Za-z0-9_]{0,63}` | Names of caller environment variables forwarded to the agent and the commands of a run. Nothing else is inherited except `PATH`, `LANG` and `LC_ALL`; `HOME` and `TMPDIR` always point to private folders. **User file only** |

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
| `claude.projects_dir`, `claude.max_line_bytes`, `task.pattern`, `task.ignore`, `projects`, `serve.port`, `prices.max_age_days`, `markers.max_events`, `delegate.*` | Not allowed (user config only). A repository must not choose which other folders kanjo reads |

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

Read several projects, serve the page on another port and warn after 14 days
(user file only; `projects` is a top-level key and goes before any `[table]`):

```toml
projects = ["/home/me/work/app-a", "/home/me/work/app-b"]

[serve]
port = 9000

[prices]
max_age_days = 14
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
filed under the wrong task. The first ones are dropped by `task.ignore`; add the
others there (exact labels), restrict the pattern to your tracker's project
keys, or use the manual task map:

```toml
[task]
pattern = '\b(?:ABC|XYZ)-[0-9]+\b'
```

Extend the ignore list (it replaces the default, so repeat the defaults you want to keep):

```toml
[task]
ignore = ["UTF-8", "SHA-256", "CVE-2024"]
```

## Notes

- `--data-dir` also moves the default task map (`<data_dir>/claude-tasks.tsv`).
- Claude Code names a project folder after its path with every character other than a letter or digit replaced by `-` (`/a/.b/c_d` becomes `-a--b-c-d`).
- `projects` is documented in [projects.md](projects.md); `serve.port` in [commands/serve.md](commands/serve.md); `prices.max_age_days` in [commands/prices-check.md](commands/prices-check.md).
- Environment-variable and per-role routing configuration come with later slices.

# kanjo run

Hands one task to an agent in a disposable git worktree, checks the result, and
writes a report. It never commits, pushes or applies anything: you read the
report, apply the patch yourself if you want it, and record your verdict with
[finish](finish.md). The lifecycle, the verdicts and the files of a run are in
[../delegation.md](../delegation.md).

```
kanjo run --agent script --script FILE --model MODEL --prompt-file FILE
          [--task LABEL] [--ref REF] [--expect PATH]... [--forbid PATH]...
          [--setup CMD] [--test CMD] [--check CMD]... [--timeout SECONDS]
          [--repo DIR] [--data-dir DIR]
```

**There is no sandbox yet.** The agent, `--setup`, `--test` and `--check` run
with your rights and can read or change anything you can. Every run prints
`warning: no sandbox: the agent and the commands run with your rights`. The
only agent today is `script`, a test aid that runs a script you wrote.

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--agent` | none (required) | `script` | The agent adapter |
| `--script` | none (required with `--agent script`) | absolute path of a file | The script that plays the agent: it gets the prompt on standard input and `KANJO_SESSION`, `KANJO_MODEL`, `KANJO_RESUME` (1 on the retry, else 0) in its environment, runs in the worktree, and exits 75 when its quota is exhausted. It must be a regular executable (not a link) owned by you, not writable by group or others, and not inside `<data_dir>/worktrees` |
| `--model` | none (required) | model id: `[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}` | Model name handed to the agent and recorded with the usage |
| `--prompt-file` | none (required) | path of a file | The prompt. At most `delegate.max_prompt_bytes`, not empty. It reaches the agent on standard input, never as an argument |
| `--task` | none (usage is not counted on a task) | a task label, not `-` | Task the usage of the run is counted on, as if you had marked it |
| `--ref` | `HEAD` | a commit, branch or tag | Where the worktree starts. A value starting with `-` or holding a space is refused |
| `--expect` | none | repository-relative path; repeatable, at most 64 | A file the agent must create or change. Missing after the retry: `failed`. With at least one `--expect`, other changed files give `check-stray-files` |
| `--forbid` | none | repository-relative path; repeatable, at most 64 | A path the agent must not touch, with everything under it, compared without regard to case. `.git` is always forbidden. Touched: `failed` |
| `--setup` | none | shell command, at most 4096 bytes | Run in the worktree before the agent. Failing: `failed`, the agent is not started |
| `--test` | none | shell command | Run after the agent, in the same box |
| `--check` | none | shell command; repeatable, at most 32 | Run after `--test`, in order. A failure does not stop the next one |
| `--timeout` | `delegate.timeout_s` (900) | seconds, greater than 0 | Time the agent may run, both launches together. Past it the run is `timeout` |
| `--repo` | the current folder | a git working tree root | The repository to delegate in |
| `--data-dir` | `data_dir` from the config | a folder path | Folder holding `kanjo.db`, `worktrees/` and `runs/` |
| `-h` | | | Print the options and exit 0 |

Paths of `--expect` and `--forbid` are slash separated, without `..`, empty
segment or control character, at most 256 bytes. Commands come from the command
line only and are never stored anywhere. Limits and delays are set in the user
config, see [../configuration.md](../configuration.md) (`delegate.*`).

## Output

The report, one fact per line, then nothing else. The last lines are the
commands to apply the patch and to record your verdict, with absolute paths:

```
task: ABC-12
session: ses_20261103T120000_a1b2c3
verdict: ok-to-review
...
patch: '/work/app/.ai-usage/runs/ses_20261103T120000_a1b2c3/changes.patch'
apply: git apply '/work/app/.ai-usage/runs/ses_20261103T120000_a1b2c3/changes.patch'
finish: kanjo finish ses_20261103T120000_a1b2c3 --verdict accepted|modified|rejected
note: ABC-12 ses_20261103T120000_a1b2c3 ok-to-review 8.12s
```

Warnings (a file that could not be written, usage that could not be saved) go to
stderr. File names with control characters are shown as `<invalid-name-N>`.

## Exit codes

| Code | Case |
|---|---|
| 0 | Verdict `ok-to-review` or `check-stray-files` |
| 1 | Verdict `failed`, `quota-exhausted` or `interrupted`; no commit in the repository; `--ref` is not a commit; the folder is not a git working tree root; a database, data folder or prompt file problem |
| 2 | Missing or invalid option, prompt file empty or over the cap |
| 124 | Verdict `timeout` |

Exit codes 3, 4 and 5 are reserved ([../exit-codes.md](../exit-codes.md)).

## Examples

```sh
echo "Add the parse function to src/parse.go" > /tmp/prompt.txt
kanjo run --agent script --script ~/bin/my-agent.sh --model test-model \
  --prompt-file /tmp/prompt.txt --task ABC-12 \
  --expect src/parse.go --forbid migrations --test "go test ./..." --check "go vet ./..."
kanjo finish ses_20261103T120000_a1b2c3 --verdict accepted
```

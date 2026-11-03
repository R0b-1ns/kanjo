# Specification: AI delegation and consumption tracking tool (kanjo)

Standalone project: its own repository, installable, with its own docs and
tests. A throwaway prototype (a shell delegation script, a Python tracking
script and a price file) served as a behavioral reference; it is not to be
reused as is. This document describes what the tool must do, not how to code it.

## 1. Goal

A single command-line tool that:

- delegates small tasks to cheaper models (through opencode) in an isolated
  environment, and checks the result itself;
- measures token consumption per model and per task, for delegated agents
  ("juniors", via opencode) as well as for Claude Code (without going through
  opencode);
- estimates the cost with the public Anthropic prices;
- shows everything on a local web page with charts;
- bounds and isolates: a consumption cap per task, agents run in a container,
  agent returns validated against a schema;
- measures what matters: the cost per accepted task, not only tokens.

End goal: know, per task (slice) and per model, what each method costs, to
decide which roles to delegate to which models.

### Out of scope

- No model call to compute anything.
- No real billing: the Claude cost is an estimate at API prices, not what a
  subscriber pays.
- No hosted service, no account, no data sent off the machine.
- Never decides in place of the lead: it does not commit, push, merge or apply
  anything without an explicit command.

## 2. Principles

- **Launching does not mean succeeding.** The verdict comes from checks made
  by the tool (files, tests, forbidden paths), never from what the model claims.
- **A missing measure is not a failure.** If tokens cannot be read, the task
  continues and the report says `unavailable`.
- **No content in tracking data.** Only counts, model names, identifiers and
  dates are stored, never a prompt, an answer or a code excerpt.
- **Everything is local**, in plain text files (CSV or SQLite), readable
  without the tool.
- **The core knows no tool.** Everything that touches the outside goes through
  an interface (section 2.1). opencode, Claude Code, git, the container
  runtime, the network proxy, storage and the web page are replaceable adapters.
- **Minimal dependencies.** Standard library as much as possible; no CDN in
  the web page.

### 2.1 Architecture: one core, interfaces, adapters

The core holds the logic (data model, cap policy, routing, verdicts, cost
computation, aggregations) and contains no tool name and no system call. It
never imports an adapter; adapters are chosen and assembled by configuration.
The core vocabulary is neutral ("agent run", "usage source"), not "opencode
session".

| Interface (port) | Role | Planned adapters | Fake for tests |
|---|---|---|---|
| AgentRunner | run an agent on a task, with a model, in a sandbox; return output, exit code, duration | opencode | scripted agent (succeeds, forgets a file, touches a forbidden path, loops, exhausts the quota, returns invalid JSON) |
| UsageSource | return normalized usage rows (section 4) | Claude Code transcripts; opencode session export | fixed row sets |
| Sandbox | create, start, watch and destroy the execution environment | container (section 12); none | in-memory sandbox recording requested accesses |
| Workspace | create and clean an isolated workspace, produce the patch | git worktree | temporary folder |
| PriceBook | price per model | `prices.json` file | fixed table |
| Store | read and write usage, tasks, markers, journal | files (CSV/SQLite) | memory |
| Clock | the time | system | fixed clock |
| Presenter | expose the data | local web server; text; CSV | none |

Rules: a new agent CLI or a new usage source is added without modifying the
core (one adapter plus one configuration line); each interface has a contract
test that all its adapters must pass; the whole core is testable with the
fakes, without network, container or opencode.

## 3. Data sources

This section describes the UsageSource adapters; the core only sees the
normalized rows of section 4.

### 3.1 Claude Code (without opencode)

Local JSONL transcripts: `~/.claude/projects/<project-folder>/<session>.jsonl`,
plus sub-agents in `<session>/subagents/*.jsonl`. The project folder is the
path with `/` replaced by `-`; the tool takes a configurable prefix to include
the root workspace and the repos.

Reading rules (learned on the prototype):

- Count only lines with `type == "assistant"` that have `message.usage` and
  `message.id`.
- Deduplicate by `message.id` (the same message appears on several lines, one
  per content block); the last copy wins.
- Ignore the `<synthetic>` model.
- Useful `usage` fields: `input_tokens`, `output_tokens` (thinking included),
  `cache_read_input_tokens`, and
  `cache_creation.ephemeral_5m_input_tokens` /
  `ephemeral_1h_input_tokens`. If the `cache_creation` detail is missing, put
  `cache_creation_input_tokens` in 5 min and 0 in 1 h.
- Message date: `timestamp` (UTC or local day; the choice is documented and constant).
- Sub-agents belong to the parent session.
- The format may change: an unknown field is ignored, an unreadable line is
  skipped and counted in a warning, never a fatal error.

### 3.2 Juniors launched by the tool (opencode)

After each launch, the tool exports the session (`opencode session export
<id>`, run in the working folder) and sums per model: `tokens.input`,
`tokens.output`, `tokens.reasoning`, `tokens.cache.read`, `tokens.cache.write`,
and `cost` (cost reported by opencode, in dollars). The model is the message's
`model.id`. A free model may have a cost of 0.

### 3.3 Extensible

An "adapter" interface (a function returning normalized usage rows) to add
another source (another agent CLI) later without touching the rest.

## 4. Data model

A normalized usage row:

| Field | Meaning |
|---|---|
| source | `claude` or `junior` (or another adapter) |
| session | session identifier |
| day | date YYYY-MM-DD |
| task | task label (section 6) |
| model | exact model identifier |
| messages | number of messages counted |
| input, output, reasoning | tokens |
| cache_write_5m, cache_write_1h, cache_read | tokens |
| cost_reported_usd | cost given by the source (juniors), otherwise empty |

Granularity: one row per (source, session, day, task, model). The task is part
of the key because a session may cover several (section 15). For a junior, one
row per launch and per model, with the duration, the number of launches and
retries, and the verdict in the delegated-tasks table (section 5).

Storage: a configurable data folder (default `./.ai-usage/`, ignored by git).
Atomic writes (temporary file then rename). The snapshot (section 7) replaces
the rows of the same key (session, day, task, model) with the most recent
version; it never deletes a row that the transcripts no longer contain (Claude
Code purges them after a few weeks).

## 5. Delegation (engine)

The engine is written against the AgentRunner, Workspace and Sandbox
interfaces. The details below ("opencode run, session ses_...") describe the
opencode adapter, not the core.

It reproduces the behavior of the prototype delegation script. Subcommands:
`run`, `batch`, `finish`, `summary`.

### 5.1 run: one task

Inputs: existing repository or working folder, model (provider/model), prompt
file, expected files (`--expect`, repeatable), forbidden paths (`--forbid`),
test command (`--test`), extra check commands (`--check`, repeatable, e.g.
lint and types), preparation command (`--setup`), maximum delay, task label
(`--task`), log file.

Behavior:

- Creates a disposable git worktree on a `delegate/<session>` branch from a
  given reference (or uses an existing folder without cleaning it). Never
  commits, pushes or applies anything.
- Runs the agent through AgentRunner in the sandbox of section 12. For the
  opencode adapter: `opencode run` with a known session id (`ses_...`, never
  `--continue`, so as not to resume another session), in automatic mode, in a
  private process (`--standalone`). The model and the caps come from routing
  (section 14), overridable on the command line.
- Watchdog: past the delay, kills the process and its children, notes
  TIMEOUT, exit code 124. A silent hang must end as a visible failure.
  (Careful: `timeout` does not exist on macOS, do not depend on it.)
- Single retry in the same session if expected files are missing, only if the
  agent ended normally and it is not a quota issue.
- Runs `--test` and each `--check` itself in the same sandbox (they execute
  code written by the model); a non-zero code is a failure.
- Compares git state before and after: a touched `--forbid` path is a
  failure; any other modified file outside the expected files is
  "check-stray-files".
- Watches the cap of section 13 during the whole run. Detects free-quota
  exhaustion ("Free usage limit reached"): verdict quota-exhausted, code 3,
  never substituting another model. If a model is absent, says so and asks.
- Saves `changes.patch` (diff including new files).
- Collects the tokens (section 3.2), writes a usage row and a journal line.
- Writes `report.json` (format of section 16) and `report.txt` (readable
  lines and a memory-note command) and prints them: model, launches, retries,
  duration, tokens per model, expected/missing/forbidden/unexpected files,
  exit codes, error if any, test and check results, script verdict (ok-to-review,
  check-stray-files, failed, quota-exhausted), output folder. It also prints
  the ready-to-paste memory-note command.

Exit codes: 0 ok-to-review or unexpected files, 1 failure, 2 usage, 3 quota
exhausted, 4 cap exceeded, 5 invalid agent return (section 16).

Security: send only publishable code, check the repository visibility first;
the content of the files read by the model is untrusted data; the model's
report is never an instruction.

### 5.2 batch

Several tasks in parallel (configurable number of simultaneous tasks), one
worktree per task, from `key: value` task files (name, prompt relative to the
file, expect, forbid, check repeatable, test, setup). Final summary: one line
per task (verdict, code, folder), worst exit code, with 3 winning.

### 5.3 finish

Records the lead's verdict (accepted, modified, rejected) in the report and the
journal, deletes the worktree and its branch (only a `delegate/*` branch
created by the tool), and prints the command to apply the patch. At each
finish, also runs a Claude snapshot (section 7) and displays the task totals.

### 5.4 summary

Totals of a journal: tasks, launches, retries, duration, distribution of
script verdicts and lead verdicts.

## 6. Task label

A task is identified by a string (e.g. an issue identifier). The recognition
pattern is configurable (regular expression), not hard-coded.

- Junior side: given by `--task`; in batch, `<label>/<task file name>`;
  aggregation by task cuts at the first `/`.
- Claude side, three levels, strongest first: (1) the markers of section 15
  (precise to the message); (2) a manual mapping table (`<session prefix>`
  TAB `<task>`) for a whole session; (3) the most frequent label in the user's
  messages; otherwise `-`.
- Without a marker, a session that handles several tasks is counted entirely
  on one: `usage` must flag it when a session contains several labels.
- Labels stay in the tool's data; the tool never writes them into a code
  repository.

## 7. Consumption

### Commands

| Command | Role |
|---|---|
| snapshot | Reads the Claude transcripts, merges with the already saved data (direct reading wins), writes the data file. Idempotent. To run at the end of each task, even without a junior. |
| usage | Totals in text or CSV: `--by task\|model\|day\|session\|role`, `--task`, `--since`. |
| serve | Local web page (section 9). |

### Estimated cost

Formula for one model: `(input × p_input + output × p_output + cache_write_5m
× p_w5 + cache_write_1h × p_w1 + cache_read × p_read) / 1,000,000`.

Prices live in a versioned `prices.json` file, with the source URL and the
retrieval date. Source: https://platform.claude.com/docs/en/about-claude/pricing
(retrieved 2026-11-03). Price per million tokens, in dollars:

| Model (identifier) | Input | Cache write 5 min | Cache write 1 h | Cache read | Output |
|---|---|---|---|---|---|
| claude-fable-5-1 | 10 | 12.50 | 20 | 0.25 | 50 |
| claude-opus-5-5 | 4 | 5 | 8 | 0.20 | 20 |
| claude-sonnet-5-5 | 2 | 2.50 | 4 | 0.10 | 10 |
| claude-haiku-5-5 (prompts <= 100k) | 0.10 | 0.125 | 0.20 | 0.01 | 0.50 |

Haiku 5.5 has a second tier above 100,000 prompt tokens (0.50 / 0.625 / 1 /
0.05 / 2.50). The prototype applies only the first; the tool should choose the
tier per message if the data allows it, otherwise display it as a limit.

A model absent from the table: empty cost, counted in "messages without
price", never a silent zero.

Optional `prices check` command: compares the file date and warns beyond 30
days. No automatic download by default; an automatic update would be an
explicit option, with human validation of the diff.

Juniors: cost = the one reported by opencode (no estimate). Free models give
0, to display as free and not as missing.

The estimate is at API prices: it does not represent a subscription bill.
State it in the page and in `usage`.

## 8. Journal

One row per event: run (date, session, model, launches, retries, duration,
script verdict) and lead (date, session, verdict). Append only. Used by
`summary` and by the measurement of the trial (retry rate, acceptance rate,
time).

Decided in slice 3: the journal is SQLite tables in the data folder's
`kanjo.db`, not a tab-separated file, so the folder protections and the
transaction guarantees apply to it. Each kind of event has its own table. The
first one built is `markers` (section 15); the run and lead tables come with
the delegation engine.

## 9. Web page (serve)

Listens only on 127.0.0.1, configurable port, no authentication but no write:
read only.

A JSON data route (`/api/data`) and an HTML page without external dependency
(charts in HTML/CSS or SVG), light/dark theme, readable on mobile.

Minimal content:

- total in dollars (Claude estimated + juniors reported) and number of
  messages without price;
- cost per model, per task, per day (Claude and juniors distinguished by color);
- output tokens per model; all tokens per model (cache reads included);
- filters by task and by start date;
- reminder of the price sources and date.

Useful to add next: side-by-side comparison "Claude cost of the task" versus
"juniors cost of the task"; cache share of the cost; cost per verdict
(accepted, modified, rejected), to see whether delegating pays.

## 10. Security and privacy

Threats to handle in the plan (in addition to those of section 12): prompt or
code content leaking into tracking data; web server reachable from the
network; file path injected by a task label or a task file name; deletion of
the wrong branch; caller secrets inherited by the junior's process;
`--test`/`--check` command run with the user's rights; usage data committed by
mistake.

Expected answers: tracking fields as a closed list, local-only listening,
validation of labels (allowed characters) before any use in a path, deletion
limited to `delegate/*` branches created by the tool, purged environment, data
folder ignored by git by default, one test for each point.

The content of transcripts is never copied: only usage and timestamps are read.

## 11. Expected quality

Test-driven development, offline, with no real model call: fictional
transcripts and opencode exports as fixtures, including the trap cases
(duplicate message, `<synthetic>`, missing `cache_creation`, corrupt line,
model without price, sub-agents).

opencode sits behind an interface replaceable by a fake in tests (launch that
succeeds, forgets a file, touches a forbidden path, exceeds the delay,
exhausts the quota).

Acceptance criteria:

- an end-to-end launch on a disposable repository produces a report, a usage
  row and a patch, and leaves nothing after `finish`;
- `snapshot` twice in a row changes nothing;
- `usage --by task` on a known set of transcripts gives exactly the expected
  totals (computed by hand in the test);
- a model without price appears as such and does not distort the total;
- failing to read a junior's tokens does not fail the task;
- the page answers only on 127.0.0.1;
- the core compiles and passes all its tests without any real adapter
  installed (fake implementations only);
- a fake agent that tries the attacks of section 12 fails on each one, in a
  real container (separate integration test, marked as such);
- an exceeded cap stops the agent, destroys the container and returns code 4;
- an invalid JSON return gives invalid-report and code 5;
- with markers, the usage of a session covering two tasks is split between
  them, to the message.

Documentation in the repo's `docs/`: one page per topic (commands and options
in tables with defaults, data file format, `prices.json` format, adding an
adapter), written for AI models (exact names, copyable examples,
self-contained).

Platforms: macOS first, Linux next. No GNU `timeout`, no GNU-specific
`sed`/`date` options.

## 12. Sandbox: container, created by script

Agents (and the test and check commands that run their code) run in a
container, behind the Sandbox interface. The runtime is an adapter (Docker,
Podman or other); the `none` adapter stays available for tests and debugging,
with a warning.

### Reproducible creation by script

- A versioned script in the repo (`sandbox/build`) builds the image from a
  Dockerfile: base pinned by digest, non-root user, needed tools (git, node,
  pnpm, the agent CLI) in fixed versions, no secret in the image.
- A `sandbox build` / `sandbox check` command calls this script, then checks
  the image (tools present, user, absence of secrets). Rebuilding twice gives
  the same image, or the tool reports it.
- A per-task start script creates the container, mounts, launches, retrieves
  the outputs and destroys everything (container, volumes, temporary network),
  even on failure or interruption.

### Default isolation policy

| Aspect | Rule |
|---|---|
| Files | only the task's worktree is mounted writable; toolchains read-only; no host HOME, no neighbor repository; temp in tmpfs |
| Agent state | state folder specific to the run, inside the container, never the host's (no access to other projects' sessions); exported before destruction to read the tokens |
| Secrets | only the key of the chosen model's provider, injected for this run (variable or in-memory temporary file), never written in the image or the logs |
| Privileges | non-root user, all capabilities dropped, no-new-privileges, read-only root filesystem |
| Resources | CPU, memory and process caps; maximum duration (the watchdog of section 5) |
| Network | closed by default. Outbound access goes through a proxy that allows only a list of domains (the model's provider). Test commands run without network; dependencies are installed beforehand by `--setup`, with a registry domain list specific to that step |

### Threats to test

A fake agent (test adapter) tries to: read host files (`~/.ssh`, `~/.aws`,
neighbor repositories), write outside the worktree, reach an unauthorized
domain, read another provider's key, start a process that outlives the run,
fill the disk or memory, escape through a symbolic link of the worktree. Each
attempt must fail or be contained, and be recorded in the report.

## 13. Consumption cap per task

Caps in tokens and/or dollars, per task and per run: `max_tokens`, `max_usd`.
Time (timeout) stays a separate cap.

Source of the value: routing (section 14), overridable by the command line;
the most specific value wins. A task cap covers all the task's runs, retries
included.

Applied to juniors: during the run, the tool periodically queries
UsageSource; on overrun it cleanly stops the agent and destroys the container.
Verdict budget-exceeded, code 4, the partial patch is kept for reading. The
real overrun can exceed the cap by one polling interval: document the interval
and the margin.

Applied to Claude: the tool cannot stop Claude Code. It alerts: `usage` and
the page flag that a task exceeds its cap (warning, never action).

An absent cap means "no cap", displayed clearly; there may be a global default
cap in the configuration.

## 14. Declarative, modular routing

A text configuration file (TOML or YAML) describes roles and their models; no
model is written in the code.

```toml
[roles.test-writer]
primary  = { adapter = "opencode", model = "provider/model-a", max_usd = 0.50, timeout_s = 600 }
fallback = [ { adapter = "opencode", model = "provider/model-b" } ]

[roles.review-logic]
primary  = { adapter = "opencode", model = "provider/model-c", max_tokens = 400000 }
```

Requirements:

- Easy to change: adding a role, changing a model or a cap only needs this
  file; no recompilation or code change.
- Layers (the most specific wins): tool defaults, user global file, repository
  file, environment variables, command line.
- Validation at load time with clear messages (unknown role, unknown adapter,
  missing model); `routing show` displays the resolved route of a role,
  `routing check` verifies that the models exist through the adapter.
- Explicit fallback: a fallback is used only if declared, and only for the
  declared causes (model unavailable, provider error; never for an exhausted
  free quota without agreement, existing rule). Each use of a fallback is
  recorded in the report (`fallback_used`). Configurable maximum depth (1 by
  default).
- A missing model without a declared fallback: the tool stops and says so, it
  substitutes nothing.

## 15. Task markers (mark)

`mark start <task>` and `mark end` write dated events in the append-only
journal (a `markers` table, section 8); `mark current` shows the active task.
No `--note` option: free text is where a secret ends up in stored data, and
nothing would read it. It can be added later with a bounded column.

Attribution of Claude usage: each message is assigned to the task active at
the instant of its timestamp. Markers win over the manual table and over the
text heuristic (section 6). Before the first marker or outside any interval,
the old rule applies.

A global marker by default (global within one data folder), with
`--session <id>` to bind it to a specific session (several Claude sessions in
parallel on different tasks). A session marker wins over the global one for
that session. A marker is stamped when the command runs; there is no
backdating, and a clock behind the last marker of the scope is refused. Details
and edge cases: `docs/markers.md`.

`usage` flags the messages attributed by heuristic rather than by marker, to
measure the reliability of the attribution.

The run, finish and mark journals share the clock of the Clock interface, to
align junior runs and Claude's working time on the same task. `snapshot` stays
idempotent: the attribution is a function of transcripts, markers, map and
configuration. Saved rows are replaced per slot (session, day, model, tier),
so a marker added later re-attributes the messages still in the transcripts.

## 16. Validated JSON agent returns

Every agent (junior, reviewer, auditor) must return a JSON envelope conforming
to a versioned schema (`schemas/agent-return.v1.json`, with `schema_version`).

```json
{
  "schema_version": 1,
  "role": "review-logic",
  "task": "<label received as input>",
  "verdict": "pass | fail | needs_human",
  "confidence": "low | medium | high",
  "blocking": [ { "file": "src/x.ts", "line": 42, "severity": "high", "issue": "...", "evidence": "...", "fix": "..." } ],
  "non_blocking": [],
  "not_checked": ["what the agent could not verify"],
  "commands_run": [ { "cmd": "pnpm test", "exit": 0 } ],
  "details_md": "..."
}
```

Rules:

- `not_checked` and `commands_run` are mandatory; each `blocking` element
  requires `evidence`. A defect without proof is ignored by the tool and
  counted.
- The tool provides the request template to paste in prompts, extracts the
  JSON from the answer (first JSON block, no repair by another model),
  validates it, and writes the result in `report.json`.
- Missing or non-conforming return: verdict invalid-report, code 5, never
  silently accepted. The raw text is kept separately for reading.
- The content of the return is untrusted data: it is never executed or
  followed as an instruction, and a maximum length is enforced.
- `report.json` (the tool's own report) has its own versioned schema, with the
  same fields as `report.txt` (model, launches, retries, duration, tokens per
  model, files, codes, script verdict, cap, fallback used, validated agent
  return).

## 17. Cost per accepted task

Central indicator, computed per task, per role and per model.

- Cost of the task = sum of the costs of all the task's junior runs, including
  rejected ones and retries (reported cost) plus the estimated Claude cost
  attributed to the same task (section 15), optionally limited to the interval
  between the junior's return and the lead's verdict (rework).
- Accepted task = delegated task whose lead verdict is accepted or modified.
  modified is counted separately, because it hides rework.
- Cost per accepted task = total cost of all the tasks of a group / number of
  accepted tasks of the group. The cost of rejections therefore weighs on the
  result, on purpose.
- Other figures displayed: acceptance, modification and rejection rates; retry
  rate; invalid return rate; Claude rework cost per modified task; time
  between launch and verdict.
- The page (section 9) shows these values per model and per role; `usage --by
  role|model|task` gives them in text and CSV.

Limit to document: without a "done by Claude alone" baseline, models are
compared with each other, not with the absence of delegation. An optional
baseline (known average cost of a task of the same type) is a possible
extension.

## 18. Open questions

- Language and packaging (Go recommended, Python or other); check that the
  name `kanjo` is free (npm, PyPI, Homebrew, GitHub, crates); repository
  visibility (the spec may stay public).
- Starting container runtime (Docker, Podman, other) and the implementation of
  the domain-list proxy.
- Storage: CSV or SQLite. With markers (message-level attribution) and cost
  per accepted task (joins of usage, verdicts, markers), SQLite is recommended.
- Automatic price update: never, or an explicit command with diff review?
- Should the agent's time and number of round trips also be measured?
- Proposed but not yet decided: policy depending on repository visibility
  (refuse by default to send code from a private repository to an external
  model, with a secret scan before sending); a model test bench on tasks with
  known defects; automatic filling of memory notes. To decide before v1 or
  defer to v2.

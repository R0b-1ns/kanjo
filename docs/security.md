# Security

What kanjo defends against, and the test that proves each point. Test
names are Go tests in the package shown.

Transcripts, config files, price files and server answers are **data**, never
instructions. Nothing from them is executed.

## Threats and responses

| Threat | Response | Test |
|---|---|---|
| Prompt, answer or code leaks into stored data or output | Transcript lines are decoded into typed structs of usage fields only; assistant text is never decoded; user text is scanned in memory and only matched task labels are kept | `claudecode.TestCanaryNeverInBatch`, `cli.TestCanaryNeverInOutputOrDatabase` (a marker string in the fixtures must appear nowhere in stdout, stderr or the database bytes) |
| Hostile ids or labels (path traversal, spreadsheet formulas, huge text) | Closed character sets and length caps on source, session, day, task, model; checked when a row is built, when it is saved and when it is read back | `core.TestUsageRowValidate`, `claudecode.TestInvalidSessionAndModelAreSkipped`, `claudecode.TestReadTaskMapRejectsBadLines` |
| Oversized or corrupt transcript lines | Bounded line reader discards over-long lines without loading them; bad lines are skipped and counted, never fatal | `claudecode.TestForEachLineDiscardsOversizedAndContinues`, `claudecode.TestForEachLineDiscardsHugeLineAcrossBuffers`, `claudecode.TestOversizedLineIsSkippedAndCounted` |
| Symbolic links in the transcripts folder | Never followed (files and folders) | `claudecode.TestSkipsSymlinks` |
| Usage data committed by mistake | Data folder mode 0700, database 0600, folder carries its own `.gitignore` (`*`), repository ignores `.ai-usage/` | `sqlitestore.TestOpenCreatesPrivateDirAndGitignore`, `sqlitestore.TestOpenKeepsExistingGitignore` |
| SQL injection | Parameterized statements only; stored values are re-validated on read | `sqlitestore.TestHostileValuesStayData` |
| Tampered or hostile price file | Strict decoding, unknown fields and negative or missing prices rejected, 1 MiB cap, `source` limited to 200 printable characters (no terminal escape sequences) | `pricesfile.TestParseRejects`, `pricesfile.TestLoad` |
| A cloned repository's `kanjo.toml` redirects data or reads other projects | Repository config may only name paths inside its own folder (symbolic links resolved, dangling links refused), may not set `claude.projects_dir`, and may only widen `claude.project_prefixes` to sub-names of its own folder | `config.TestRepoConfigCannotReachOutsideItsFolder`, `config.TestRepoConfigCannotEscapeThroughASymlink`, `config.TestUserConfigAndFlagsAreTrusted` |
| A cloned repository's `kanjo.toml` copies a typed secret into the database (`task.pattern`) or lifts the line cap | Allow-list of keys for the repository config; `task.pattern`, `task.ignore`, `claude.max_line_bytes`, `claude.projects_dir` are user-only | `config.TestRepoConfigMayNotSetUserOnlyKeys`, `config.TestRepoConfigCannotReachOutsideItsFolder` |
| A committed `.ai-usage` link, `kanjo.db` link, hostile `.gitignore` or crafted database redirects writes or makes the data committable | The default data folder is checked like a repository path; links inside the data folder and a `.gitignore` that does not ignore everything are refused; `trusted_schema` is off | `config.TestDefaultDataDirCannotBeASymlinkOutside`, `sqlitestore.TestOpenRefusesSymlinkedFiles`, `sqlitestore.TestOpenRefusesAGitignoreThatDoesNotIgnoreEverything`, `sqlitestore.TestDatabasesDoNotTrustTheirSchema` |
| Terminal control sequences in paths printed by kanjo | Paths and error text are printed with non-printable characters replaced by `?` | `cli.TestPrintedPathsCannotCarryTerminalEscapes` |
| A forged release answer points `self-update` at another repository on GitHub | Asset URLs must be under this repository's release path for the release tag | `selfupdate.TestAssetsMustBelongToTheRepositoryRelease`, `cli.TestProductionUpdaterOnlyTalksToGitHub` |
| A future commit date blocks later updates | The release tag is stamped with the build time | `selfupdate.TestReleaseWorkflowStampsTagsWithBuildTime` |
| An empty project prefix reads every project | Empty prefixes and empty prefix lists are rejected | `config.TestInvalidConfigIsRejected` |
| Unsafe task map entry | Prefix and task validated, errors name the file and line | `claudecode.TestReadTaskMapRejectsBadLines`, `cli.TestBadTaskMapExits1NamingFileAndLine` |
| A purged or partly purged transcript erases or shrinks saved history | Merge only replaces rows an incoming row supersedes and never with fewer messages; a session with no usable row is not recorded | `kanjotest` store contract ("rows the source no longer has are kept", "a smaller incoming row never shrinks saved usage"), `cli.TestSnapshotKeepsRowsOfPurgedTranscripts`, `cli.TestPartlyPurgedSessionNeverShrinksSavedUsage`, `claudecode.TestSessionWithoutUsableRowsIsNotListed` |
| A CSV cell that looks like a formula | Session ids, task labels and model ids cannot start with `-`, `=`, `+` or `@` | `core.TestUsageRowValidate` |
| Reading reports needs write access, or creates files | `usage` opens the database read-only and never creates the folder | `cli.TestUsageWorksOnAReadOnlyDataFolder`, `cli.TestUsageBeforeAnySnapshotCreatesNothing`, `sqlitestore.TestOpenReadOnlyReadsButNeverWrites`, `sqlitestore.TestOpenReadOnlyNeverCreatesAnything` |
| Reading another project's folder by a near-match prefix | A prefix matches a whole folder name or a `-` sub-name, in config and in a repository config | `claudecode.TestPrefixMatchesWholeNameOrSubFolderNames`, `config.TestRepoPrefixMustBeTheFolderNameOrASubName` |
| Downgrade through `self-update` | Only a release with a newer timestamp is installed | `selfupdate.TestOnlyANewerReleaseIsAnUpdate`, `cli.TestSelfUpdateNeverDowngrades` |
| Data folder cannot be written | Clear error naming the path, no partial database | `sqlitestore.TestOpenUnwritableDirFails` |
| Tampered download in `self-update` | The SHA-256 must match `SHA256SUMS` or nothing is replaced | `selfupdate.TestHashMismatchKeepsOldBinary` |
| `self-update` sent to another host | Only `https`, no credentials, allowed GitHub hosts, also after redirects (max 5) | `selfupdate.TestForeignHostsAreRefused`, `selfupdate.TestRedirectToForeignHostIsRefused`, `selfupdate.TestPlainHTTPAndUserinfoAreRefused`, `selfupdate.TestRedirectLimit`, `cli.TestProductionUpdaterOnlyTalksToGitHub` |
| Huge or endless download | Size caps (1 MiB API, 64 KiB checksums, 200 MiB binary), request timeout | `selfupdate.TestOversizedDownloadsAreRefused`, `selfupdate.TestExactLimitIsAccepted`, `selfupdate.TestClientDefaults` |
| Half-written binary | Temporary file next to the target, renamed only after the checksum matches, removed on any failure | `selfupdate.TestUnwritableDirectoryFailsCleanly`, `selfupdate.TestHashMismatchKeepsOldBinary` |
| Server text in error messages | Never echoed | `selfupdate.TestBadServerAnswers`, `cli.TestSelfUpdateFailureExits1` |
| The page is reachable from the network | The listen address is fixed to `127.0.0.1` (there is no option); `Serve` refuses any other listener | `web.TestListenLoopbackIsLoopbackOnly` (also dials this machine's LAN address), `web.TestServeRefusesANonLoopbackListener`, `cli.TestServeAnswersOnlyOnLoopbackWithItsOwnHost` |
| DNS rebinding (a foreign name resolving to 127.0.0.1) | `Host` must be exactly `127.0.0.1:<port>` or `localhost:<port>`, otherwise 403 | `web.TestHostMustBeLoopbackWithThePort` |
| Writes or side effects through the page | Only `GET` and `HEAD`; every handler is read-only | `web.TestOnlyGetAndHead`, `cli.TestServeLogsNothingAndCreatesNothing` |
| Cross-origin reads, framing, sniffing | No CORS header; strict CSP (`default-src 'none'`), `nosniff`, `no-referrer`, `Cross-Origin-Resource-Policy: same-origin`, `no-store` on every answer, errors included | `web.TestSecurityHeadersOnEveryAnswer` |
| Script injection through a session id, task, model or project name | JSON is encoded (markup stays escaped); the page uses `textContent` and SVG attributes only; no inline script or style, no external reference | `web.TestHostileValuesStayData`, `web.TestPageNeverBuildsMarkupFromData`, `web.TestPageHasNoInlineScriptOrStyle`, `web.TestPageHasNoExternalReferences` |
| Slow, huge or odd requests | Header, read, write and idle timeouts, 8 KiB headers, 1024-byte query cap, strict query parameters (each once, validated) | `web.TestServerLimits`, `web.TestLongQueryIs414`, `web.TestBadParametersAre400` |
| File listing, path or error-text leaks | Four fixed routes served from embedded files (no file server); loader errors are never echoed; project names are folder base names | `web.TestUnknownPathsAre404`, `web.TestLoaderErrorHidesTheDetail`, `cli.TestCanaryNeverInPageOrJSON`, `projects.TestFailureCarriesNoPath` |
| Port clash hides the real server | A busy port is an error, never a fallback; bad ports are usage errors | `web.TestListenLoopbackRefusesABusyPort`, `cli.TestServeBusyPortFailsWithoutFallback`, `cli.TestServeBadPort` |
| Another project's database is hostile (corrupt, linked, tampered values) | Opened read-only, never created, links refused, schema untrusted, values re-validated; the project is named as unreadable and the others still reported | `projects.TestCorruptDatabaseIsReportedAndOthersStillRead`, `projects.TestSymlinkedDatabaseIsRefusedNotFollowed`, `projects.TestHostileRowsInAnotherDatabaseMakeTheProjectFail`, `projects.TestReadNeverCreatesOrChangesAnything`, `cli.TestUsageAllWithABrokenProjectStillPrintsTheRest`, `cli.TestScopeAllNamesAnUnreadableProject` |
| A cloned repository chooses which projects are read, or the port or age limit | `projects`, `serve.port` and `prices.max_age_days` are user-config only; a project's own `kanjo.toml` is never read | `config.TestRepoConfigMayNotSetProjectsServeOrPrices`, `cli.TestRepoConfigProjectsAreRefusedByUsageAll` |
| Page and CLI disagree on totals | Both call `core.Aggregate` | `cli.TestPageAndCLIGiveTheSameTotalsForTheProject`, `cli.TestPageAndCLIGiveTheSameTotalsForAllProjects` |
| An unpriced model hidden as 0 on the page | `usd` is `null` and the group is labelled "no price"; the total ignores it | `web.TestUnpricedModelIsNullAndDoesNotChangeTheTotal` |
| Old prices go unnoticed | `prices check` warns strictly above `prices.max_age_days`, lists unpriced models, never downloads | `cli.TestPricesCheckAtThirtyDaysDoesNotWarn`, `cli.TestPricesCheckAtThirtyOneDaysWarns`, `cli.TestPricesCheckListsUnpricedModelsOfTheData` |
| Secrets in logs | kanjo never reads credentials and never logs content; errors name files, lines or counts only | covered by the canary tests above |

## Residual risks

- A symbolic link created between the config check and the use of the folder is not detected (check-then-use race). The check is exact on a static tree.
- A `kanjo.db` that a repository already tracks in git stays tracked despite `.gitignore`; kanjo does not run git. Do not run `snapshot` in a repository that commits its data folder.
- A user who sets `data_dir`, `task.pattern` or `claude.projects_dir` in their own config, or passes `--data-dir`, is trusted completely.
- A task label chosen by the user's own `task.pattern` is stored as is; do not use a pattern that can match secrets.
- Transcript files are opened by path after being listed; a file swapped for a symbolic link in between could be read. Only usage numbers are extracted from it.
- `self-update` trusts GitHub's TLS and the repository's release assets. The attestation proves origin but is checked by you, not by the command ([release.md](release.md)).
- `kanjo serve` has no authentication: any process of any user on the same machine can read the page and `/api/data` (counts, model names, task labels, project folder names). Do not run it on a shared host.
- A browser tab on another site cannot read the page (no CORS, `Host` check), but a local program can; the data is the same as `kanjo usage` prints.
- Commands run with your user rights. There is no sandbox yet.

## Not yet built

Delegation to other models, container isolation, secret handling for agents, spending caps and agent-return
validation. The threats for those are in [spec.md](spec.md) sections 10 and 12
and will be handled, with tests, in their own slices.

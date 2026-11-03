# Security

What this slice defends against, and the test that proves each point. Test
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
| An empty project prefix reads every project | Empty prefixes and empty prefix lists are rejected | `config.TestInvalidConfigIsRejected` |
| Unsafe task map entry | Prefix and task validated, errors name the file and line | `claudecode.TestReadTaskMapRejectsBadLines`, `cli.TestBadTaskMapExits1NamingFileAndLine` |
| A partly purged transcript erases saved history | Merge only replaces rows an incoming row supersedes; a session with no usable row is not recorded | `kanjotest` store contract ("rows the source no longer has are kept"), `cli.TestSnapshotKeepsRowsOfPurgedTranscripts`, `claudecode.TestSessionWithoutUsableRowsIsNotListed` |
| Data folder cannot be written | Clear error naming the path, no partial database | `sqlitestore.TestOpenUnwritableDirFails` |
| Tampered download in `self-update` | The SHA-256 must match `SHA256SUMS` or nothing is replaced | `selfupdate.TestHashMismatchKeepsOldBinary` |
| `self-update` sent to another host | Only `https`, no credentials, allowed GitHub hosts, also after redirects (max 5) | `selfupdate.TestForeignHostsAreRefused`, `selfupdate.TestRedirectToForeignHostIsRefused`, `selfupdate.TestPlainHTTPAndUserinfoAreRefused`, `selfupdate.TestRedirectLimit`, `cli.TestProductionUpdaterOnlyTalksToGitHub` |
| Huge or endless download | Size caps (1 MiB API, 64 KiB checksums, 200 MiB binary), request timeout | `selfupdate.TestOversizedDownloadsAreRefused`, `selfupdate.TestExactLimitIsAccepted`, `selfupdate.TestClientDefaults` |
| Half-written binary | Temporary file next to the target, renamed only after the checksum matches, removed on any failure | `selfupdate.TestUnwritableDirectoryFailsCleanly`, `selfupdate.TestHashMismatchKeepsOldBinary` |
| Server text in error messages | Never echoed | `selfupdate.TestBadServerAnswers`, `cli.TestSelfUpdateFailureExits1` |
| Secrets in logs | kanjo never reads credentials and never logs content; errors name files, lines or counts only | covered by the canary tests above |

## Residual risks

- A symbolic link created between the config check and the use of the folder is not detected (check-then-use race). The check is exact on a static tree.
- Transcript files are opened by path after being listed; a file swapped for a symbolic link in between could be read. Only usage numbers are extracted from it.
- `self-update` trusts GitHub's TLS and the repository's release assets. The attestation proves origin but is checked by you, not by the command ([release.md](release.md)).
- Commands run with your user rights. There is no sandbox yet.

## Not in this slice

Web server (planned: 127.0.0.1 only, read only), delegation to other models,
container isolation, secret handling for agents, spending caps and agent-return
validation. The threats for those are in [spec.md](spec.md) sections 10 and 12
and will be handled, with tests, in their own slices.

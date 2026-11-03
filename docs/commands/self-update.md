# kanjo self-update

Replaces the running `kanjo` binary with the latest GitHub release. This is the
only command that uses the network, and only when you run it.

```
kanjo self-update [--check]
```

## Options

| Option | Default | Values | Meaning |
|---|---|---|---|
| `--check` | off | flag | Only report whether a newer release exists. Downloads and installs nothing |
| `-h` | | | Print the options and exit 0 |

## Steps

1. GET the latest release from `https://api.github.com/repos/R0b-1ns/kanjo/releases/latest` (answer capped at 1 MiB).
2. If the latest release is not newer than the running version (`kanjo version`), stop: already up to date. Release tags are ordered by the timestamp in the tag, so a build as recent as or newer than the latest release is never replaced by an older one. A tag in another format (such as `dev`) counts as different and is replaced.
3. Find the assets `kanjo-<GOOS>-<GOARCH>` and `SHA256SUMS`. Missing: error.
4. Download `SHA256SUMS` (capped at 64 KiB) and read the checksum line of the binary.
5. Download the binary (capped at 200 MiB) into a temporary file in the same folder as the running binary, hashing as it is written.
6. If the SHA-256 differs from `SHA256SUMS`, delete the temporary file and fail: the running binary is untouched.
7. Mark the file executable (0755), flush it, and rename it over the running binary.

Safety rules: only `https` URLs without credentials on these hosts are
contacted, including after redirects (at most 5): `api.github.com`,
`github.com`, `objects.githubusercontent.com`,
`release-assets.githubusercontent.com`. Request timeout: 2 minutes. Server
error text is never echoed.

The checksum proves the download was not corrupted or swapped in transit; it
does not prove who built it. To check the origin, see
[../release.md](../release.md).

## Output

stdout, one line:

| Situation | Line |
|---|---|
| installed | `updated <old tag> -> <new tag>` |
| `--check`, newer release exists | `update available: <old tag> -> <new tag>` |
| not older than the latest release | `kanjo is up to date (<running tag>)` |

A development build (`kanjo version` prints `kanjo dev`) always sees the latest
release as newer.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Installed, up to date, or update available (`--check`) |
| 1 | Network error, bad answer, missing asset, checksum mismatch, folder of the binary not writable |
| 2 | Bad option or stray argument |

## Examples

```sh
kanjo self-update --check
kanjo self-update
```

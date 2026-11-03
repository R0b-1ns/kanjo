# Release

## Workflows

| File | When | What |
|---|---|---|
| `.github/workflows/ci.yml` | pull requests and pushes to `main` | `go mod verify`, `go vet ./...`, `go test -race -count=1 ./...`, `govulncheck`, `scripts/build.sh ci`. Read-only token |
| `.github/workflows/release.yml` | every push to `main` (a merge) | The same checks, then builds the binaries, attests them and creates a GitHub release. Concurrent runs are serialized |

Every merge whose checks pass becomes a release. There is no release PR.

## Tag

```
v0.0.0-<UTC yyyymmddhhmmss of the commit>-<12-character commit sha>
```

Example: `v0.0.0-20261103120000-a1b2c3d4e5f6`. It sorts by time. The binary is
built with this tag as its version, so `kanjo version` prints it and
`kanjo self-update` can compare it to the latest release.

## Assets of each release

| Asset | Content |
|---|---|
| `kanjo-darwin-amd64`, `kanjo-darwin-arm64`, `kanjo-linux-amd64`, `kanjo-linux-arm64` | Static binaries (`CGO_ENABLED=0`, `-trimpath`) |
| `SHA256SUMS` | `sha256sum` output for the four binaries |

The release notes hold the commit sha. A build provenance attestation is
published for the binaries.

## Permissions

The build job has a read-only token. Only the final job can write (contents,
attestations, id-token) and it only attaches files the build job produced,
after re-checking `SHA256SUMS`. Actions are pinned by commit sha.

## Installing by hand

```sh
tag=$(gh release view --repo R0b-1ns/kanjo --json tagName -q .tagName)
gh release download "$tag" --repo R0b-1ns/kanjo --pattern 'kanjo-darwin-arm64' --pattern SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing
chmod +x kanjo-darwin-arm64 && mv kanjo-darwin-arm64 ~/bin/kanjo
```

Later: `kanjo self-update` ([commands/self-update.md](commands/self-update.md)).

## Checking where a binary came from

`SHA256SUMS` only proves the file was not damaged or swapped in transit. To
verify that GitHub Actions built it from this repository:

```sh
gh attestation verify kanjo-darwin-arm64 --repo R0b-1ns/kanjo
```

`kanjo self-update` does not run this check itself.

## Building locally

```sh
scripts/build.sh            # version "dev"; writes dist/ and dist/SHA256SUMS
scripts/build.sh v1.2.3     # sets the version printed by `kanjo version`
```

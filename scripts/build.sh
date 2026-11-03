#!/bin/sh
# Build static binaries for every supported platform into dist/ with checksums.
# Usage: scripts/build.sh [version]
set -eu
version="${1:-dev}"
cd "$(dirname "$0")/.."
rm -rf dist
mkdir -p dist
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
	os="${target%/*}"
	arch="${target#*/}"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
		-ldflags "-s -w -buildid= -X github.com/R0b-1ns/kanjo/internal/cli.Version=${version}" \
		-o "dist/kanjo-${os}-${arch}" ./cmd/kanjo
done
(cd dist && if command -v sha256sum >/dev/null 2>&1; then sha256sum kanjo-* > SHA256SUMS; else shasum -a 256 kanjo-* > SHA256SUMS; fi)
cat dist/SHA256SUMS

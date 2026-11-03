# kanjo version

```
kanjo version
```

Prints one line to stdout and exits 0:

```
kanjo <version>
```

`<version>` is set at build time (`scripts/build.sh [version]`). A plain
`go build` prints `kanjo dev`. Release binaries print their release tag, which
has the form `v0.0.0-<UTC yyyymmddhhmmss>-<12-character commit sha>` (see
[../release.md](../release.md)). `kanjo self-update` compares this string with
the latest release tag.

The command takes no options.

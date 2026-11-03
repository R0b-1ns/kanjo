package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/R0b-1ns/kanjo/internal/selfupdate"
)

const latestReleaseURL = "https://api.github.com/repos/R0b-1ns/kanjo/releases/latest"

// releaseHosts are the only hosts self-update may contact: the GitHub API, the
// release page and the two hosts GitHub serves release files from.
var releaseHosts = []string{"api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}

func productionUpdater(exe string) selfupdate.Updater {
	return selfupdate.Updater{
		LatestURL: latestReleaseURL, AllowedHosts: releaseHosts,
		Current: Version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Exe: exe,
	}
}

// runningBinary is the path self-update would replace, with symlinks resolved.
// It is empty when unknown; self-update then refuses to install.
func runningBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

type selfUpdateOpts struct{ check bool }

func selfUpdateFlags(fs *flag.FlagSet) *selfUpdateOpts {
	o := &selfUpdateOpts{}
	fs.BoolVar(&o.check, "check", false, "only report whether a newer release exists; download and install nothing")
	return o
}

func selfUpdateCmd(args []string, stdout, stderr io.Writer, e env) int {
	fs := newFlagSet("self-update", stderr)
	o := selfUpdateFlags(fs)
	if code := parseFlags(fs, args); code != proceed {
		return code
	}
	ctx := context.Background()
	if o.check {
		latest, available, err := e.update.Check(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		if available {
			fmt.Fprintf(stdout, "update available: %s -> %s\n", e.update.Current, latest)
		} else {
			fmt.Fprintf(stdout, "kanjo is up to date (%s)\n", latest)
		}
		return 0
	}
	tag, updated, err := e.update.Update(ctx)
	switch {
	case err != nil:
		return fail(stderr, err)
	case updated:
		fmt.Fprintf(stdout, "updated %s -> %s\n", e.update.Current, tag)
	default:
		fmt.Fprintf(stdout, "kanjo is up to date (%s)\n", tag)
	}
	return 0
}

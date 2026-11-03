// Package gitworktree is the core.Workspace adapter over a real git
// repository: one disposable worktree and branch per run.
//
// Every git call is hardened the same way: hooks, fsmonitor and external diff
// or text conversion programs are off, the user's and the system's git
// configuration are ignored, nothing is asked on a terminal, the environment
// is rebuilt from nothing, and every reference or path that comes from outside
// is checked before it reaches git (ids are hex, branch names are derived from
// a validated session id).
package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// hardening is put before every git command.
var hardening = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.fsmonitor=false",
	"-c", "core.symlinks=true",
	"-c", "core.quotepath=off",
	"-c", "diff.noprefix=false",
	"-c", "diff.mnemonicPrefix=false",
}

const stderrLimit = 300

// git runs one command in dir and returns its standard output. extraEnv is
// added to the rebuilt environment. A failure carries the exit code (see
// exitCode) and a short, single-line copy of git's complaint.
func git(ctx context.Context, dir string, extraEnv []string, stdout io.Writer, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append(append([]string{}, hardening...), args...)...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
	}, extraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &limited{w: &errb, left: stderrLimit}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if err := cmd.Run(); err != nil {
		return nil, &gitError{args: args[:1], err: err, stderr: oneLine(errb.String())}
	}
	return out.Bytes(), nil
}

type gitError struct {
	args   []string
	err    error
	stderr string
}

func (e *gitError) Error() string { return fmt.Sprintf("git %s: %v: %s", e.args[0], e.err, e.stderr) }
func (e *gitError) Unwrap() error { return e.err }

// exitCode is the exit status of a failed git command, or -1.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
}

// limited keeps the first left bytes and drops the rest without failing.
type limited struct {
	w    io.Writer
	left int
}

func (l *limited) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > l.left {
		p = p[:l.left]
	}
	l.left -= len(p)
	l.w.Write(p)
	return n, nil
}

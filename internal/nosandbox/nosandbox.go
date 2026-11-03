// Package nosandbox is the core.Sandbox that runs commands on the machine with
// the user's own rights. It is a watchdog and an environment filter, not a
// container: the command can read and change anything the user can. A
// descendant that leaves the process group (setsid, double fork) survives the
// watchdog; only a real container closes that.
package nosandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// waitDelay bounds the wait for output pipes that a surviving child holds open.
const waitDelay = time.Second

// Sandbox implements core.Sandbox.
type Sandbox struct{}

// Open checks dir (absolute, an existing real directory, not a link) and
// creates the private HOME and TMPDIR folders of the box.
func (Sandbox) Open(_ context.Context, spec core.BoxSpec) (core.Box, error) {
	if !filepath.IsAbs(spec.Dir) {
		return nil, errors.New("box folder must be an absolute path")
	}
	fi, err := os.Lstat(spec.Dir)
	switch {
	case err != nil:
		return nil, fmt.Errorf("box folder: %w", err)
	case !fi.IsDir():
		return nil, errors.New("box folder is not a real directory")
	}
	private, err := os.MkdirTemp("", "kanjo-box-")
	if err != nil {
		return nil, err
	}
	home, tmp := filepath.Join(private, "home"), filepath.Join(private, "tmp")
	if err := errors.Join(os.Mkdir(home, 0o700), os.Mkdir(tmp, 0o700)); err != nil {
		os.RemoveAll(private)
		return nil, err
	}
	env := make([]string, 0, len(spec.Env)+2)
	for _, e := range spec.Env {
		if !strings.HasPrefix(e, "HOME=") && !strings.HasPrefix(e, "TMPDIR=") {
			env = append(env, e)
		}
	}
	env = append(env, "HOME="+home, "TMPDIR="+tmp)
	return &box{dir: spec.Dir, env: env, private: private}, nil
}

type box struct {
	dir, private string
	env          []string
	mu           sync.Mutex
	closed       bool
}

func (b *box) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// Close removes the private folders; it is safe to call twice.
func (b *box) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	return os.RemoveAll(b.private)
}

// Exec implements core.Box.
func (b *box) Exec(ctx context.Context, c core.Cmd) (core.ExecResult, error) {
	if b.isClosed() {
		return core.ExecResult{}, core.ErrBoxClosed
	}
	if (len(c.Argv) == 0) == (c.Shell == "") {
		return core.ExecResult{}, errors.New("a command needs exactly one of Argv and Shell")
	}
	argv := c.Argv
	if c.Shell != "" {
		argv = []string{"/bin/sh", "-c", c.Shell}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env, cmd.Stdin = b.dir, b.env, c.Stdin
	cmd.Stdout, cmd.Stderr = c.Out, c.Out
	if c.Out == nil {
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return core.ExecResult{}, fmt.Errorf("start command: %w", err)
	}
	killed := watch(ctx, cmd, c.Grace)
	// Whatever is left in the group is stopped: a command does not outlive its run.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return core.ExecResult{Exit: exitCode(cmd.ProcessState), Killed: killed, Duration: time.Since(start)}, nil
}

// watch waits for the command; when ctx ends first it sends SIGTERM to the
// group, then SIGKILL after grace. It reports whether ctx ended the command.
func watch(ctx context.Context, cmd *exec.Cmd, grace time.Duration) (killed bool) {
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
		return false
	case <-ctx.Done():
	}
	pgid := cmd.Process.Pid
	syscall.Kill(-pgid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}
	return true
}

// exitCode is the exit status, or 128 plus the signal as a shell reports it.
func exitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

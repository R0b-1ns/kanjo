package nosandbox_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/nosandbox"
)

func sh(script string) core.Cmd { return core.Cmd{Shell: script} }

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func pids(out string) []int {
	var ps []int
	for _, f := range strings.Fields(out) {
		if n, err := strconv.Atoi(f); err == nil {
			ps = append(ps, n)
		}
	}
	return ps
}

func TestSandboxContract(t *testing.T) {
	probe := kanjotest.SandboxProbe{
		Exit:       func(n int) core.Cmd { return sh("exit " + strconv.Itoa(n)) },
		Sleep:      func() core.Cmd { return sh("sleep 30") },
		PrintEnv:   func() core.Cmd { return core.Cmd{Argv: []string{"/usr/bin/env"}} },
		SpawnChild: func() core.Cmd { return sh("sleep 30 & echo $!; wait") },
		IgnoreTerm: func() core.Cmd { return sh("trap '' TERM; sleep 30") },
		ChildAlive: func(out string) bool {
			ps := pids(out)
			return len(ps) > 0 && alive(ps[0])
		},
	}
	kanjotest.SandboxContract(t, func(*testing.T, []string) core.Sandbox { return nosandbox.Sandbox{} }, probe)
}

func open(t *testing.T, env ...string) (core.Box, string) {
	t.Helper()
	dir := t.TempDir()
	box, err := nosandbox.Sandbox{}.Open(context.Background(), core.BoxSpec{Dir: dir, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { box.Close() })
	return box, dir
}

func envOf(t *testing.T, box core.Box) map[string]string {
	t.Helper()
	var out bytes.Buffer
	if _, err := box.Exec(context.Background(), core.Cmd{Argv: []string{"/usr/bin/env"}, Out: &out}); err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		k, v, _ := strings.Cut(l, "=")
		m[k] = v
	}
	return m
}

func TestEnvironmentIsExactlyTheSpec(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "hunter2")
	box, dir := open(t, "A=1")
	got := envOf(t, box)
	for k := range got {
		if k != "A" && k != "HOME" && k != "TMPDIR" && k != "PWD" {
			t.Errorf("unexpected variable %s", k)
		}
	}
	if got["A"] != "1" {
		t.Errorf("A = %q", got["A"])
	}
	for _, k := range []string{"HOME", "TMPDIR"} {
		p := got[k]
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s = %q: %v, %v", k, p, fi, err)
		}
		if rel, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(rel, "..") {
			t.Errorf("%s = %q is inside the working folder", k, p)
		}
	}
	if got["HOME"] == got["TMPDIR"] {
		t.Error("HOME and TMPDIR must differ")
	}
}

func TestSpecCannotOverridePrivateFolders(t *testing.T) {
	box, _ := open(t, "HOME=/etc", "TMPDIR=/etc")
	got := envOf(t, box)
	if got["HOME"] == "/etc" || got["TMPDIR"] == "/etc" {
		t.Fatalf("env = %v", got)
	}
}

func TestRunsInTheWorkingFolderWithStdin(t *testing.T) {
	box, dir := open(t)
	var out bytes.Buffer
	res, err := box.Exec(context.Background(), core.Cmd{Shell: "pwd; cat", Stdin: strings.NewReader("typed\n"), Out: &out})
	if err != nil || res.Exit != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	got, _ := filepath.EvalSymlinks(lines[0])
	if got != want || lines[len(lines)-1] != "typed" {
		t.Fatalf("out = %q, want dir %q", out.String(), want)
	}
}

func TestExitCodePassesThrough(t *testing.T) {
	box, _ := open(t)
	res, err := box.Exec(context.Background(), sh("kill -9 $$"))
	if err != nil || res.Exit != 137 {
		t.Fatalf("res %+v err %v, want 137", res, err)
	}
	if res, _ := box.Exec(context.Background(), core.Cmd{Argv: []string{"/bin/sh", "-c", "exit 9"}}); res.Exit != 9 {
		t.Fatalf("argv exit = %d", res.Exit)
	}
	if _, err := box.Exec(context.Background(), core.Cmd{Argv: []string{"/nonexistent/program"}}); err == nil {
		t.Fatal("a program that cannot start must be an error")
	}
}

func TestKillsTheWholeGroup(t *testing.T) {
	box, _ := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var out bytes.Buffer
	// a child shell and its own child: both must die
	res, err := box.Exec(ctx, core.Cmd{Shell: "sh -c 'sleep 30 & echo $!; wait' & echo $!; wait", Out: &out, Grace: 100 * time.Millisecond})
	if err != nil || !res.Killed {
		t.Fatalf("res %+v err %v", res, err)
	}
	ps := pids(out.String())
	if len(ps) != 2 {
		t.Fatalf("pids = %v from %q", ps, out.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (alive(ps[0]) || alive(ps[1])) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(ps[0]) || alive(ps[1]) {
		t.Fatalf("processes %v survived", ps)
	}
}

func TestTermIsEnoughForACommandThatHandlesIt(t *testing.T) {
	box, _ := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := box.Exec(ctx, core.Cmd{Shell: "trap 'exit 7' TERM; sleep 30 & wait", Grace: 10 * time.Second})
	if err != nil || !res.Killed {
		t.Fatalf("res %+v err %v", res, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited for the grace period although the command exited on the polite signal")
	}
}

func TestTermThenKillAfterGrace(t *testing.T) {
	box, _ := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := box.Exec(ctx, core.Cmd{Shell: "trap '' TERM; sleep 30", Grace: 500 * time.Millisecond})
	took := time.Since(start)
	if err != nil || !res.Killed || res.Exit != 137 {
		t.Fatalf("res %+v err %v, want killed with 137", res, err)
	}
	if took < 650*time.Millisecond || took > 5*time.Second {
		t.Fatalf("took %v, want about timeout + grace", took)
	}
}

func TestDoesNotWaitForAPipeHeldOpenByAChild(t *testing.T) {
	box, _ := open(t)
	var out bytes.Buffer // not a file: the adapter reads a pipe
	start := time.Now()
	res, err := box.Exec(context.Background(), core.Cmd{Shell: "sleep 30 & echo $!; echo done", Out: &out})
	if err != nil || res.Exit != 0 || res.Killed {
		t.Fatalf("res %+v err %v", res, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("waited for the child that holds the pipe")
	}
	ps := pids(out.String())
	if len(ps) != 1 {
		t.Fatalf("out = %q", out.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && alive(ps[0]) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(ps[0]) {
		t.Fatal("a background child outlived its command")
	}
}

func TestCloseRemovesThePrivateFolder(t *testing.T) {
	box, _ := open(t)
	got := envOf(t, box)
	if err := box.Close(); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"HOME", "TMPDIR"} {
		if _, err := os.Stat(got[k]); !os.IsNotExist(err) {
			t.Errorf("%s still exists after Close: %v", k, err)
		}
	}
	if err := box.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestOpenRefusesALinkedMissingOrFileDir(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	os.Mkdir(real, 0o700)
	link := filepath.Join(root, "link")
	os.Symlink(real, link)
	file := filepath.Join(root, "file")
	os.WriteFile(file, nil, 0o600)
	for name, dir := range map[string]string{"link": link, "missing": filepath.Join(root, "none"), "file": file} {
		if box, err := (nosandbox.Sandbox{}).Open(context.Background(), core.BoxSpec{Dir: dir}); err == nil {
			box.Close()
			t.Errorf("%s accepted", name)
		}
	}
}

func TestACommandNeedsExactlyOneForm(t *testing.T) {
	box, _ := open(t)
	for _, c := range []core.Cmd{{}, {Argv: []string{"x"}, Shell: "y"}} {
		if _, err := box.Exec(context.Background(), c); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

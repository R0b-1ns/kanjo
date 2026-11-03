package kanjotest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// FakeSandbox is a core.Sandbox that runs nothing: it records every BoxSpec
// and Cmd and lets Handler answer. A Handler may block until ctx ends and then
// return Killed.
type FakeSandbox struct {
	// Handler answers a command; nil means exit 0.
	Handler func(ctx context.Context, spec core.BoxSpec, cmd core.Cmd) core.ExecResult

	mu    sync.Mutex
	specs []core.BoxSpec
	cmds  []core.Cmd
}

// NewFakeSandbox returns a sandbox whose commands all succeed.
func NewFakeSandbox() *FakeSandbox { return &FakeSandbox{} }

// Specs returns the specs of every box opened.
func (f *FakeSandbox) Specs() []core.BoxSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.specs)
}

// Cmds returns every command run, in order.
func (f *FakeSandbox) Cmds() []core.Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cmds)
}

// Open implements core.Sandbox.
func (f *FakeSandbox) Open(_ context.Context, spec core.BoxSpec) (core.Box, error) {
	if !strings.HasPrefix(spec.Dir, "/") {
		return nil, errors.New("box folder must be an absolute path")
	}
	spec.Env = slices.Clone(spec.Env)
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	return &fakeBox{f: f, spec: spec}, nil
}

type fakeBox struct {
	f      *FakeSandbox
	spec   core.BoxSpec
	mu     sync.Mutex
	closed bool
}

func (b *fakeBox) Exec(ctx context.Context, cmd core.Cmd) (core.ExecResult, error) {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return core.ExecResult{}, core.ErrBoxClosed
	}
	if (len(cmd.Argv) == 0) == (cmd.Shell == "") {
		return core.ExecResult{}, errors.New("a command needs exactly one of Argv and Shell")
	}
	b.f.mu.Lock()
	b.f.cmds = append(b.f.cmds, cmd)
	b.f.mu.Unlock()
	if b.f.Handler == nil {
		return core.ExecResult{}, nil
	}
	start := time.Now()
	res := b.f.Handler(ctx, b.spec, cmd)
	res.Duration = time.Since(start)
	return res, nil
}

func (b *fakeBox) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

// SandboxProbe gives the commands the contract needs, written for the adapter
// under test (shell scripts for a real one).
type SandboxProbe struct {
	Exit       func(n int) core.Cmd // ends with exit code n
	Sleep      func() core.Cmd      // runs until stopped
	PrintEnv   func() core.Cmd      // writes its environment, one NAME=value per line, to Out
	SpawnChild func() core.Cmd      // starts a child that outlives it unless killed, then waits; writes anything to Out
	IgnoreTerm func() core.Cmd      // ignores the polite stop signal and runs until killed
	// ChildAlive reports whether the child of SpawnChild still runs, given what
	// SpawnChild wrote to Out.
	ChildAlive func(out string) bool
}

const probeBound = 5 * time.Second

// lockedBuffer is an io.Writer safe to read while a command may still write.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func openBox(t *testing.T, sb core.Sandbox, env []string) core.Box {
	t.Helper()
	box, err := sb.Open(context.Background(), core.BoxSpec{Dir: t.TempDir(), Env: env})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { box.Close() })
	return box
}

// execCancelled runs cmd and cancels the context after 150 ms; it fails the
// test when Exec does not return within grace plus the bound.
func execCancelled(t *testing.T, box core.Box, cmd core.Cmd, grace time.Duration) (core.ExecResult, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &lockedBuffer{}
	cmd.Out, cmd.Grace = out, grace
	time.AfterFunc(150*time.Millisecond, cancel)
	type result struct {
		res core.ExecResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := box.Exec(ctx, cmd)
		done <- result{res, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Exec: %v", r.err)
		}
		return r.res, out.String()
	case <-time.After(grace + probeBound):
		t.Fatal("Exec did not return after its context ended")
		return core.ExecResult{}, ""
	}
}

func sandboxCases(p SandboxProbe) []contractCase[func(env []string) core.Sandbox] {
	return []contractCase[func(env []string) core.Sandbox]{
		{"exit codes pass through", func(t *testing.T, open func([]string) core.Sandbox) {
			box := openBox(t, open(nil), nil)
			for _, n := range []int{0, 3, 42} {
				res, err := box.Exec(context.Background(), p.Exit(n))
				if err != nil || res.Exit != n || res.Killed {
					t.Fatalf("exit %d: res = %+v, err %v", n, res, err)
				}
			}
		}},
		{"the command sees every BoxSpec.Env entry and no host variable", func(t *testing.T, open func([]string) core.Sandbox) {
			t.Setenv("KANJO_HOST_CANARY", "leaked-value")
			box := openBox(t, open([]string{"KANJO_ONE=1"}), []string{"KANJO_ONE=1", "KANJO_TWO=two"})
			out := &lockedBuffer{}
			cmd := p.PrintEnv()
			cmd.Out = out
			if res, err := box.Exec(context.Background(), cmd); err != nil || res.Exit != 0 {
				t.Fatalf("res = %+v, err %v", res, err)
			}
			got := out.String()
			for _, want := range []string{"KANJO_ONE=1", "KANJO_TWO=two"} {
				if !strings.Contains(got, want) {
					t.Errorf("environment lacks %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "KANJO_HOST_CANARY") || strings.Contains(got, "leaked-value") {
				t.Errorf("a host variable leaked into the box:\n%s", got)
			}
		}},
		{"a cancelled context kills and sets Killed within the bound", func(t *testing.T, open func([]string) core.Sandbox) {
			box := openBox(t, open(nil), nil)
			res, _ := execCancelled(t, box, p.Sleep(), 100*time.Millisecond)
			if !res.Killed {
				t.Fatalf("res = %+v, want Killed", res)
			}
		}},
		{"children are killed with the command", func(t *testing.T, open func([]string) core.Sandbox) {
			box := openBox(t, open(nil), nil)
			res, out := execCancelled(t, box, p.SpawnChild(), 100*time.Millisecond)
			if !res.Killed {
				t.Fatalf("res = %+v, want Killed", res)
			}
			deadline := time.Now().Add(probeBound)
			for p.ChildAlive(out) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if p.ChildAlive(out) {
				t.Fatal("the child survived the kill")
			}
		}},
		{"a command that ignores the polite signal is killed after Grace", func(t *testing.T, open func([]string) core.Sandbox) {
			box := openBox(t, open(nil), nil)
			start := time.Now()
			res, _ := execCancelled(t, box, p.IgnoreTerm(), 300*time.Millisecond)
			if !res.Killed {
				t.Fatalf("res = %+v, want Killed", res)
			}
			if time.Since(start) < 300*time.Millisecond {
				t.Fatal("killed before Grace ended")
			}
		}},
		{"Out receives the output", func(t *testing.T, open func([]string) core.Sandbox) {
			box := openBox(t, open([]string{"KANJO_OUT=seen"}), []string{"KANJO_OUT=seen"})
			out := &lockedBuffer{}
			cmd := p.PrintEnv()
			cmd.Out = out
			box.Exec(context.Background(), cmd)
			if !strings.Contains(out.String(), "KANJO_OUT=seen") {
				t.Fatalf("out = %q", out.String())
			}
		}},
		{"exec after Close is refused", func(t *testing.T, open func([]string) core.Sandbox) {
			box := openBox(t, open(nil), nil)
			if err := box.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := box.Exec(context.Background(), p.Exit(0)); !errors.Is(err, core.ErrBoxClosed) {
				t.Fatalf("err = %v", err)
			}
		}},
		{"a relative or empty Dir is refused by Open", func(t *testing.T, open func([]string) core.Sandbox) {
			for _, dir := range []string{"", "relative/dir", "."} {
				if box, err := open(nil).Open(context.Background(), core.BoxSpec{Dir: dir}); err == nil {
					box.Close()
					t.Errorf("Dir %q accepted", dir)
				}
			}
		}},
	}
}

// SandboxContract checks the behavior every core.Sandbox must have. open
// returns a sandbox; env is the environment the test will pass to Open.
func SandboxContract(t *testing.T, open func(t *testing.T, env []string) core.Sandbox, p SandboxProbe) {
	for _, c := range sandboxCases(p) {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, func(env []string) core.Sandbox { return open(t, env) })
		})
	}
}

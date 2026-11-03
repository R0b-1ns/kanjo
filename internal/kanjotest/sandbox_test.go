package kanjotest_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

// probeFake is a FakeSandbox whose handler understands the contract's probes.
func probeFake() (func(t *testing.T, env []string) core.Sandbox, kanjotest.SandboxProbe) {
	var childAlive atomic.Bool
	fake := kanjotest.NewFakeSandbox()
	fake.Handler = func(ctx context.Context, spec core.BoxSpec, cmd core.Cmd) core.ExecResult {
		switch cmd.Argv[0] {
		case "probe-exit":
			n, _ := strconv.Atoi(cmd.Argv[1])
			return core.ExecResult{Exit: n}
		case "probe-env":
			for _, e := range spec.Env {
				cmd.Out.Write([]byte(e + "\n"))
			}
			return core.ExecResult{}
		case "probe-sleep":
			<-ctx.Done()
			return core.ExecResult{Exit: -1, Killed: true}
		case "probe-child":
			childAlive.Store(true)
			cmd.Out.Write([]byte("child\n"))
			<-ctx.Done()
			childAlive.Store(false)
			return core.ExecResult{Exit: -1, Killed: true}
		default: // probe-ignore-term: dies only after the grace period
			<-ctx.Done()
			time.Sleep(cmd.Grace)
			return core.ExecResult{Exit: -1, Killed: true}
		}
	}
	probe := kanjotest.SandboxProbe{
		Exit:       func(n int) core.Cmd { return core.Cmd{Argv: []string{"probe-exit", strconv.Itoa(n)}} },
		Sleep:      func() core.Cmd { return core.Cmd{Argv: []string{"probe-sleep"}} },
		PrintEnv:   func() core.Cmd { return core.Cmd{Argv: []string{"probe-env"}} },
		SpawnChild: func() core.Cmd { return core.Cmd{Argv: []string{"probe-child"}} },
		IgnoreTerm: func() core.Cmd { return core.Cmd{Argv: []string{"probe-ignore-term"}} },
		ChildAlive: func(string) bool { return childAlive.Load() },
	}
	return func(*testing.T, []string) core.Sandbox { return fake }, probe
}

func TestFakeSandboxContract(t *testing.T) {
	open, probe := probeFake()
	kanjotest.SandboxContract(t, open, probe)
}

func TestFakeSandboxRecordsSpecsAndCommands(t *testing.T) {
	fake := kanjotest.NewFakeSandbox()
	fake.Handler = func(context.Context, core.BoxSpec, core.Cmd) core.ExecResult { return core.ExecResult{Exit: 7} }
	box, err := fake.Open(context.Background(), core.BoxSpec{Dir: "/fake/w", Env: []string{"A=1"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := box.Exec(context.Background(), core.Cmd{Shell: "echo hi"})
	if err != nil || res.Exit != 7 {
		t.Fatalf("res = %+v, err %v", res, err)
	}
	if len(fake.Specs()) != 1 || fake.Specs()[0].Dir != "/fake/w" || len(fake.Cmds()) != 1 || fake.Cmds()[0].Shell != "echo hi" {
		t.Fatalf("specs %+v cmds %+v", fake.Specs(), fake.Cmds())
	}
	if _, err := box.Exec(context.Background(), core.Cmd{}); err == nil {
		t.Fatal("a command with neither Argv nor Shell must be refused")
	}
	if _, err := box.Exec(context.Background(), core.Cmd{Argv: []string{"a"}, Shell: "b"}); err == nil {
		t.Fatal("a command with both Argv and Shell must be refused")
	}
}

package scriptagent_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/scriptagent"
)

func script(t *testing.T, dir string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode) // the umask may have narrowed the mode
	return p
}

func realTemp(t *testing.T) string {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAgentRunnerContract(t *testing.T) {
	dir := realTemp(t)
	path := script(t, dir, 0o700)
	kanjotest.AgentRunnerContract(t, func(t *testing.T) kanjotest.AgentFixture {
		fake := kanjotest.NewFakeSandbox()
		box, _ := fake.Open(context.Background(), core.BoxSpec{Dir: "/fake/w"})
		return kanjotest.AgentFixture{Box: box, Session: "ses_c", New: func(b kanjotest.AgentBehavior) core.AgentRunner {
			if b == kanjotest.AgentReportsUsage {
				return nil
			}
			fake.Handler = func(ctx context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
				switch b {
				case kanjotest.AgentFails:
					return core.ExecResult{Exit: 3}
				case kanjotest.AgentLoops:
					<-ctx.Done()
					return core.ExecResult{Exit: -1, Killed: true}
				}
				return core.ExecResult{}
			}
			a, err := scriptagent.New(path, "")
			if err != nil {
				t.Fatal(err)
			}
			return a
		}}
	})
}

func TestRefusesAScriptInsideTheWorkdir(t *testing.T) {
	work := realTemp(t)
	if _, err := scriptagent.New(script(t, work, 0o700), work); err == nil {
		t.Fatal("a script inside the forbidden folder was accepted")
	}
	sub := filepath.Join(work, "sub")
	os.Mkdir(sub, 0o700)
	if _, err := scriptagent.New(script(t, sub, 0o700), work); err == nil {
		t.Fatal("a script below the forbidden folder was accepted")
	}
	other := realTemp(t)
	if _, err := scriptagent.New(script(t, other, 0o700), work); err != nil {
		t.Fatalf("a script elsewhere: %v", err)
	}
	// a link out of the forbidden folder into it is still inside
	link := filepath.Join(other, "ln.sh")
	os.Symlink(script(t, work, 0o700), link)
	if _, err := scriptagent.New(link, work); err == nil {
		t.Fatal("a link was accepted")
	}
}

func TestRefusesAWritableScript(t *testing.T) {
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777, 0o720} {
		if _, err := scriptagent.New(script(t, realTemp(t), mode), ""); err == nil {
			t.Errorf("mode %o accepted", mode)
		}
	}
}

func TestRefusesANonExecutableScript(t *testing.T) {
	if _, err := scriptagent.New(script(t, realTemp(t), 0o600), ""); err == nil {
		t.Fatal("accepted")
	}
}

func TestRefusesALinkedScript(t *testing.T) {
	dir := realTemp(t)
	target := script(t, dir, 0o700)
	link := filepath.Join(realTemp(t), "ln.sh")
	os.Symlink(target, link)
	if _, err := scriptagent.New(link, ""); err == nil {
		t.Fatal("accepted a link")
	}
}

func TestRefusesAMissingOrRelativeOrDirectoryScript(t *testing.T) {
	dir := realTemp(t)
	for _, p := range []string{filepath.Join(dir, "none"), dir, "agent.sh", ""} {
		if _, err := scriptagent.New(p, ""); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func fakeRun(t *testing.T, req core.AgentRequest, exit int) (core.AgentResult, *kanjotest.FakeSandbox, string) {
	t.Helper()
	path := script(t, realTemp(t), 0o700)
	a, err := scriptagent.New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	fake := kanjotest.NewFakeSandbox()
	fake.Handler = func(_ context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		return core.ExecResult{Exit: exit}
	}
	box, _ := fake.Open(context.Background(), core.BoxSpec{Dir: "/fake/w"})
	res, err := a.Run(context.Background(), box, req)
	if err != nil {
		t.Fatal(err)
	}
	return res, fake, path
}

func TestPromptGoesToStdinNotArgv(t *testing.T) {
	const canary = "CANARY-PROMPT-71"
	_, fake, path := fakeRun(t, core.AgentRequest{Session: "ses_1", Model: "m", Prompt: "do " + canary}, 0)
	c := fake.Cmds()[0]
	if strings.Join(c.Argv, " ") != path {
		t.Fatalf("argv = %v", c.Argv)
	}
	for _, e := range c.Env {
		if strings.Contains(e, canary) {
			t.Fatalf("prompt in the environment: %v", c.Env)
		}
	}
	b, _ := io.ReadAll(c.Stdin)
	if string(b) != "do "+canary {
		t.Fatalf("stdin = %q", b)
	}
}

func TestExit75IsQuota(t *testing.T) {
	res, _, _ := fakeRun(t, core.AgentRequest{Session: "ses_1", Model: "m", Prompt: "p"}, 75)
	if !res.Quota || res.Killed {
		t.Fatalf("res = %+v", res)
	}
	if res, _, _ := fakeRun(t, core.AgentRequest{Session: "ses_1", Model: "m", Prompt: "p"}, 3); res.Quota || res.Exit != 3 {
		t.Fatalf("res = %+v", res)
	}
}

func TestResumeIsPassedInTheEnvironment(t *testing.T) {
	_, fake, _ := fakeRun(t, core.AgentRequest{Session: "ses_1", Model: "m", Prompt: "p", Resume: true}, 0)
	env := strings.Join(fake.Cmds()[0].Env, ",")
	for _, want := range []string{"KANJO_SESSION=ses_1", "KANJO_MODEL=m", "KANJO_RESUME=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("env %q lacks %s", env, want)
		}
	}
	_, fake, _ = fakeRun(t, core.AgentRequest{Session: "ses_1", Model: "m", Prompt: "p"}, 0)
	if !strings.Contains(strings.Join(fake.Cmds()[0].Env, ","), "KANJO_RESUME=0") {
		t.Error("KANJO_RESUME=0 missing")
	}
}

func TestRunRefusesAnInvalidSessionOrModel(t *testing.T) {
	path := script(t, realTemp(t), 0o700)
	a, _ := scriptagent.New(path, "")
	fake := kanjotest.NewFakeSandbox()
	box, _ := fake.Open(context.Background(), core.BoxSpec{Dir: "/fake/w"})
	for _, req := range []core.AgentRequest{{Session: "a b", Model: "m"}, {Session: "ses_1", Model: "bad model"}} {
		if _, err := a.Run(context.Background(), box, req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
	if len(fake.Cmds()) != 0 {
		t.Fatal("a command ran")
	}
}

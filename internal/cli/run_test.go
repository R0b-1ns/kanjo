package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

// runRig is a world whose run command uses fake workspace, sandbox and agent.
type runRig struct {
	*world
	ws    *kanjotest.FakeWorkspace
	sb    *kanjotest.FakeSandbox
	ports *delegatePorts
}

func newRunRig(t *testing.T, launches ...[]kanjotest.AgentStep) *runRig {
	t.Helper()
	w := newWorld(t)
	write(t, filepath.Join(w.cwd, "prompt.txt"), "write the file\n")
	ws := kanjotest.NewFakeWorkspace()
	sb := kanjotest.NewFakeSandbox()
	return &runRig{world: w, ws: ws, sb: sb, ports: &delegatePorts{Workspace: ws, Sandbox: sb, Agent: kanjotest.NewScriptedAgent(ws, launches...)}}
}

var writeFileA = []kanjotest.AgentStep{kanjotest.Write("src/a.go", "package a")}

func (r *runRig) exec(ctx context.Context, args ...string) (code int, stdout, stderr string) {
	r.t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb, env{home: r.home, cwd: r.cwd, clock: kanjotest.FixedClock{T: fixedNow}, ctx: ctx, ports: r.ports})
	return code, out.String(), errb.String()
}

func (r *runRig) run(args ...string) (int, string, string) {
	return r.exec(context.Background(), append([]string{"run", "--agent", "script", "--model", "test-model", "--prompt-file", "prompt.txt", "--expect", "src/a.go"}, args...)...)
}

func (r *runRig) openStore() *sqlitestore.Store {
	r.t.Helper()
	st, err := sqlitestore.Open(r.data)
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

func (r *runRig) setConfig(extra string) {
	cfg := "data_dir = \"" + r.data + "\"\n[claude]\nproject_prefixes = [\"-home-dev-demo\"]\n" + extra
	write(r.t, filepath.Join(r.home, ".config", "kanjo", "config.toml"), cfg)
}

var sessionRe = regexp.MustCompile(`session: (ses_[A-Za-z0-9_]+)`)

func sessionOf(t *testing.T, out string) string {
	t.Helper()
	m := sessionRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no session in output:\n%s", out)
	}
	return m[1]
}

func TestRunRejectsHostileFlagsBeforeCreatingAnything(t *testing.T) {
	bad := [][]string{
		{"--task", "../x"}, {"--task", "-"}, {"--ref", "-x"}, {"--ref", "a b"}, {"--expect", "a\nb"}, {"--forbid", "../x"},
		{"--expect", "/abs"}, {"--setup", ""}, {"--test", "a\x00b"}, {"--check", ""}, {"--timeout", "0"}, {"--timeout", "-3"},
		{"--script", "/bin/true"}, // another agent than script is not the point: --script needs --agent script
	}
	for _, extra := range bad {
		r := newRunRig(t, writeFileA)
		args := append([]string{"run", "--agent", "script", "--model", "test-model", "--prompt-file", "prompt.txt"}, extra...)
		if extra[0] == "--script" {
			args[2] = "other" // "--agent other"
			args[1] = "--agent"
		}
		if code, _, errb := r.exec(context.Background(), args...); code != 2 {
			t.Errorf("%v: exit %d (%s)", extra, code, errb)
		}
		if _, err := os.Stat(r.data); err == nil {
			t.Errorf("%v: the data folder was created", extra)
		}
	}
	r := newRunRig(t, writeFileA)
	for _, extra := range [][]string{{"--model", ""}, {"--model", "bad model"}, {"--agent", "gpt"}} {
		args := append([]string{"run", "--agent", "script", "--model", "m", "--prompt-file", "prompt.txt"}, extra...)
		if code, _, _ := r.exec(context.Background(), args...); code != 2 {
			t.Errorf("%v: exit %d", extra, code)
		}
	}
	long := make([]string, 0, 130)
	for i := 0; i < 65; i++ {
		long = append(long, "--expect", "f"+strings.Repeat("x", i))
	}
	if code, _, _ := r.run(long...); code != 2 {
		t.Errorf("65 --expect: exit %d", code)
	}
	if _, err := os.Stat(r.data); err == nil {
		t.Error("the data folder was created")
	}
}

func TestRunWithoutRequiredFlagsIsAUsageError(t *testing.T) {
	r := newRunRig(t, writeFileA)
	for _, args := range [][]string{
		{"run"}, {"run", "--agent", "script"}, {"run", "--agent", "script", "--model", "m"}, {"run", "--model", "m", "--prompt-file", "prompt.txt"},
		{"run", "--agent", "script", "--model", "m", "--prompt-file", "prompt.txt", "extra"},
	} {
		if code, _, _ := r.exec(context.Background(), args...); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestRunWarnsThatThereIsNoSandbox(t *testing.T) {
	r := newRunRig(t, writeFileA)
	_, _, errb := r.run()
	if !strings.Contains(errb, "warning: no sandbox: the agent and the commands run with your rights") {
		t.Fatalf("stderr = %q", errb)
	}
}

func TestRunDoesNotPassCallerSecrets(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "hunter2")
	t.Setenv("KANJO_PASS_ME", "passed")
	t.Setenv("LANG", "C.UTF-8")
	r := newRunRig(t, writeFileA)
	r.setConfig("[delegate]\nenv_pass = [\"KANJO_PASS_ME\"]\n")
	if code, _, errb := r.run("--test", "true"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	env := strings.Join(r.sb.Specs()[0].Env, "\n")
	if strings.Contains(env, "SECRET_TOKEN") || strings.Contains(env, "hunter2") {
		t.Fatalf("a secret reached the box:\n%s", env)
	}
	for _, want := range []string{"KANJO_PASS_ME=passed", "LANG=C.UTF-8", "PATH="} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s:\n%s", want, env)
		}
	}
}

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name  string
		steps []kanjotest.AgentStep
		extra []string
		want  int
	}{
		{"ok", writeFileA, nil, 0},
		{"stray", []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Write("notes.txt", "x")}, nil, 0},
		{"failed", []kanjotest.AgentStep{kanjotest.Fail(2)}, nil, 1},
		{"quota", []kanjotest.AgentStep{kanjotest.ExhaustQuota()}, nil, 1},
		{"timeout", []kanjotest.AgentStep{kanjotest.Loop()}, []string{"--timeout", "1"}, 124},
	}
	for _, c := range cases {
		r := newRunRig(t, c.steps)
		if code, out, errb := r.run(c.extra...); code != c.want {
			t.Errorf("%s: exit %d, want %d\n%s\n%s", c.name, code, c.want, out, errb)
		}
	}
}

func TestRunExitCodeOfAnInterruptIs1(t *testing.T) {
	r := newRunRig(t, []kanjotest.AgentStep{kanjotest.Loop()})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	args := []string{"run", "--agent", "script", "--model", "m", "--prompt-file", "prompt.txt"}
	if code, out, _ := r.exec(ctx, args...); code != 1 || !strings.Contains(out, "verdict: interrupted") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestRunOutputCannotCarryEscapes(t *testing.T) {
	r := newRunRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.Write("evil\x1b[31m\nname.txt", "x")})
	code, out, errb := r.run()
	if code != 0 || strings.ContainsAny(out+errb, "\x1b\x07\r") {
		t.Fatalf("exit %d out %q err %q", code, out, errb)
	}
	if !strings.Contains(out, "<invalid-name-1>") {
		t.Fatalf("out = %q", out)
	}
}

func TestRunPromptFileOverTheCapIsRefused(t *testing.T) {
	r := newRunRig(t, writeFileA)
	r.setConfig("[delegate]\nmax_prompt_bytes = 10\n")
	write(t, filepath.Join(r.cwd, "big.txt"), strings.Repeat("x", 11))
	code, _, errb := r.exec(context.Background(), "run", "--agent", "script", "--model", "m", "--prompt-file", "big.txt")
	if code != 2 || !strings.Contains(errb, "max_prompt_bytes") {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, err := os.Stat(r.data); err == nil {
		t.Fatal("the data folder was created")
	}
	write(t, filepath.Join(r.cwd, "empty.txt"), "")
	if code, _, _ := r.exec(context.Background(), "run", "--agent", "script", "--model", "m", "--prompt-file", "empty.txt"); code != 2 {
		t.Fatalf("empty prompt: exit %d", code)
	}
	if code, _, _ := r.exec(context.Background(), "run", "--agent", "script", "--model", "m", "--prompt-file", "missing.txt"); code != 1 {
		t.Fatalf("missing prompt file: exit %d", code)
	}
}

func TestRunPrintsApplyAndFinishHints(t *testing.T) {
	r := newRunRig(t, writeFileA)
	code, out, _ := r.run()
	session := sessionOf(t, out)
	if code != 0 || !strings.Contains(out, "verdict: ok-to-review") ||
		!strings.Contains(out, "apply: git apply '"+filepath.Join(r.data, "runs", session, "changes.patch")+"'") ||
		!strings.Contains(out, "finish: kanjo finish "+session+" --verdict accepted|modified|rejected") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(r.data, "runs", session, "report.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRunRefusesAFolderThatIsNotAGitRoot(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.cwd, "prompt.txt"), "p")
	script := filepath.Join(t.TempDir(), "agent.sh")
	write(t, script, "#!/bin/sh\n")
	os.Chmod(script, 0o700)
	code, _, errb := w.run("run", "--agent", "script", "--script", script, "--model", "m", "--prompt-file", "prompt.txt")
	if code != 1 || !strings.Contains(errb, "git working tree") {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, err := os.Stat(w.data); err == nil {
		t.Fatal("the data folder was created")
	}
}

func TestRunScriptAgentNeedsAScript(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.cwd, "prompt.txt"), "p")
	if code, _, errb := w.run("run", "--agent", "script", "--model", "m", "--prompt-file", "prompt.txt"); code != 2 || !strings.Contains(errb, "--script") {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestRunUsageRowReachesUsageReport(t *testing.T) {
	r := newRunRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "x"), kanjotest.ReportUsage("test-model", core.Tokens{Input: 100, Output: 5}, nil)})
	r.run("--task", "ABC-12")
	code, out, _ := r.world.run("usage", "--by", "task", "--csv")
	if code != 0 || !strings.Contains(out, "ABC-12,1,100,5") {
		t.Fatalf("usage:\n%s", out)
	}
}

func TestCanaryNeverInRunOutputReportOrDatabase(t *testing.T) {
	const canary = "CANARY-RUN-5e9"
	r := newRunRig(t, []kanjotest.AgentStep{kanjotest.Write("src/a.go", "package a // "+canary), kanjotest.Output("answer " + canary)})
	write(t, filepath.Join(r.cwd, "prompt.txt"), "do "+canary)
	r.sb.Handler = func(_ context.Context, _ core.BoxSpec, c core.Cmd) core.ExecResult {
		c.Out.Write([]byte("output " + canary))
		return core.ExecResult{}
	}
	code, out, errb := r.run("--setup", "echo "+canary, "--test", "echo "+canary, "--check", "echo "+canary)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	session := sessionOf(t, out)
	files := map[string]string{"stdout": out, "stderr": errb}
	for _, name := range []string{"report.json", "report.txt", "changes.patch"} {
		b, err := os.ReadFile(filepath.Join(r.data, "runs", session, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(b)
	}
	db, _ := os.ReadFile(filepath.Join(r.data, "kanjo.db"))
	files["kanjo.db"] = string(db)
	for name, content := range files {
		if strings.Contains(content, canary) {
			t.Errorf("canary found in %s", name)
		}
	}
}

func openStoreAt(dir string) (*sqlitestore.Store, error) { return sqlitestore.Open(dir) }

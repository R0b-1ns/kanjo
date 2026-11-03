//go:build integration

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/sysclock"
)

var (
	buildOnce sync.Once
	binDir    string
	binPath   string
	buildErr  error
)

// TestMain removes the binary built for the child-process tests.
func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// kanjoBinary builds the command once.
func kanjoBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "kanjo-bin-")
		if buildErr != nil {
			return
		}
		binPath = filepath.Join(binDir, "kanjo")
		out, err := exec.Command("go", "build", "-o", binPath, "../../cmd/kanjo").CombinedOutput()
		if err != nil {
			buildErr = &buildFailure{err, string(out)}
		}
	})
	if buildErr != nil {
		t.Fatalf("go build: %v", buildErr)
	}
	return binPath
}

type buildFailure struct {
	err error
	out string
}

func (b *buildFailure) Error() string { return b.err.Error() + "\n" + b.out }

// e2e is a real git repository, a home folder and a folder of agent scripts.
type e2e struct {
	t                         *testing.T
	home, repo, data, scripts string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func newE2E(t *testing.T, commit bool) *e2e {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &e2e{t: t, home: filepath.Join(root, "home"), repo: filepath.Join(root, "repo"), scripts: filepath.Join(root, "scripts")}
	e.data = filepath.Join(e.repo, ".ai-usage")
	os.MkdirAll(e.home, 0o700)
	os.MkdirAll(e.scripts, 0o700)
	os.MkdirAll(e.repo, 0o755)
	git(t, e.repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(e.repo, "README.md"), "hello\n")
	write(t, filepath.Join(e.repo, "gone.txt"), "bye\n")
	if commit {
		git(t, e.repo, "add", "-A")
		git(t, e.repo, "commit", "-q", "-m", "init")
	}
	write(t, filepath.Join(e.repo, "prompt.txt"), "write the file\n")
	return e
}

// script writes an executable agent script and returns its path.
func (e *e2e) script(name, body string) string {
	e.t.Helper()
	p := filepath.Join(e.scripts, name)
	write(e.t, p, "#!/bin/sh\n"+body)
	os.Chmod(p, 0o700)
	return p
}

// kanjo runs a command in this process, in the repository.
func (e *e2e) kanjo(args ...string) (code int, stdout, stderr string) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb, env{home: e.home, cwd: e.repo, clock: sysclock.Clock{}})
	return code, out.String(), errb.String()
}

func (e *e2e) runArgs(script string, extra ...string) []string {
	return append([]string{"run", "--agent", "script", "--script", script, "--model", "test-model", "--prompt-file", "prompt.txt"}, extra...)
}

// start launches the command as a child process; wait returns its exit code.
func (e *e2e) start(args ...string) (*exec.Cmd, *bytes.Buffer) {
	e.t.Helper()
	cmd := exec.Command(kanjoBinary(e.t), args...)
	cmd.Dir = e.repo
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.home}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd, &out
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

func (e *e2e) listing() string {
	return git(e.t, e.repo, "status", "--porcelain") + git(e.t, e.repo, "branch", "--list") + git(e.t, e.repo, "worktree", "list", "--porcelain")
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

const goodAgent = "cat > /dev/null\nmkdir -p src\necho 'package a' > src/a.go\n"

func TestIntegrationRunAcceptFinish(t *testing.T) {
	e := newE2E(t, true)
	before := e.listing()
	code, out, errb := e.kanjo(e.runArgs(e.script("good.sh", goodAgent), "--expect", "src/a.go", "--task", "ABC-12", "--test", "test -f src/a.go", "--check", "true")...)
	if code != 0 || !strings.Contains(out, "verdict: ok-to-review") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
	session := sessionOf(t, out)
	runDir := filepath.Join(e.data, "runs", session)
	for _, f := range []string{"report.json", "report.txt", "changes.patch", "agent.log", "test.log", "check-1.log"} {
		if _, err := os.Stat(filepath.Join(runDir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	patch := filepath.Join(runDir, "changes.patch")
	git(t, e.repo, "apply", "--check", patch) // applies cleanly on the main checkout
	if b, _ := os.ReadFile(patch); !strings.Contains(string(b), "+package a") {
		t.Fatalf("patch:\n%s", b)
	}
	if rows := (&world{t: t, data: e.data}).rows(); len(rows) != 0 {
		t.Fatalf("the script reported no usage, but rows = %+v", rows)
	}
	if code, out, errb := e.kanjo("finish", session, "--verdict", "accepted"); code != 0 || !strings.Contains(out, "workspace removed") || !strings.Contains(out, "git -C") {
		t.Fatalf("finish: exit %d\n%s\n%s", code, out, errb)
	}
	for _, p := range []string{filepath.Join(e.data, "worktrees", session), filepath.Join(e.data, "worktrees", session+".lock")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s remains", p)
		}
	}
	if strings.Contains(git(t, e.repo, "branch", "--list", "delegate/*"), "delegate") {
		t.Error("the branch remains")
	}
	if e.listing() != before {
		t.Errorf("the repository changed:\n%s\nvs\n%s", before, e.listing())
	}
}

func TestIntegrationRetryForMissingFile(t *testing.T) {
	e := newE2E(t, true)
	agent := e.script("retry.sh", "cat > /dev/null\nif [ \"$KANJO_RESUME\" = 1 ]; then mkdir -p src; echo x > src/a.go; fi\n")
	code, out, _ := e.kanjo(e.runArgs(agent, "--expect", "src/a.go")...)
	if code != 0 || !strings.Contains(out, "launches: 2 (retries 1)") || !strings.Contains(out, "verdict: ok-to-review") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestIntegrationForbiddenPathFails(t *testing.T) {
	e := newE2E(t, true)
	agent := e.script("forbid.sh", "cat > /dev/null\nmkdir -p src config\necho x > src/a.go\necho x > config/prod.yaml\n")
	code, out, _ := e.kanjo(e.runArgs(agent, "--expect", "src/a.go", "--forbid", "config")...)
	if code != 1 || !strings.Contains(out, "forbidden-path") || !strings.Contains(out, "forbidden: config/prod.yaml") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestIntegrationStrayFileGivesCheckStrayFiles(t *testing.T) {
	e := newE2E(t, true)
	agent := e.script("stray.sh", goodAgent+"echo x > notes.txt\n")
	code, out, _ := e.kanjo(e.runArgs(agent, "--expect", "src/a.go")...)
	if code != 0 || !strings.Contains(out, "verdict: check-stray-files") || !strings.Contains(out, "unexpected: notes.txt") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestIntegrationUnsafeLinkFails(t *testing.T) {
	e := newE2E(t, true)
	agent := e.script("link.sh", "cat > /dev/null\nmkdir -p src\nln -s /etc/passwd src/a.go\n")
	code, out, _ := e.kanjo(e.runArgs(agent, "--expect", "src/a.go")...)
	if code != 1 || !strings.Contains(out, "unsafe-symlink") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestIntegrationTimeoutExits124AndKillsChildren(t *testing.T) {
	e := newE2E(t, true)
	pids := filepath.Join(e.scripts, "pids")
	agent := e.script("hang.sh", "cat > /dev/null\n(sleep 300 & echo $! >> "+pids+"; wait) &\nsleep 300 &\necho $! >> "+pids+"\nwait\n")
	code, out, _ := e.kanjo(e.runArgs(agent, "--timeout", "1")...)
	if code != 124 || !strings.Contains(out, "verdict: timeout") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	b, _ := os.ReadFile(pids)
	fields := strings.Fields(string(b))
	if len(fields) != 2 {
		t.Fatalf("pids = %q", b)
	}
	time.Sleep(200 * time.Millisecond)
	for _, f := range fields {
		pid, _ := strconv.Atoi(f)
		if alive(pid) {
			t.Errorf("process %d survived the timeout", pid)
		}
	}
}

func TestIntegrationSigintLeavesAFinishableRun(t *testing.T) {
	e := newE2E(t, true)
	marker := filepath.Join(e.scripts, "started")
	agent := e.script("slow.sh", "cat > /dev/null\nmkdir -p src\necho partial > src/a.go\necho $$ > "+marker+"\nsleep 60\n")
	cmd, out := e.start(e.runArgs(agent, "--expect", "src/a.go")...)
	waitFile(t, marker)
	cmd.Process.Signal(syscall.SIGINT)
	err := cmd.Wait()
	if exitCode(err) != 1 || !strings.Contains(out.String(), "verdict: interrupted") {
		t.Fatalf("exit %d\n%s", exitCode(err), out)
	}
	session := sessionOf(t, out.String())
	if b, _ := os.ReadFile(filepath.Join(e.data, "runs", session, "changes.patch")); !strings.Contains(string(b), "src/a.go") {
		t.Errorf("the patch lacks what the agent wrote: %q", b)
	}
	if _, o, _ := e.kanjo("summary"); !strings.Contains(o, "interrupted  1") {
		t.Fatalf("summary:\n%s", o)
	}
	if code, o, errb := e.kanjo("finish", session, "--verdict", "rejected"); code != 0 {
		t.Fatalf("finish: exit %d\n%s\n%s", code, o, errb)
	}
}

func TestIntegrationCleanDuringARun(t *testing.T) {
	e := newE2E(t, true)
	marker := filepath.Join(e.scripts, "started")
	agent := e.script("long.sh", "cat > /dev/null\necho $$ > "+marker+"\nsleep 2\nmkdir -p src\necho x > src/a.go\n")
	cmd, out := e.start(e.runArgs(agent, "--expect", "src/a.go")...)
	waitFile(t, marker)
	code, o, errb := e.kanjo("clean", "--yes")
	if code != 0 || !strings.Contains(o, "skipped (running)") {
		t.Fatalf("clean: exit %d\n%s\n%s", code, o, errb)
	}
	if err := cmd.Wait(); exitCode(err) != 0 || !strings.Contains(out.String(), "verdict: ok-to-review") {
		t.Fatalf("the run was disturbed: exit %d\n%s", exitCode(err), out)
	}
}

func TestIntegrationCleanRemovesACrashedRun(t *testing.T) {
	e := newE2E(t, true)
	marker := filepath.Join(e.scripts, "started")
	agent := e.script("long.sh", "cat > /dev/null\necho $$ > "+marker+"\nsleep 60\n")
	cmd, _ := e.start(e.runArgs(agent)...)
	agentPid, _ := strconv.Atoi(waitFile(t, marker))
	defer syscall.Kill(-agentPid, syscall.SIGKILL) // the orphaned agent
	cmd.Process.Signal(syscall.SIGKILL)
	cmd.Wait()
	_, listing, _ := e.kanjo("clean")
	if !strings.Contains(listing, " crashed ") {
		t.Fatalf("listing:\n%s", listing)
	}
	if code, o, errb := e.kanjo("clean", "--yes"); code != 0 || !strings.Contains(o, "removed ses_") {
		t.Fatalf("clean: exit %d\n%s\n%s", code, o, errb)
	}
	if _, o, _ := e.kanjo("summary"); !strings.Contains(o, "interrupted  1") || !strings.Contains(o, "abandoned  1") {
		t.Fatalf("summary:\n%s", o)
	}
	if strings.Contains(git(t, e.repo, "branch", "--list", "delegate/*"), "delegate") {
		t.Error("the branch remains")
	}
}

func TestIntegrationFinishTwice(t *testing.T) {
	e := newE2E(t, true)
	_, out, _ := e.kanjo(e.runArgs(e.script("good.sh", goodAgent))...)
	session := sessionOf(t, out)
	if code, _, _ := e.kanjo("finish", session, "--verdict", "modified"); code != 0 {
		t.Fatalf("first finish: %d", code)
	}
	if code, o, _ := e.kanjo("finish", session, "--verdict", "modified"); code != 0 || !strings.Contains(o, "already recorded") {
		t.Fatalf("second finish: %d\n%s", code, o)
	}
	if code, _, _ := e.kanjo("finish", session, "--verdict", "accepted"); code != 1 {
		t.Fatalf("another verdict: %d", code)
	}
}

func TestIntegrationRepoWithoutCommit(t *testing.T) {
	e := newE2E(t, false)
	code, _, errb := e.kanjo(e.runArgs(e.script("good.sh", goodAgent))...)
	if code != 1 || !strings.Contains(errb, "no commit") {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, o, _ := e.kanjo("summary"); o != "no runs\n" {
		t.Fatalf("summary: %q", o)
	}
	if _, err := os.Stat(filepath.Join(e.data, "worktrees")); err == nil {
		t.Fatal("a worktrees folder was created")
	}
}

func TestIntegrationMainCheckoutIsUntouched(t *testing.T) {
	e := newE2E(t, true)
	write(t, filepath.Join(e.repo, "dirty.txt"), "uncommitted work\n")
	status := git(t, e.repo, "status", "--porcelain")
	branches := git(t, e.repo, "branch", "--list")
	_, out, _ := e.kanjo(e.runArgs(e.script("good.sh", goodAgent), "--expect", "src/a.go")...)
	session := sessionOf(t, out)
	e.kanjo("finish", session, "--verdict", "accepted")
	if git(t, e.repo, "status", "--porcelain") != status || git(t, e.repo, "branch", "--list") != branches {
		t.Fatal("the main checkout or its branches changed")
	}
	if b, _ := os.ReadFile(filepath.Join(e.repo, "dirty.txt")); string(b) != "uncommitted work\n" {
		t.Fatal("an uncommitted file changed")
	}
	if _, err := os.Stat(filepath.Join(e.repo, "src", "a.go")); err == nil {
		t.Fatal("the change was applied: kanjo must never apply")
	}
}

func TestIntegrationUnrecordedBranchSurvivesClean(t *testing.T) {
	e := newE2E(t, true)
	git(t, e.repo, "branch", "delegate/mine")
	mine := filepath.Join(filepath.Dir(e.repo), "mine")
	git(t, e.repo, "worktree", "add", "-q", "-b", "other", mine, "HEAD")
	_, out, _ := e.kanjo(e.runArgs(e.script("good.sh", goodAgent))...)
	session := sessionOf(t, out)
	if code, _, _ := e.kanjo("clean", "--yes"); code != 0 {
		t.Fatalf("clean: %d", code)
	}
	e.kanjo("finish", session, "--verdict", "accepted")
	if !strings.Contains(git(t, e.repo, "branch", "--list"), "delegate/mine") || !strings.Contains(git(t, e.repo, "branch", "--list"), "other") {
		t.Fatal("a user branch was removed")
	}
	if _, err := os.Stat(filepath.Join(mine, "README.md")); err != nil {
		t.Fatal("the user's worktree was removed")
	}
}

func TestIntegrationRunDoesNotPassCallerSecrets(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "hunter2")
	e := newE2E(t, true)
	dump := filepath.Join(e.scripts, "env.txt")
	agent := e.script("env.sh", "cat > /dev/null\nenv > "+dump+"\nmkdir -p src\necho x > src/a.go\n")
	if code, out, errb := e.kanjo(e.runArgs(agent, "--expect", "src/a.go", "--test", "env > "+dump+".test")...); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
	for _, f := range []string{dump, dump + ".test"} {
		b, err := os.ReadFile(f)
		if err != nil || strings.Contains(string(b), "SECRET_TOKEN") || strings.Contains(string(b), "hunter2") {
			t.Errorf("%s: %v\n%s", f, err, b)
		}
		if !strings.Contains(string(b), "KANJO_SESSION=") && f == dump {
			t.Errorf("the script lacks KANJO_SESSION:\n%s", b)
		}
	}
}

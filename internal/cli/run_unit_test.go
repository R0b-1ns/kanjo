package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/config"
)

func TestRequestTakesItsLimitsFromTheConfig(t *testing.T) {
	cfg := config.Config{Delegate: config.DelegateConfig{TimeoutS: 900, CommandTimeoutS: 600, KillGraceS: 5, MaxPatchBytes: 77}}
	o := &runOpts{agent: "script", model: "m"}
	req := o.request(cfg, "p")
	if req.Timeout != 900*time.Second || req.CommandTimeout != 600*time.Second || req.Grace != 5*time.Second || req.MaxPatchBytes != 77 {
		t.Fatalf("request = %+v", req)
	}
	o.timeout = 3
	if got := o.request(cfg, "p").Timeout; got != 3*time.Second {
		t.Fatalf("--timeout 3 gives %v", got)
	}
}

func TestRepoPath(t *testing.T) {
	for _, c := range []struct{ repo, cwd, want string }{
		{"", "/work", "/work"}, {"/abs/repo", "/work", "/abs/repo"}, {"rel/repo", "/work", "/work/rel/repo"},
	} {
		if got := repoPath(c.repo, c.cwd); got != c.want {
			t.Errorf("repoPath(%q, %q) = %q, want %q", c.repo, c.cwd, got, c.want)
		}
	}
}

func TestBoxEnvHoldsEachNameOnceAndOnlyWhatIsSet(t *testing.T) {
	t.Setenv("PATH", "/bin")
	t.Setenv("KANJO_A", "1")
	os.Unsetenv("LC_ALL")
	got := boxEnv([]string{"PATH", "KANJO_A", "KANJO_A", "KANJO_UNSET_XYZ"})
	count := map[string]int{}
	for _, e := range got {
		count[strings.SplitN(e, "=", 2)[0]]++
	}
	for name, n := range count {
		if n != 1 {
			t.Errorf("%s appears %d times", name, n)
		}
	}
	if _, ok := count["KANJO_UNSET_XYZ"]; ok || count["PATH"] != 1 || count["KANJO_A"] != 1 {
		t.Errorf("env = %v", got)
	}
}

func TestRunRefusesAPromptThatIsNotARegularFile(t *testing.T) {
	r := newRunRig(t, writeFileA)
	code, _, errb := r.exec(context.Background(), "run", "--agent", "script", "--model", "m", "--prompt-file", r.cwd)
	if code != 1 || !strings.Contains(errb, "regular file") {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestRunAcceptsAPromptOfExactlyTheCap(t *testing.T) {
	r := newRunRig(t, writeFileA)
	r.setConfig("[delegate]\nmax_prompt_bytes = 10\n")
	write(t, filepath.Join(r.cwd, "ten.txt"), "0123456789")
	if code, _, errb := r.exec(context.Background(), "run", "--agent", "script", "--model", "m", "--prompt-file", "ten.txt"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestRunChecksTheGitRootBeforeAnythingElse(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.cwd, "prompt.txt"), "p")
	args := []string{"run", "--agent", "script", "--model", "m", "--prompt-file", "prompt.txt", "--script", filepath.Join(w.cwd, "not-executable.sh")}
	write(t, filepath.Join(w.cwd, "not-executable.sh"), "#!/bin/sh\n")

	os.Symlink(w.cwd, filepath.Join(w.cwd, ".git")) // a link is not a git root
	if code, _, errb := w.run(args...); code != 1 || !strings.Contains(errb, "git working tree") {
		t.Fatalf("linked .git: exit %d: %s", code, errb)
	}
	os.Remove(filepath.Join(w.cwd, ".git"))
	os.Mkdir(filepath.Join(w.cwd, ".git"), 0o700) // a real one passes the check; the script is then refused
	code, _, errb := w.run(args...)
	if code != 1 || strings.Contains(errb, "git working tree") || !strings.Contains(errb, "executable") {
		t.Fatalf("real .git, bad script: exit %d: %s", code, errb)
	}
	if _, err := os.Stat(w.data); err == nil {
		t.Fatal("the data folder was created although the script was refused")
	}
}

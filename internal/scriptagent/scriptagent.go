// Package scriptagent is a test aid: a core.AgentRunner that runs a script you
// wrote, with the prompt on its standard input. It exists so the delegation
// pipeline can be exercised end to end without a model. It never runs code from
// the repository: the script must live outside the folder the agent can write
// to, be a regular executable you own, and not be writable by anyone else.
//
// Contract with the script: standard input is the prompt; the environment has
// KANJO_SESSION, KANJO_MODEL and KANJO_RESUME (1 on the retry, else 0); the
// working folder is the worktree; exit code 75 means the quota is exhausted;
// it reports no usage.
package scriptagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// ExitQuota is the exit code that means the quota is exhausted.
const ExitQuota = 75

// Agent implements core.AgentRunner.
type Agent struct{ script string }

// New checks the script: absolute, a regular file (not a link), executable,
// owned by the current user, not writable by group or others, and not inside
// forbidden (the folder holding the worktrees, which an agent can write to).
func New(script, forbidden string) (*Agent, error) {
	if !filepath.IsAbs(script) {
		return nil, errors.New("the script path must be absolute")
	}
	fi, err := os.Lstat(script)
	if err != nil {
		return nil, fmt.Errorf("script: %w", err)
	}
	if err := checkFile(fi); err != nil {
		return nil, err
	}
	if forbidden != "" && inside(script, forbidden) {
		return nil, errors.New("the script is inside the worktrees folder, which the agent can write to")
	}
	return &Agent{script: script}, nil
}

// checkFile wants a regular executable owned by the user and not writable by others.
func checkFile(fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.Mode().IsRegular():
		return errors.New("the script must be a regular file, not a link or a folder")
	case fi.Mode().Perm()&0o100 == 0:
		return errors.New("the script is not executable")
	case fi.Mode().Perm()&0o022 != 0:
		return errors.New("the script is writable by group or others")
	case !ok || int(st.Uid) != os.Getuid():
		return errors.New("the script is not owned by you")
	}
	return nil
}

// inside reports whether path is in root, both with symbolic links resolved.
func inside(path, root string) bool {
	p, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return true // cannot tell: refuse
	}
	// root may not exist yet: resolve its parent, which must exist for anything to be inside it
	parent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil {
		return false
	}
	r := filepath.Join(parent, filepath.Base(root))
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Run implements core.AgentRunner.
func (a *Agent) Run(ctx context.Context, box core.Box, req core.AgentRequest) (core.AgentResult, error) {
	if !core.ValidSession(req.Session) || !core.ValidModel(req.Model) {
		return core.AgentResult{}, errors.New("invalid session or model")
	}
	resume := "0"
	if req.Resume {
		resume = "1"
	}
	res, err := box.Exec(ctx, core.Cmd{
		Argv:  []string{a.script},
		Env:   []string{"KANJO_SESSION=" + req.Session, "KANJO_MODEL=" + req.Model, "KANJO_RESUME=" + resume},
		Stdin: strings.NewReader(req.Prompt),
		Out:   req.Log,
		Grace: req.Grace,
	})
	if err != nil {
		return core.AgentResult{}, err
	}
	return core.AgentResult{Exit: res.Exit, Killed: res.Killed, Quota: !res.Killed && res.Exit == ExitQuota, Duration: res.Duration}, nil
}

package core

import (
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// RunVerdict is the script's verdict on a delegated run.
type RunVerdict string

const (
	RunRunning     RunVerdict = "running" // between Begin and Finish only
	RunOK          RunVerdict = "ok-to-review"
	RunStray       RunVerdict = "check-stray-files"
	RunFailed      RunVerdict = "failed"
	RunTimeout     RunVerdict = "timeout"
	RunQuota       RunVerdict = "quota-exhausted"
	RunInterrupted RunVerdict = "interrupted"
)

// LeadVerdict is the lead's verdict on a run. Abandoned is written only by clean.
type LeadVerdict string

const (
	Accepted  LeadVerdict = "accepted"
	Modified  LeadVerdict = "modified"
	Rejected  LeadVerdict = "rejected"
	Abandoned LeadVerdict = "abandoned"
)

// Run is what is known when a run starts.
type Run struct {
	Session, Task, Adapter, Model string
	StartedAt                     time.Time
	Base                          string // commit id the worktree starts from
}

// RunResult completes a run. The zero value with Verdict RunRunning is a run
// that has not finished.
type RunResult struct {
	Verdict           RunVerdict
	Exit              int
	FinishedAt        time.Time
	Launches, Retries int
	Duration          time.Duration
	Tip               string // branch tip at the end of the run
}

// Lead is the lead's verdict on a run.
type Lead struct {
	At      time.Time
	Session string
	Verdict LeadVerdict
}

// RunRecord is a run as the journal keeps it.
type RunRecord struct {
	Run
	RunResult
	Created bool // the workspace was created (so it may be removed)
	Lead    *Lead
}

var (
	refRe      = regexp.MustCompile(`^[A-Za-z0-9_./~^@{}+:,=%-]{1,200}$`)
	commitIDRe = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// BranchOf is the branch of a session's worktree.
func BranchOf(session string) string { return "delegate/" + session }

// DirOf is the worktree folder of a session, relative to the data folder.
func DirOf(session string) string { return "worktrees/" + session }

// ExitCode is the process exit code of a verdict (spec section 10).
func (v RunVerdict) ExitCode() int {
	switch v {
	case RunOK, RunStray:
		return 0
	case RunTimeout:
		return 124
	}
	return 1
}

// NewSessionID builds a session id from the time and 24 random bits.
func NewSessionID(now time.Time, rnd [3]byte) string {
	return "ses_" + now.UTC().Format("20060102T150405") + "_" + hex.EncodeToString(rnd[:])
}

// ValidRepoPath reports whether p is a repository-relative, slash separated
// path with no "." or ".." segment, no empty segment, no control character,
// valid UTF-8, at most 256 bytes.
func ValidRepoPath(p string) bool {
	if p == "" || len(p) > 256 || !utf8.ValidString(p) || strings.ContainsFunc(p, isControl) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// ValidRef reports whether s can be passed to git as a revision: no leading
// "-", no space or control character, at most 200 bytes.
func ValidRef(s string) bool { return !strings.HasPrefix(s, "-") && refRe.MatchString(s) }

// ValidCommitID reports whether s is a lower-case hex commit id (SHA-1 or SHA-256).
func ValidCommitID(s string) bool { return commitIDRe.MatchString(s) }

// Validate checks the closed rules of a run. Errors never echo values.
func (r Run) Validate() error {
	switch {
	case !ValidSession(r.Session):
		return errors.New("invalid session id")
	case !ValidTask(r.Task):
		return errors.New("invalid task label")
	case !ValidSource(r.Adapter):
		return errors.New("invalid adapter name")
	case !ValidModel(r.Model):
		return errors.New("invalid model id")
	case r.StartedAt.IsZero():
		return errors.New("run has no start time")
	case !ValidCommitID(r.Base):
		return errors.New("invalid base commit")
	}
	return nil
}

var runVerdicts = map[RunVerdict]bool{RunRunning: true, RunOK: true, RunStray: true, RunFailed: true, RunTimeout: true, RunQuota: true, RunInterrupted: true}

// Validate checks a result, whether the run is still running or final.
func (r RunResult) Validate() error {
	switch {
	case !runVerdicts[r.Verdict]:
		return errors.New("invalid run verdict")
	case (r.Verdict == RunRunning) != r.FinishedAt.IsZero():
		return errors.New("a run has a finish time exactly when it is final")
	case r.Duration < 0:
		return errors.New("duration is negative")
	case r.Tip != "" && !ValidCommitID(r.Tip):
		return errors.New("invalid tip commit")
	}
	return r.validateCounts()
}

func (r RunResult) validateCounts() error {
	switch {
	case r.Launches < 0 || r.Launches > 2:
		return errors.New("launches is outside 0..2")
	case r.Retries < 0 || r.Retries > 1 || r.Retries > r.Launches:
		return errors.New("retries is outside 0..1 or above launches")
	}
	return nil
}

// ValidateFinal is Validate and also refuses a result that is still running.
func (r RunResult) ValidateFinal() error {
	if r.Verdict == RunRunning {
		return errors.New("a final result cannot be running")
	}
	return r.Validate()
}

// Validate checks a lead verdict.
func (l Lead) Validate() error {
	switch {
	case l.At.IsZero():
		return errors.New("lead verdict has no time")
	case !ValidSession(l.Session):
		return errors.New("invalid session id")
	case l.Verdict != Accepted && l.Verdict != Modified && l.Verdict != Rejected && l.Verdict != Abandoned:
		return errors.New("invalid lead verdict")
	}
	return nil
}

// Validate checks a whole record, its lead included.
func (r RunRecord) Validate() error {
	if err := r.Run.Validate(); err != nil {
		return err
	}
	if err := r.RunResult.Validate(); err != nil {
		return err
	}
	if r.Lead == nil {
		return nil
	}
	if r.Lead.Session != r.Session {
		return errors.New("lead verdict belongs to another run")
	}
	return r.Lead.Validate()
}

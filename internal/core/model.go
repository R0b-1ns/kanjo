// Package core holds the domain model, the ports and the logic of kanjo.
// It imports no adapter and makes no system call.
package core

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// NoTask is the label of usage that belongs to no known task.
const NoTask = "-"

var (
	sourceRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	sessionRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	modelRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}$`)
	taskRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,63}$`)
)

// Tokens are token counts, all non-negative.
type Tokens struct {
	Input, Output, Reasoning, CacheWrite5m, CacheWrite1h, CacheRead int64
}

// Add returns the field-wise sum.
func (t Tokens) Add(o Tokens) Tokens {
	return Tokens{
		t.Input + o.Input, t.Output + o.Output, t.Reasoning + o.Reasoning,
		t.CacheWrite5m + o.CacheWrite5m, t.CacheWrite1h + o.CacheWrite1h, t.CacheRead + o.CacheRead,
	}
}

func (t Tokens) validate() error {
	for name, v := range map[string]int64{
		"input": t.Input, "output": t.Output, "reasoning": t.Reasoning,
		"cache_write_5m": t.CacheWrite5m, "cache_write_1h": t.CacheWrite1h, "cache_read": t.CacheRead,
	} {
		if v < 0 {
			return fmt.Errorf("%s is negative", name)
		}
	}
	return nil
}

// Key identifies a usage row.
type Key struct {
	Source, Session, Day, Task, Model string
	LongContext                       bool
}

// UsageRow is one normalized usage line (spec section 4).
type UsageRow struct {
	Key
	Messages int64
	Tokens
	CostReportedUSD *float64
}

// ValidSource reports whether s is an allowed source name.
func ValidSource(s string) bool { return sourceRe.MatchString(s) }

// ValidSession reports whether s is an allowed session id.
func ValidSession(s string) bool { return sessionRe.MatchString(s) }

// ValidModel reports whether s is an allowed model id.
func ValidModel(s string) bool { return modelRe.MatchString(s) }

// ValidTask reports whether s is NoTask or a safe task label: it may be used
// in a path because no segment is empty, "." or "..".
func ValidTask(s string) bool {
	if s == NoTask {
		return true
	}
	if !taskRe.MatchString(s) {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// ValidDay reports whether s is a real YYYY-MM-DD date.
func ValidDay(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// Validate checks every field against the closed rules of the data model.
func (r UsageRow) Validate() error {
	if err := r.Key.validate(); err != nil {
		return err
	}
	if r.Messages < 0 {
		return errors.New("messages is negative")
	}
	if err := r.Tokens.validate(); err != nil {
		return err
	}
	if c := r.CostReportedUSD; c != nil && !(*c >= 0 && *c <= math.MaxFloat64) {
		return errors.New("invalid reported cost")
	}
	return nil
}

func (k Key) validate() error {
	switch {
	case !ValidSource(k.Source):
		return errors.New("invalid source")
	case !ValidSession(k.Session):
		return errors.New("invalid session id")
	case !ValidDay(k.Day):
		return errors.New("invalid day")
	case !ValidTask(k.Task):
		return errors.New("invalid task label")
	case !ValidModel(k.Model):
		return errors.New("invalid model id")
	}
	return nil
}

// SessionInfo describes how a session's task was attributed.
type SessionInfo struct {
	Source, Session string
	TaskSource      string // "map", "heuristic" or "none"
	LabelsSeen      int
}

// Validate checks the session description.
func (s SessionInfo) Validate() error {
	switch {
	case !ValidSource(s.Source):
		return errors.New("invalid source")
	case !ValidSession(s.Session):
		return errors.New("invalid session id")
	case s.TaskSource != "map" && s.TaskSource != "heuristic" && s.TaskSource != "none":
		return errors.New("invalid task source")
	case s.LabelsSeen < 0:
		return errors.New("labels seen is negative")
	}
	return nil
}

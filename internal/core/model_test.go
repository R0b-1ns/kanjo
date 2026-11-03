package core

import (
	"math"
	"strings"
	"testing"
)

func validRow() UsageRow {
	return UsageRow{
		Key:      Key{Source: "claude", Session: "s-1", Day: "2026-11-03", Task: "ABC-12", Model: "claude-opus-5-5"},
		Messages: 2,
		Tokens:   Tokens{Input: 1, Output: 2},
	}
}

func TestUsageRowValidate(t *testing.T) {
	if err := validRow().Validate(); err != nil {
		t.Fatalf("valid row rejected: %v", err)
	}
	neg, nan, inf := -0.1, math.NaN(), math.Inf(1)
	cases := map[string]func(*UsageRow){
		"task dotdot":           func(r *UsageRow) { r.Task = "../x" },
		"task inner dotdot":     func(r *UsageRow) { r.Task = "a/../b" },
		"task empty segment":    func(r *UsageRow) { r.Task = "a//b" },
		"task formula =":        func(r *UsageRow) { r.Task = "=cmd" },
		"task formula +":        func(r *UsageRow) { r.Task = "+1" },
		"task formula @":        func(r *UsageRow) { r.Task = "@x" },
		"task space":            func(r *UsageRow) { r.Task = "a b" },
		"task too long":         func(r *UsageRow) { r.Task = strings.Repeat("a", 65) },
		"task empty":            func(r *UsageRow) { r.Task = "" },
		"model synthetic":       func(r *UsageRow) { r.Model = "<synthetic>" },
		"day bad month":         func(r *UsageRow) { r.Day = "2026-13-01" },
		"day short":             func(r *UsageRow) { r.Day = "2026-1-1" },
		"session slash":         func(r *UsageRow) { r.Session = "a/b" },
		"session leading -":     func(r *UsageRow) { r.Session = "-2-3" },
		"source upper":          func(r *UsageRow) { r.Source = "Claude" },
		"negative input":        func(r *UsageRow) { r.Input = -1 },
		"negative messages":     func(r *UsageRow) { r.Messages = -1 },
		"negative marked":       func(r *UsageRow) { r.Marked = -1 },
		"marked above messages": func(r *UsageRow) { r.Marked = r.Messages + 1 },
		"cost negative":         func(r *UsageRow) { r.CostReportedUSD = &neg },
		"cost NaN":              func(r *UsageRow) { r.CostReportedUSD = &nan },
		"cost +Inf":             func(r *UsageRow) { r.CostReportedUSD = &inf },
	}
	for name, mutate := range cases {
		r := validRow()
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestNoTaskIsValid(t *testing.T) {
	r := validRow()
	r.Task = NoTask
	if err := r.Validate(); err != nil {
		t.Fatalf("%q rejected: %v", NoTask, err)
	}
}

func TestSessionInfoValidate(t *testing.T) {
	ok := SessionInfo{Source: "claude", Session: "s-1", TaskSource: "map", LabelsSeen: 1}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}
	for name, s := range map[string]SessionInfo{
		"task source": {Source: "claude", Session: "s-1", TaskSource: "x"},
		"session":     {Source: "claude", Session: "", TaskSource: "none"},
		"labels":      {Source: "claude", Session: "s-1", TaskSource: "none", LabelsSeen: -1},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTokensAdd(t *testing.T) {
	a := Tokens{1, 2, 3, 4, 5, 6}
	got := a.Add(Tokens{10, 20, 30, 40, 50, 60})
	if want := (Tokens{11, 22, 33, 44, 55, 66}); got != want {
		t.Fatalf("Add = %+v, want %+v", got, want)
	}
}

func TestZeroCountsAndCostAreValid(t *testing.T) {
	zero := 0.0
	r := validRow()
	r.Messages, r.Tokens, r.CostReportedUSD = 0, Tokens{}, &zero
	if err := r.Validate(); err != nil {
		t.Fatalf("zero row rejected: %v", err)
	}
	half := 0.5
	r.CostReportedUSD = &half
	if err := r.Validate(); err != nil {
		t.Fatalf("cost 0.5 rejected: %v", err)
	}
	big := math.MaxFloat64
	r.CostReportedUSD = &big
	if err := r.Validate(); err != nil {
		t.Fatalf("largest finite cost rejected: %v", err)
	}
	s := SessionInfo{Source: "claude", Session: "s-1", TaskSource: "none"}
	if err := s.Validate(); err != nil {
		t.Fatalf("zero LabelsSeen rejected: %v", err)
	}
}

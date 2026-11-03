package core

import "testing"

func TestVerdictPrecedence(t *testing.T) {
	cases := []struct {
		reasons []Reason
		want    RunVerdict
	}{
		{nil, RunOK},
		{[]Reason{ReasonStray}, RunStray},
		{[]Reason{ReasonStray, ReasonAgentFailed}, RunFailed},
		{[]Reason{ReasonAgentFailed, ReasonQuota}, RunQuota},
		{[]Reason{ReasonQuota, ReasonAgentTimeout, ReasonAgentFailed}, RunTimeout},
		{[]Reason{ReasonAgentTimeout, ReasonInterrupted, ReasonStray}, RunInterrupted},
		{[]Reason{ReasonSetupFailed}, RunFailed},
	}
	for _, c := range cases {
		if got := verdictOf(c.reasons); got != c.want {
			t.Errorf("verdictOf(%v) = %s, want %s", c.reasons, got, c.want)
		}
	}
}

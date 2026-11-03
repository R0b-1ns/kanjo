package sysclock

import (
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
)

var _ core.Clock = Clock{}

func TestNowIsCurrentUTC(t *testing.T) {
	before := time.Now()
	got := Clock{}.Now()
	if got.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", got.Location())
	}
	if got.Before(before) || got.Sub(before) > time.Minute {
		t.Fatalf("Now = %v, expected about %v", got, before)
	}
}

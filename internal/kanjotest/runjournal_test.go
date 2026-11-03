package kanjotest_test

import (
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

func TestMemRunJournalContract(t *testing.T) {
	kanjotest.RunJournalContract(t, func(t *testing.T) core.RunJournal { return kanjotest.NewMemRunJournal() })
}

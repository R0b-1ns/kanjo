package kanjotest_test

import (
	"testing"
	"time"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

func TestMemStoreContract(t *testing.T) {
	kanjotest.StoreContract(t, func(t *testing.T) core.Store { return kanjotest.NewMemStore() })
}

func TestTablePricesContract(t *testing.T) {
	pb := kanjotest.TablePrices{
		Prices:    map[string]core.Price{"m": {Input: 1, Output: 2}},
		Long:      map[string]core.Price{"m": {Input: 3, Output: 4}},
		Threshold: map[string]int64{"m": 100},
	}
	kanjotest.PriceBookContract(t, pb, "m")
	if p, _ := pb.Lookup("m", true); p.Input != 3 {
		t.Fatalf("long tier price = %+v", p)
	}
	if !pb.LongContext("m", 101) || pb.LongContext("m", 100) {
		t.Fatal("threshold must be exclusive")
	}
}

func TestStaticSourceContract(t *testing.T) {
	kanjotest.UsageSourceContract(t, kanjotest.StaticSource{Batch: core.Batch{
		Rows: []core.UsageRow{{
			Key:      core.Key{Source: "claude", Session: "s-1", Day: "2026-11-03", Task: "ABC-1", Model: "m"},
			Messages: 1,
		}},
		Sessions: []core.SessionInfo{{Source: "claude", Session: "s-1", TaskSource: "none"}},
	}})
}

func TestFixedClock(t *testing.T) {
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if got := (kanjotest.FixedClock{T: at}).Now(); !got.Equal(at) {
		t.Fatalf("Now = %v", got)
	}
}

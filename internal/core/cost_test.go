package core_test

import (
	"math"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

func prices() kanjotest.TablePrices {
	return kanjotest.TablePrices{
		Prices:    map[string]core.Price{"m": {Input: 4, Output: 20, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.2}},
		Long:      map[string]core.Price{"m": {Input: 40, Output: 200, CacheWrite5m: 50, CacheWrite1h: 80, CacheRead: 2}},
		Threshold: map[string]int64{"m": 100000},
	}
}

func millionRow(model string) core.UsageRow {
	m := int64(1_000_000)
	return core.UsageRow{
		Key:      core.Key{Source: "claude", Session: "s", Day: "2026-11-03", Task: "ABC-1", Model: model},
		Messages: 3,
		Tokens:   core.Tokens{Input: m, Output: m, CacheWrite5m: m, CacheWrite1h: m, CacheRead: m},
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCostFormula(t *testing.T) {
	usd, priced := core.Cost(millionRow("m"), prices())
	if !priced || !near(usd, 37.2) {
		t.Fatalf("Cost = %v, %v; want 37.2, true", usd, priced)
	}
}

func TestCostIgnoresReasoning(t *testing.T) {
	r := millionRow("m")
	r.Reasoning = 5_000_000
	if usd, _ := core.Cost(r, prices()); !near(usd, 37.2) {
		t.Fatalf("reasoning tokens must not be charged on top of output: %v", usd)
	}
}

func TestCostUnpriced(t *testing.T) {
	if usd, priced := core.Cost(millionRow("unknown"), prices()); priced || usd != 0 {
		t.Fatalf("Cost = %v, %v; want 0, false", usd, priced)
	}
}

func TestCostReportedWins(t *testing.T) {
	r := millionRow("unknown")
	c := 1.5
	r.CostReportedUSD = &c
	if usd, priced := core.Cost(r, prices()); !priced || usd != 1.5 {
		t.Fatalf("Cost = %v, %v; want 1.5, true", usd, priced)
	}
	zero := 0.0
	r.CostReportedUSD = &zero
	if usd, priced := core.Cost(r, prices()); !priced || usd != 0 {
		t.Fatalf("a reported 0 is a free model, not a missing price: %v, %v", usd, priced)
	}
}

func TestCostLongContextTier(t *testing.T) {
	r := millionRow("m")
	r.LongContext = true
	if usd, _ := core.Cost(r, prices()); !near(usd, 372) {
		t.Fatalf("long tier cost = %v, want 372", usd)
	}
}
